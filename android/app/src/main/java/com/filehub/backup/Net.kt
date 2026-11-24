package com.filehub.backup

import android.content.Context
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import coil.ImageLoader
import kotlinx.coroutines.asContextElement
import kotlinx.coroutines.withContext
import okhttp3.Call
import okhttp3.ConnectionPool
import okhttp3.Cookie
import okhttp3.CookieJar
import okhttp3.Dispatcher
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import java.net.URLEncoder
import java.io.IOException
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.TimeUnit

/** Every authentication attempt owns one immutable recipient and cookie jar. */
object Net {
    private data class Account(val base: String, val user: String, val pass: String)
    private val lock = Any()
    private var generation = 0L
    private var signedOut = false
    @Volatile private var active: Session? = null
    private val backupSession = ThreadLocal<Session>()
    @Volatile var onSessionChanged: (() -> Unit)? = null
    val server: String get() = active?.account?.base ?: ""
    val user: String get() = active?.account?.user ?: ""
    val pass: String get() = active?.account?.pass ?: ""
    private fun enc(value: String) = URLEncoder.encode(value, "UTF-8")
    private val formType = "application/x-www-form-urlencoded".toMediaType()
    private fun sameOrigin(a: HttpUrl, b: HttpUrl) = a.scheme == b.scheme && a.host == b.host && a.port == b.port

    private class Session(val account: Account, val epoch: Long) {
        val cookies = CopyOnWriteArrayList<Cookie>()
        val jar = object : CookieJar {
            override fun saveFromResponse(url: HttpUrl, values: List<Cookie>) {
                if (sameOrigin(url, account.base.toHttpUrlOrNull()!!) && values.isNotEmpty()) {
                    cookies.clear(); cookies.addAll(values)
                }
            }
            override fun loadForRequest(url: HttpUrl) =
                if (sameOrigin(url, account.base.toHttpUrlOrNull()!!)) cookies.filter { it.matches(url) } else emptyList()
        }
        // Password-bearing requests must never redirect to another recipient.
        val loginClient = OkHttpClient.Builder().cookieJar(jar)
            .followRedirects(false).followSslRedirects(false)
            .connectTimeout(20, TimeUnit.SECONDS).readTimeout(30, TimeUnit.SECONDS).build()
        val client: OkHttpClient = OkHttpClient.Builder().cookieJar(jar)
            .followSslRedirects(false)
            .authenticator { _, response ->
                val current = synchronized(lock) { active === this && generation == epoch && !signedOut }
                if (!current || response.priorResponse != null ||
                    !sameOrigin(response.request.url, account.base.toHttpUrlOrNull()!!)) null
                else if (authenticate(response.request.header("Cookie") ?: "") && synchronized(lock) { active === this && generation == epoch && !signedOut })
                    response.request.newBuilder().removeHeader("Cookie").build() else null
            }
            .dispatcher(Dispatcher().apply { maxRequests = 24; maxRequestsPerHost = 6 })
            .connectionPool(ConnectionPool(8, 5, TimeUnit.MINUTES))
            .connectTimeout(20, TimeUnit.SECONDS).readTimeout(60, TimeUnit.SECONDS)
            .writeTimeout(10, TimeUnit.MINUTES).build()
        // One login serves simultaneous thumbnail/API 401s. A response rejected
        // before another thread renewed the cookie can reuse that newer cookie.
        private val authenticationLock = Any()
        fun authenticate(rejectedCookie: String? = null): Boolean = synchronized(authenticationLock) {
            val currentCookies = jar.loadForRequest(account.base.toHttpUrlOrNull()!!)
                .filter { it.expiresAt > System.currentTimeMillis() }
            val header = currentCookies.joinToString("; ") { "${it.name}=${it.value}" }
            if (currentCookies.isNotEmpty() && (rejectedCookie == null || rejectedCookie != header)) return@synchronized true
            authenticateRequest()
        }
        private fun authenticateRequest(): Boolean = try {
            val body = "username=${enc(account.user)}&password=${enc(account.pass)}".toRequestBody(formType)
            loginClient.newCall(Request.Builder().url(account.base + "/api/login").post(body).build())
                .execute().use { it.isSuccessful }
        } catch (_: Exception) { false }
    }

    private val anonymous = OkHttpClient.Builder().build()
    private fun requestSession(): Session? {
        val pinned = backupSession.get() ?: return active
        return synchronized(lock) {
            if (signedOut || active !== pinned || generation != pinned.epoch) {
                throw IOException("Backup account changed")
            }
            pinned
        }
    }

    val client: OkHttpClient get() = requestSession()?.client ?: anonymous

    internal fun activeEndpoint(): Pair<String, OkHttpClient> = checkNotNull(active).let {
        it.account.base to it.client
    }

    internal fun offlineDownload(server: String, user: String, source: String, path: String): Call? =
        synchronized(lock) {
            val session = active ?: return null
            if (signedOut || session.account.base != normalizedServer(server) || session.account.user != user) return null
            session.client.newCall(Request.Builder().url(Api.downloadUrl(source, path)).build())
        }

