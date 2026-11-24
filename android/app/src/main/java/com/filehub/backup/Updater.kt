package com.filehub.backup

import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Build
import android.provider.Settings
import androidx.core.content.FileProvider
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.awaitCancellation
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.launch
import okhttp3.Request
import org.json.JSONObject
import java.io.File
import java.io.InputStream
import java.io.IOException
import java.io.OutputStream
import java.security.MessageDigest

/** In-app update: read the version manifest, download the APK, hand it to the
 *  system package installer. */
object Updater {
    private const val MAX_APK_BYTES = 256L * 1024 * 1024
    private class PackageTooLarge : IOException()
    data class Info(
        val versionCode: Int,
        val versionName: String,
        val notes: String,
        val sha256: String,
        val size: Long?,
    )

    sealed interface Progress {
        data object Connecting : Progress
        data class Downloading(val downloaded: Long, val total: Long?) : Progress {
            val fraction: Double?
                get() = total?.takeIf { it > 0 }?.let { (downloaded.toDouble() / it).coerceIn(0.0, 1.0) }
        }
        data object Verifying : Progress
        data object OpeningInstaller : Progress
    }

    fun latest(): Info? {
        val s = try {
            Net.client.newCall(Request.Builder().url(Net.url("/api/app/latest")).build()).execute().use {
                if (it.isSuccessful) it.body?.string() else null
            }
        } catch (e: Exception) { null } ?: return null
        return try {
            val o = JSONObject(s)
            val vc = o.optInt("versionCode", -1)
            val hash = o.optString("sha256")
            if (vc <= 0 || !hash.matches(Regex("[0-9a-fA-F]{64}"))) null
            else Info(vc, o.optString("versionName"), o.optString("notes"), hash.lowercase(),
                o.optLong("size").takeIf { it > 0 })
        } catch (e: Exception) { null }
    }

    fun currentCode(ctx: Context): Int = try {
        val pi = ctx.packageManager.getPackageInfo(ctx.packageName, 0)
        if (Build.VERSION.SDK_INT >= 28) pi.longVersionCode.toInt() else @Suppress("DEPRECATION") pi.versionCode
    } catch (e: Exception) { 0 }

    /** Downloads the latest APK and launches the installer. Returns a status string. */
    suspend fun downloadAndInstall(ctx: Context, info: Info, onProgress: (Progress) -> Unit): String = coroutineScope {
        val text = ctx.appResources()
        var temporary: File? = null
        try {
            // Android requires the per-source install permission before opening the installer.
            if (!ctx.packageManager.canRequestPackageInstalls()) {
                ctx.startActivity(
                    Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, Uri.parse("package:${ctx.packageName}"))
                        .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
                )
                return@coroutineScope text.getString(R.string.update_install_permission)
            }
            val dir = File(ctx.filesDir, "updates").apply { mkdirs() }
            val out = File(dir, "app.apk")
            File(dir, "pending.apk").delete() // Old releases reused this staging name.
            val part = File.createTempFile("pending-", ".apk", dir).also { temporary = it }
            val digest = MessageDigest.getInstance("SHA-256")
            val request = Request.Builder().url(Net.url("/api/app/apk")).build()
            val call = Net.client.newCall(request)
            val cancelCall = launch(start = CoroutineStart.UNDISPATCHED) {
                try { awaitCancellation() } finally { call.cancel() }
            }
            val downloaded = try {
                call.execute().use { resp ->
                    ensureActive()
                    if (!resp.isSuccessful) return@coroutineScope text.getString(R.string.update_download_error, resp.code)
                    val url = resp.request.url
                    if (url.scheme != request.url.scheme || url.host != request.url.host || url.port != request.url.port) {
                        return@coroutineScope text.getString(R.string.update_invalid_package)
                    }
                    val body = resp.body ?: return@coroutineScope text.getString(R.string.update_empty_package)
                    val expectedLength = body.contentLength()
                    val manifestSize = info.size
                    if (expectedLength > MAX_APK_BYTES || (manifestSize != null && manifestSize !in 1..MAX_APK_BYTES)) throw PackageTooLarge()
                    if (expectedLength >= 0 && manifestSize != null && expectedLength != manifestSize) {
                        return@coroutineScope text.getString(R.string.update_partial_package)
                    }
                    val total = expectedLength.takeIf { it > 0 } ?: manifestSize
                    val bytes = body.byteStream().use { input ->
                        part.outputStream().use { output ->
                            val count = copyApk(input, output, digest, total ?: MAX_APK_BYTES) {
                                ensureActive()
                                onProgress(Progress.Downloading(it, total))
                            }
                            onProgress(Progress.Verifying)
                            output.fd.sync()
                            count
                        }
                    }
                    if (total != null && bytes != total) return@coroutineScope text.getString(R.string.update_partial_package)
                    bytes
                }
            } finally {
                cancelCall.cancel()
            }
            val actual = digest.digest().joinToString("") { "%02x".format(it.toInt() and 0xff) }
            if (downloaded <= 0 || actual != info.sha256) return@coroutineScope text.getString(R.string.update_hash_error)
            @Suppress("DEPRECATION")
            val packageInfo = ctx.packageManager.getPackageArchiveInfo(part.absolutePath, 0)
                ?: return@coroutineScope text.getString(R.string.update_invalid_package)
            @Suppress("DEPRECATION")
            val code = if (Build.VERSION.SDK_INT >= 28) packageInfo.longVersionCode else packageInfo.versionCode.toLong()
            if (packageInfo.packageName != ctx.packageName || code != info.versionCode.toLong() || code <= currentCode(ctx))
                return@coroutineScope text.getString(R.string.update_wrong_package)
            ensureActive()
            if (!part.renameTo(out)) return@coroutineScope text.getString(R.string.update_prepare_error)
            val uri = FileProvider.getUriForFile(ctx, "${ctx.packageName}.fileprovider", out)
            val intent = Intent(Intent.ACTION_VIEW).apply {
                setDataAndType(uri, "application/vnd.android.package-archive")
                addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_ACTIVITY_NEW_TASK)
            }
            onProgress(Progress.OpeningInstaller)
            ctx.startActivity(intent)
            text.getString(R.string.update_installer_opened)
        } catch (e: CancellationException) { throw e }
        catch (_: PackageTooLarge) { text.getString(R.string.update_package_too_large) }
        catch (e: Exception) { ensureActive(); text.getString(R.string.update_error_detail, e.message.orEmpty()) }
        finally { temporary?.delete() }
    }

    internal fun copyApk(
        input: InputStream,
        output: OutputStream,
        digest: MessageDigest,
        limit: Long = MAX_APK_BYTES,
        onBytesWritten: (Long) -> Unit,
    ): Long {
        val buffer = ByteArray(128 * 1024)
        var written = 0L
        onBytesWritten(written)
        while (true) {
            val count = input.read(buffer)
            if (count < 0) return written
            if (count > minOf(limit, MAX_APK_BYTES) - written) throw PackageTooLarge()
            output.write(buffer, 0, count)
            digest.update(buffer, 0, count)
            written += count
            onBytesWritten(written)
        }
    }
}