    internal fun normalizedServer(server: String): String? {
        val raw = server.trim().trimEnd('/')
        val explicitScheme = "://" in raw
        val base = (if (explicitScheme) raw else "https://$raw").toHttpUrlOrNull() ?: return null
        if (base.username.isNotEmpty() || base.password.isNotEmpty() || base.query != null || base.fragment != null) return null
        val host = base.host
        val lan = isPrivateIPv4(ipv4Parts(host)) || host == "localhost" || host == "::1" ||
            host.endsWith(".local") || host.endsWith(".lan") || host.endsWith(".home.arpa")
        val endpoint = if (!explicitScheme && lan) {
            "http://$raw".toHttpUrlOrNull() ?: return null
        } else base
        return endpoint.toString().trimEnd('/')
    }

    internal fun supportsBackgroundBackup(server: String): Boolean =
        normalizedServer(server)?.toHttpUrlOrNull()?.isHttps == true

    internal suspend fun <T> withBackupSession(server: String, user: String, pass: String, block: suspend () -> T): T? {
        val endpoint = normalizedServer(server) ?: return null
        if (endpoint.toHttpUrlOrNull()?.isHttps != true) return null
        val session = selectSession(endpoint, user, pass, false) ?: return null
        if (!session.authenticate()) return null
        return withContext(backupSession.asContextElement(session)) { block() }
    }

    private fun ipv4Parts(host: String): List<Int> = host.split('.')
        .takeIf { parts -> parts.size == 4 && parts.all { (it.toIntOrNull() ?: -1) in 0..255 } }
        ?.map(String::toInt).orEmpty()

    private fun isPrivateIPv4(address: List<Int>): Boolean = address.size == 4 && when (address[0]) {
            10, 127 -> true
            172 -> address[1] in 16..31
            192 -> address[1] == 168
            169 -> address[1] == 254
            else -> false
    }

    internal fun isDirectLocalIPv4(host: String, local: ByteArray, prefix: Int): Boolean {
        val address = ipv4Parts(host)
        if (!isPrivateIPv4(address) || address[0] == 127 || local.size != 4 || prefix !in 1..32) return false
        return address.indices.all { i ->
            val bits = (prefix - i * 8).coerceIn(0, 8)
            val mask = 0xff shl (8 - bits) and 0xff
            (address[i] and mask) == (local[i].toInt() and mask)
        }
    }

    /** A trusted home-LAN connection is eligible for the existing byte check. */
    internal fun cleanupVerificationServer(ctx: Context): String? {
        val selected = server
        val endpoint = selected.toHttpUrlOrNull() ?: return null
        if (endpoint.isHttps) return selected
        val manager = ctx.getSystemService(ConnectivityManager::class.java) ?: return null
        val network = manager.activeNetwork ?: return null
        val caps = manager.getNetworkCapabilities(network) ?: return null
        if (!caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) && !caps.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET)) return null
        val links = manager.getLinkProperties(network) ?: return null
        if (links.httpProxy != null) return null
        return selected.takeIf {
            links.linkAddresses.any { isDirectLocalIPv4(endpoint.host, it.address.address, it.prefixLength) }
        }
    }

    private fun selectSession(server: String, user: String, pass: String, replaceAccount: Boolean): Session? {
        val base = normalizedServer(server) ?: return null
        val candidate = Account(base, user, pass)
        var changed = false
        val session = synchronized(lock) {
            if (replaceAccount) { generation++; signedOut = false }
            else if (signedOut || active != null && active?.account != candidate) return null
            // Keep the selected immutable recipient available while offline. A
            // failed request is still false; only a successful login is persisted.
            if (active == null || replaceAccount) {
                if (!replaceAccount) generation++
                active = Session(candidate, generation); loader = null; changed = true
            }
            active!!
        }
        if (changed) onSessionChanged?.invoke()
        return session
    }

    /** Select only a previously saved recipient; no network I/O or login claim. */
    fun restore(server: String, user: String, pass: String): Boolean =
        selectSession(server, user, pass, false) != null

    fun login(server: String, user: String, pass: String, replaceAccount: Boolean = false): Boolean {
        val session = selectSession(server, user, pass, replaceAccount) ?: return false
        if (!session.authenticate()) return false
        return synchronized(lock) { !signedOut && active === session && generation == session.epoch }
    }

    @Volatile private var loader: ImageLoader? = null
    fun imageLoader(ctx: Context): ImageLoader = synchronized(lock) {
        loader ?: ImageLoader.Builder(ctx).okHttpClient(client).crossfade(true).build().also { loader = it }
    }
    fun base(): String = requestSession()?.account?.base ?: error("Войдите в Filehub")
    fun url(path: String): String = base() + path
    fun loggedIn(): Boolean = active?.cookies?.isNotEmpty() == true

    fun revokeSession(): Boolean {
        val session = active ?: return true
        if (session.cookies.none { it.name == "fh_session" }) return true
        return try {
            val request = Request.Builder().url(session.account.base + "/api/logout")
                .post("".toRequestBody()).build()
            val call = session.loginClient.newCall(request)
            call.timeout().timeout(5, TimeUnit.SECONDS)
            call.execute().use { it.isSuccessful || it.code == 401 }
        } catch (_: java.io.IOException) { false }
    }

    fun clear() {
        synchronized(lock) {
            generation++; signedOut = true
            active?.client?.dispatcher?.cancelAll(); active?.cookies?.clear()
            active = null; loader = null
        }
        onSessionChanged?.invoke()
    }
}
