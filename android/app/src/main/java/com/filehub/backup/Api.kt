package com.filehub.backup

import android.content.ContentResolver
import android.net.Uri
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.suspendCancellableCoroutine
import okhttp3.Call
import okhttp3.Callback
import okhttp3.MediaType.Companion.toMediaTypeOrNull
import okhttp3.Request
import okhttp3.RequestBody
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import okio.BufferedSink
import okio.source
import org.json.JSONObject
import org.json.JSONArray
import java.io.IOException
import java.net.URLEncoder
import java.time.ZoneId
import kotlin.coroutines.resume

/** Blocking filehub HTTP API (call from Dispatchers.IO). */
object Api {
    data class UploadReceipt(val path: String, val size: Long, val sha256: String = "")
    data class FileSearchIntent(val type: String, val year: Int, val withoutType: String, val withoutYear: String)
    data class FileSearchResult(val files: List<FileHit>, val intent: FileSearchIntent? = null)

    private fun readSearchIntent(data: JSONObject) = data.optJSONObject("intent")?.let {
        FileSearchIntent(it.optString("type"), it.optInt("year"), it.optString("withoutType"), it.optString("withoutYear"))
    }
    private fun enc(s: String) = URLEncoder.encode(s, "UTF-8")
    private val formType = "application/x-www-form-urlencoded".toMediaTypeOrNull()
    private fun form(vararg fields: Pair<String, String>): RequestBody =
        fields.joinToString("&") { (k, v) -> "${enc(k)}=${enc(v)}" }.toRequestBody(formType)

    private fun getStr(path: String): String? = try {
        Net.client.newCall(Request.Builder().url(Net.url(path)).build()).execute().use {
            if (it.isSuccessful) it.body?.string() else null
        }
    } catch (e: Exception) { null }

    private suspend fun getSearchJson(path: String): JSONObject? = suspendCancellableCoroutine { result ->
        val call = Net.client.newCall(Request.Builder().url(Net.url(path)).build())
        result.invokeOnCancellation { call.cancel() }
        call.enqueue(object : Callback {
            override fun onFailure(call: Call, e: IOException) = result.resume(null)

            override fun onResponse(call: Call, response: Response) {
                val data = response.use {
                    if (!result.isActive || !it.isSuccessful) null
                    else runCatching { it.body?.string()?.let(::JSONObject) }.getOrNull()
                }
                result.resume(data)
            }
        })
    }

    private fun post(path: String, body: RequestBody = ByteArray(0).toRequestBody(), headers: Map<String, String> = emptyMap()): Boolean = try {
        val b = Request.Builder().url(Net.url(path)).post(body)
        headers.forEach { (k, v) -> b.header(k, v) }
        Net.client.newCall(b.build()).execute().use { it.isSuccessful }
    } catch (e: Exception) { false }

    private fun postStr(path: String, body: RequestBody = ByteArray(0).toRequestBody()): String? = try {
        Net.client.newCall(Request.Builder().url(Net.url(path)).post(body).build()).execute().use {
            if (it.isSuccessful) it.body?.string() else null
        }
    } catch (e: Exception) { null }

    /** Creates a public share link and returns its full URL, or null. */
    fun shareCreate(source: String, path: String, days: Int = 0): String? {
        return try {
            val r = postStr("/api/share/create", form("source" to source, "path" to path, "password" to "", "days" to days.toString())) ?: return null
            val u = JSONObject(r).optString("url")
            if (u.isBlank()) null else Net.url(u)
        } catch (e: Exception) { null }
    }

    fun login(server: String, user: String, pass: String, replaceAccount: Boolean = false): Boolean =
        Net.login(server, user, pass, replaceAccount)

    fun sources(): List<Source> {
        return try {
            val arr = JSONObject(getStr("/api/sources") ?: return emptyList()).optJSONArray("sources") ?: return emptyList()
            (0 until arr.length()).map { val o = arr.getJSONObject(it); Source(o.optString("name"), o.optLong("total"), o.optLong("free")) }
        } catch (e: Exception) { emptyList() }
    }

    fun list(source: String, path: String, sort: String = "name", order: String = "asc"): List<Entry> {
        return try {
            val arr = JSONObject(getStr("/api/list?source=${enc(source)}&path=${enc(path)}&sort=$sort&order=$order") ?: return emptyList())
                .optJSONArray("entries") ?: return emptyList()
            (0 until arr.length()).map { val o = arr.getJSONObject(it); Entry(o.optString("name"), o.optBoolean("isDir"), o.optLong("size"), o.optLong("modTime"), owner = o.optString("owner")) }
        } catch (e: Exception) { emptyList() }
    }

    fun photos(source: String, path: String, limit: Int = 0, offset: Int = 0): PhotoPage {
        val empty = PhotoPage(emptyList(), 0, 0, 0)
        return try {
            val q = "/api/photos?source=${enc(source)}&path=${enc(path)}" + (if (limit > 0) "&limit=$limit" else "") + (if (offset > 0) "&offset=$offset" else "")
            val obj = JSONObject(getStr(q) ?: return empty)
            val arr = obj.optJSONArray("photos") ?: return empty
            // de-dupe by (source,path): a stale duplicate index row would otherwise crash
            // the Compose grid with "key already used".
            val list = (0 until arr.length())
                .map { val o = arr.getJSONObject(it); Photo(o.optString("name"), o.optString("path"), o.optString("source"), o.optLong("modTime"), o.optBoolean("isVideo"),o.optString("revision"),o.optLong("size")) }
                .distinctBy { it.source + "|" + it.path }
            PhotoPage(list, obj.optInt("total", list.size), obj.optInt("images", 0), obj.optInt("videos", 0))
        } catch (e: Exception) { empty }
    }

    data class PhotoSearchResult(
        val photos: List<Photo>,
        val available: Boolean,
        val intent: FileSearchIntent? = null,
        val total: Int = photos.size,
        val nextCursor: String = "",
    )

    // The server applies type and capture year to the whole scoped timeline.
    // Queries without those filters keep the usual OCR results.
    suspend fun searchPhotosText(q: String, cursor: String = ""): PhotoSearchResult {
        return try {
            val zone = enc(ZoneId.systemDefault().id)
            val query = "q=${enc(q)}&mode=auto&zone=$zone&limit=240&after=${enc(cursor)}"
            val data = getSearchJson("/api/search/photos/text?$query") ?: return PhotoSearchResult(emptyList(), false)
            val arr = data.optJSONArray("photos") ?: return PhotoSearchResult(emptyList(), false)
            val out = ArrayList<Photo>()
            for (i in 0 until arr.length()) {
                val o = arr.getJSONObject(i)
                out.add(Photo(o.optString("name"), o.optString("path"), o.optString("source"), o.optLong("modTime"), o.optBoolean("isVideo"),o.optString("revision"),o.optLong("size")))
            }
            PhotoSearchResult(out, true, readSearchIntent(data), data.optInt("count", out.size), data.optString("nextCursor"))
        } catch (e: CancellationException) { throw e }
        catch (e: Exception) { PhotoSearchResult(emptyList(), false) }
    }

    data class VisualPhotoSearch(val enabled: Boolean, val available: Boolean)

    suspend fun visualPhotoSearchStatus(): VisualPhotoSearch? {
        val data = getSearchJson("/api/photos/visual-search") ?: return null
        return VisualPhotoSearch(data.optBoolean("enabled"), data.optBoolean("available"))
    }

    // Semantic search reports availability separately so the UI never presents
    // an offline model as "nothing found".
    suspend fun searchSemantic(q: String): PhotoSearchResult {
        return try {
            val data = getSearchJson("/api/search/semantic?q=${enc(q)}&k=48") ?: return PhotoSearchResult(emptyList(), false)
            val arr = data.optJSONArray("photos") ?: return PhotoSearchResult(emptyList(), false)
            val out = ArrayList<Photo>()
            for (i in 0 until arr.length()) {
                val o = arr.getJSONObject(i)
                out.add(Photo(o.optString("name"), o.optString("path"), o.optString("source"), o.optLong("modTime"), o.optBoolean("isVideo"),o.optString("revision"),o.optLong("size")))
            }
            PhotoSearchResult(out, true)
        } catch (e: CancellationException) { throw e }
        catch (e: Exception) { PhotoSearchResult(emptyList(), false) }
    }

    // text/OCR content search: matches inside documents and recognized text on images.
    fun searchContent(q: String): List<ContentHit> {
        return try {
            val arr = JSONObject(getStr("/api/search/content?q=${enc(q)}") ?: return emptyList()).optJSONArray("results") ?: return emptyList()
            (0 until arr.length()).map {
                val o = arr.getJSONObject(it)
                ContentHit(o.optString("source"), o.optString("path"), o.optString("name"), o.optLong("modTime"), o.optString("excerpt"), owner = o.optString("owner"))
            }
        } catch (e: Exception) { emptyList() }
    }

    // Files search: filename matches (with folders) merged with text/OCR content
    // matches; an entry that matched by content carries an excerpt to display.
    fun searchFiles(q: String, literal: Boolean): FileSearchResult {
        return try {
            val mode = if (literal) "literal" else "auto"
            val zone = enc(ZoneId.systemDefault().id)
            val data = JSONObject(getStr("/api/search?q=${enc(q)}&mode=$mode&zone=$zone") ?: "{}")
            val intent = readSearchIntent(data)
            val byKey = LinkedHashMap<String, FileHit>()
            data.optJSONArray("results")?.let { arr ->
                for (i in 0 until arr.length()) {
                    val o = arr.getJSONObject(i)
                    val src = o.optString("source"); val path = o.optString("path")
                    byKey["$src|$path"] = FileHit(o.optString("name"), path, src, o.optBoolean("isDir"), o.optLong("size"), o.optLong("modTime"), owner = o.optString("owner"))
                }
            }
            // Content hits use a different index. An inferred filter must also
            // constrain every result, so only merge content hits in plain search.
            if (intent != null || literal) return FileSearchResult(byKey.values.toList(), intent)
            for (h in searchContent(q.removeSurrounding("\""))) {
                val k = "${h.source}|${h.path}"
                val prev = byKey[k]
                byKey[k] = if (prev != null) prev.copy(excerpt = h.excerpt)
                           else FileHit(h.name, h.path, h.source, false, 0, h.modTime, h.excerpt, owner = h.owner)
            }
            FileSearchResult(byKey.values.toList())
        } catch (e: Exception) { FileSearchResult(emptyList()) }
    }

    fun mkdir(source: String, path: String, name: String) = post("/api/mkdir?source=${enc(source)}&path=${enc(path)}&name=${enc(name)}")
    fun delete(source: String, path: String) = post("/api/delete?source=${enc(source)}&path=${enc(path)}")
    fun rename(source: String, path: String, name: String) = post("/api/rename?source=${enc(source)}&path=${enc(path)}&name=${enc(name)}")
    fun move(source: String, path: String, destSource: String, destDir: String) =
        post("/api/move?source=${enc(source)}&path=${enc(path)}&destSource=${enc(destSource)}&dest=${enc(destDir)}")
    fun copy(source: String, path: String, destSource: String, destDir: String) =
        post("/api/copy?source=${enc(source)}&path=${enc(path)}&destSource=${enc(destSource)}&dest=${enc(destDir)}")

    // &v bumps when server thumbnail generation changes (e.g. the EXIF-rotation fix) so
    // the client re-fetches instead of serving a stale/wrong cached thumbnail.
    fun thumbUrl(source: String, path: String, w: Int = 256, revision: String = "") = Net.url("/api/thumb?source=${enc(source)}&path=${enc(path)}&w=$w&v=${enc(revision.ifBlank { "3" })}&account=${enc(Net.user)}")
    fun videoFrameUrl(source: String, path: String, w: Int = 192) =
        thumbUrl(source, path, w) + "&kind=video-frame"
    fun photoThumbUrl(p: Photo, w: Int = 256) = thumbUrl(p.source, p.path, w,p.revision)
    fun downloadUrl(source: String, path: String, inline: Boolean = false) =
        Net.url("/api/download?source=${enc(source)}&path=${enc(path)}" + if (inline) "&inline=1" else "")

    // Ephemeral no-auth playback URL (12h) for handing a video to an external player
    // (VLC/MX) — they don't send our cookie, so a signed /play/<token> link is used.
    fun streamUrl(source: String, path: String): String? {
        return try {
            val u = JSONObject(getStr("/api/stream?source=${enc(source)}&path=${enc(path)}") ?: return null).optString("url")
            if (u.isEmpty()) null else Net.url(u)
        } catch (e: Exception) { null }
    }
    fun previewUrl(source: String, path: String) = Net.url("/api/preview?source=${enc(source)}&path=${enc(path)}")

    /** Fetches a text file's content (capped) for the in-app text viewer. Falls back
     *  to cp1251 for Russian non-UTF8 files. Returns null on error/binary. */
    fun fetchText(source: String, path: String, maxBytes: Int = 1 shl 20): String? = try {
        Net.client.newCall(Request.Builder().url(downloadUrl(source, path, inline = true)).build()).execute().use { resp ->
            if (!resp.isSuccessful) null
            else resp.body?.byteStream()?.use { ins ->
                val buf = ByteArray(maxBytes); var n = 0
                while (n < maxBytes) { val r = ins.read(buf, n, maxBytes - n); if (r < 0) break; n += r }
                val bytes = buf.copyOf(n)
                val utf8 = String(bytes, Charsets.UTF_8)
                if (utf8.contains('�')) String(bytes, charset("windows-1251")) else utf8
            }
        }
    } catch (e: Exception) { null }
    fun info(source: String, path: String): JSONObject? =
        try { getStr("/api/info?source=${enc(source)}&path=${enc(path)}")?.let { JSONObject(it) } } catch (e: Exception) { null }

    fun trashList(source: String): List<TrashItem> {
        return try {
            val arr = JSONObject(getStr("/api/trash/list?source=${enc(source)}") ?: return emptyList()).optJSONArray("items") ?: return emptyList()
            (0 until arr.length()).map {
                val o = arr.getJSONObject(it)
                TrashItem(o.optString("id"), o.optString("name"), o.optString("origPath"), o.optLong("deletedAt"), o.optBoolean("isDir"), o.optLong("size"))
            }
        } catch (e: Exception) { emptyList() }
    }
    fun onThisDay(): List<Photo> {
        return try {
            val arr = JSONObject(getStr("/api/photos/onthisday") ?: return emptyList()).optJSONArray("photos") ?: return emptyList()
            (0 until arr.length()).map {
                val o = arr.getJSONObject(it); Photo(o.optString("name"), o.optString("path"), o.optString("source"), o.optLong("modTime"), o.optBoolean("isVideo"),o.optString("revision"),o.optLong("size"))
            }.distinctBy { it.source + "|" + it.path }
        } catch (e: Exception) { emptyList() }
    }
    fun favList(): Set<String> {
        return try {
            val arr = JSONObject(getStr("/api/fav/list") ?: return emptySet()).optJSONArray("items") ?: return emptySet()
            (0 until arr.length()).map { val o = arr.getJSONObject(it); o.optString("source") + "|" + o.optString("path") }.toSet()
        } catch (e: Exception) { emptySet() }
    }
    fun favToggle(source: String, path: String) = post("/api/fav/toggle?source=${enc(source)}&path=${enc(path)}")

    fun rotate(source: String, path: String, deg: Int) = post("/api/photo/rotate?source=${enc(source)}&path=${enc(path)}&deg=$deg")
    fun crop(source: String, path: String, x: Float, y: Float, w: Float, h: Float) =
        post("/api/photo/crop?source=${enc(source)}&path=${enc(path)}&x=$x&y=$y&w=$w&h=$h")
    /** Creates/returns a public album gallery link (full URL), or null. */
    fun albumShare(id: Long): String? = try {
        postStr("/api/albums/share?id=$id")?.let { val u = JSONObject(it).optString("url"); if (u.isBlank()) null else Net.url(u) }
    } catch (e: Exception) { null }

    fun albums(): List<Album> {
        return try {
            val arr = JSONObject(getStr("/api/albums/list") ?: return emptyList()).optJSONArray("albums") ?: return emptyList()
            (0 until arr.length()).map {
                val o = arr.getJSONObject(it)
                Album(o.optLong("id"), o.optString("name"), o.optInt("count"), o.optString("coverSource"), o.optString("coverPath"), o.optBoolean("shareActive"))
            }
        } catch (e: Exception) { emptyList() }
    }
    fun albumCreate(name: String): Long? = try {
        postStr("/api/albums/create?name=${enc(name)}")?.let { JSONObject(it).optLong("id").takeIf { id -> id > 0 } }
    } catch (e: Exception) { null }
    fun albumDelete(id: Long) = post("/api/albums/delete?id=$id")
    fun albumRevokeLinks(id: Long) = post("/api/albums/revoke-link?id=$id")
    fun albumAdd(id: Long, source: String, path: String) = post("/api/albums/add?id=$id&source=${enc(source)}&path=${enc(path)}")
    fun albumRemove(id: Long, source: String, path: String) = post("/api/albums/remove?id=$id&source=${enc(source)}&path=${enc(path)}")
    fun albumPhotos(id: Long): List<Photo> {
        return try {
            val arr = JSONObject(getStr("/api/albums/photos?id=$id") ?: return emptyList()).optJSONArray("photos") ?: return emptyList()
            (0 until arr.length()).map {
                val o = arr.getJSONObject(it); Photo(o.optString("name"), o.optString("path"), o.optString("source"), o.optLong("modTime"), o.optBoolean("isVideo"),o.optString("revision"),o.optLong("size"))
            }.distinctBy { it.source + "|" + it.path }
        } catch (e: Exception) { emptyList() }
    }
    private fun mapJSON(path: String): JSONObject {
        val client = Net.client
        val url = Net.url(path)
        return client.newCall(Request.Builder().url(url).build()).execute().use { response ->
            if (!response.isSuccessful) {
                if (response.code == 401) {
                    throw LocalRequestException(R.string.transfer_ui_map_sign_in, "Войдите в Filehub заново.")
                }
                throw LocalRequestException(R.string.transfer_ui_map_http_error,
                    "Не удалось получить карту (${response.code}).", response.code)
            }
            val body = response.body?.string()
                ?: throw LocalRequestException(R.string.transfer_ui_empty_response, "Сервер вернул пустой ответ.")
            if (client !== Net.client) {
                throw LocalRequestException(R.string.transfer_ui_map_account_changed, "Аккаунт изменён.")
            }
            JSONObject(body)
        }
    }
    fun mapStyle(): String = mapJSON("/api/maps/config").getJSONObject("style").toString()
    fun places(): List<Place> {
        val arr = mapJSON("/api/photos/places").getJSONArray("places")
        return (0 until arr.length()).map { val o = arr.getJSONObject(it)
            val path = o.getString("path")
            Place(o.getString("source"),path,o.getDouble("lat"),o.getDouble("lon"),o.optLong("modTime"),
                o.optString("name",path.substringAfterLast('/')),o.optBoolean("isVideo",isVideoName(path)),o.optString("revision"),o.optLong("size"))
        }.filter { it.lat.isFinite() && it.lon.isFinite() && kotlin.math.abs(it.lat)<=90 && kotlin.math.abs(it.lon)<=180 }
    }
    fun videoLibrary(): VideoLibrary? {
        val payload = getStr("/api/video/library") ?: return null
        return runCatching {
            val data = JSONObject(payload)
            val folders = data.getJSONArray("folders")
            val titles = data.getJSONArray("titles")
            VideoLibrary(
                folders = List(folders.length()) { index ->
                    val folder = folders.getJSONObject(index)
                    VideoFolder(folder.getString("source"), folder.getString("path"), folder.getString("kind"))
                },
                titles = List(titles.length()) { index ->
                    val title = titles.getJSONObject(index)
                    val videos = title.getJSONArray("videos")
                    VideoTitle(title.getString("id"), title.getString("kind"), title.getString("name"), title.optString("cover"),
                        List(videos.length()) { videoIndex ->
                            val video = videos.getJSONObject(videoIndex)
                            LibraryVideo(video.getString("source"), video.getString("path"), video.getString("name"),
                                video.optInt("season"), video.optInt("episode"), video.optDouble("position", 0.0), video.optLong("watched"))
                        }, title.optString("description"), title.optInt("year"),
                        title.optJSONArray("genres")?.let { genres -> List(genres.length()) { genres.optString(it) } }.orEmpty(),
                        title.optString("poster"), title.optString("metadataLink"), title.optString("metadataSource"),
                        title.optInt("tmdbMatchId"), title.optString("tmdbQuery"), title.optInt("tmdbQueryYear"))
                },
                partial = data.optBoolean("partial"),
                canManage = data.optBoolean("canManage"),
                tmdbEnabled = data.optBoolean("tmdbEnabled"),
                tmdbPending = data.optBoolean("tmdbPending"),
            )
        }.getOrNull()
    }

    fun tmdbSettings(): Boolean? =
        getStr("/api/admin/video/tmdb/settings")?.let { runCatching { JSONObject(it).getBoolean("configured") }.getOrNull() }

    fun updateTmdbSettings(credential: String?): Boolean? = postStr("/api/admin/video/tmdb/settings",
        form("action" to if (credential == null) "disable" else "save", "credential" to credential.orEmpty()))
        ?.let { runCatching { JSONObject(it).getBoolean("configured") }.getOrNull() }

    fun findTmdbMatches(title: String, query: String, year: String): List<TmdbMatch>? {
        val payload = getStr("/api/admin/video/tmdb/search?title=${enc(title)}&q=${enc(query)}&year=${enc(year)}")
            ?: return null
        return runCatching {
            val items = JSONArray(payload)
            List(items.length()) { index ->
                val item = items.getJSONObject(index)
                TmdbMatch(item.getInt("id"), item.optString("name"), item.optString("original"),
                    item.optInt("year"), item.optString("poster"))
            }.distinctBy(TmdbMatch::id)
        }.getOrNull()
    }

    fun saveTmdbMatch(title: String, id: Int) =
        post("/api/admin/video/tmdb/match", form("title" to title, "match" to id.toString()))

    fun setVideoFolder(source: String, path: String, kind: String) =
        post("/api/admin/video/folder", form("source" to source, "path" to path, "kind" to kind))

    fun saveVideoPosition(source: String, path: String, position: Double) =
        post("/api/video/progress", form("source" to source, "path" to path, "position" to position.toString()))

    fun hlsVideoUrl(source: String, path: String) =
        Net.url("/api/video/hls/index.m3u8?source=${enc(source)}&path=${enc(path)}")

    fun recents(): List<RecentFile> {
        return try {
            val arr = JSONObject(getStr("/api/recents") ?: return emptyList()).optJSONArray("items") ?: return emptyList()
            (0 until arr.length()).map {
                val o = arr.getJSONObject(it); RecentFile(o.optString("name"), o.optString("path"), o.optString("source"), o.optLong("modTime"), o.optLong("size"), owner = o.optString("owner"))
            }
        } catch (e: Exception) { emptyList() }
    }

    fun trashRestore(source: String, id: String) = post("/api/trash/restore?source=${enc(source)}&id=${enc(id)}")
    fun trashDelete(source: String, id: String) = post("/api/trash/delete?source=${enc(source)}&id=${enc(id)}")
    fun trashEmpty(source: String) = post("/api/trash/empty?source=${enc(source)}")

    fun uploadReceipt(source: String, path: String, filename: String, resolver: ContentResolver, uri: Uri, isVideo: Boolean): UploadReceipt? {
        val mt = (if (isVideo) "video/*" else "image/*").toMediaTypeOrNull()
        val sizeHint = runCatching { resolver.openAssetFileDescriptor(uri, "r")?.use { it.length } }.getOrNull() ?: -1L
        val body = object : RequestBody() {
            override fun contentType() = mt
            override fun contentLength() = sizeHint
            override fun writeTo(sink: BufferedSink) {
                (resolver.openInputStream(uri) ?: throw IOException("Не удалось открыть файл для загрузки"))
                    .use { input -> input.source().use { sink.writeAll(it) } }
            }
        }
        return try {
            val request = Request.Builder().url(Net.url("/api/upload?source=${enc(source)}&path=${enc(path)}"))
                .header("X-Filename", enc(filename)).post(body).build()
            Net.client.newCall(request).execute().use { resp ->
                resp.checkPersonalQuota()
                if (!resp.isSuccessful) return null
                val obj = JSONObject(resp.body?.string() ?: return null)
                val storedName = obj.optString("name")
                val size = obj.optLong("size", -1)
                if (storedName.isBlank() || size < 0) null
                else UploadReceipt(if (path.isBlank()) storedName else "$path/$storedName", size)
            }
        } catch (e: UploadException) {
            throw e
        } catch (e: Exception) { null }
    }

    fun upload(source: String, path: String, filename: String, resolver: ContentResolver, uri: Uri, isVideo: Boolean): Boolean =
        uploadReceipt(source, path, filename, resolver, uri, isVideo) != null

    fun remoteHash(source: String, path: String): Pair<Long, String>? { return try {
        val obj = JSONObject(getStr("/api/info?source=${enc(source)}&path=${enc(path)}&hash=1") ?: return null)
        val hash = obj.optString("sha256")
        if (obj.optBoolean("isDir") || hash.length != 64) null else obj.optLong("size", -1) to hash
    } catch (e: Exception) { null } }

    fun isOnline(): Boolean { return try {
        JSONObject(getStr("/api/auth") ?: return false).optBoolean("authed")
    } catch (e: Exception) { false } }

    data class PersonalQuota(val limit: Long, val used: Long, val reserved: Long, val available: Long)

    fun personalQuota(): PersonalQuota? {
        val body = getStr("/api/profile/quota") ?: return null
        return runCatching {
            val quota = JSONObject(body).optJSONObject("quota") ?: return null
            PersonalQuota(quota.getLong("limitBytes"), quota.getLong("usedBytes"),
                quota.getLong("reservedBytes"), quota.getLong("availableBytes"))
        }.getOrNull()
    }

    /** Restricted profiles take their backup destination from the server. */
    fun profileStorage(): JSONObject? {
        val request = Request.Builder().url(Net.url("/api/profile/storage")).build()
        return Net.client.newCall(request).execute().use { response ->
            if (response.code == 404) null
            else {
                if (!response.isSuccessful) throw IOException("Profile storage unavailable")
                JSONObject(response.body?.string() ?: throw IOException("Empty profile storage reply"))
            }
        }
    }

    fun ensureFolder(source: String, path: String) {
        var parent = ""
        for (seg in path.split('/').filter { it.isNotEmpty() }) {
            mkdir(source, parent, seg)
            parent = if (parent.isEmpty()) seg else "$parent/$seg"
        }
    }
    private fun backupRequest(action: String, source: String, receipts: List<BackupLedger.Receipt>): Request {
        val items = JSONArray()
        for (r in receipts) items.put(JSONObject().put("key",r.key).put("path",r.remotePath).put("size",r.size).put("sha256",r.sha256))
        val body = JSONObject().put("source",source).put("items",items).toString().toRequestBody("application/json".toMediaTypeOrNull())
        return Request.Builder().url(Net.url("/api/backup/$action")).post(body).build()
    }

    suspend fun reconcile(source: String,receipts: List<BackupLedger.Receipt>,verificationServer: String? = null): Map<String,ReceiptCheck>? = try {
        val request = backupRequest("reconcile", source, receipts)
        if (verificationServer != null && request.url.toString() != "$verificationServer/api/backup/reconcile") null
        else ResumableUploader.execute(request).use { response ->
            val sameEndpoint = response.request.url == request.url && (!request.url.isHttps || response.handshake != null)
            if (!response.isSuccessful || (verificationServer != null && !sameEndpoint)) null
            else parseReconcile(JSONObject(response.body?.string() ?: throw IOException("Пустой ответ")))
        }
    }catch(e: kotlinx.coroutines.CancellationException){throw e}catch(e: Exception){null}

    suspend fun backupCopies(source: String, receipts: List<BackupLedger.Receipt>): Map<String, BackupCopyState> = try {
        ResumableUploader.execute(backupRequest("copies", source, receipts)).use { response ->
            if (!response.isSuccessful) emptyMap()
            else parseBackupCopies(JSONObject(response.body?.string() ?: throw IOException("Пустой ответ")))
        }
    } catch (e: kotlinx.coroutines.CancellationException) { throw e }
    catch (e: Exception) { emptyMap() }

    internal fun parseBackupCopies(response: JSONObject): Map<String, BackupCopyState> = buildMap {
        val items = response.getJSONArray("items")
        for (i in 0 until items.length()) {
            val item = items.getJSONObject(i)
            val key = item.getString("key")
            require(key.isNotBlank() && !containsKey(key)) { "Некорректный ответ проверки бэкапа" }
            put(key, BackupCopyState.from(item.optString("state")))
        }
    }

    fun backupHealth(): BackupHealth = BackupHealth.from(jsonGet("/api/backup/health"))
    internal fun parseReconcile(response: JSONObject): Map<String,ReceiptCheck> {
        val result=LinkedHashMap<String,ReceiptCheck>()
        val items=response.getJSONArray("items")
        for(i in 0 until items.length()) {
            val item=items.getJSONObject(i); val key=item.getString("key")
            require(key.isNotBlank() && !result.containsKey(key)) { "Некорректный ответ сверки" }
            // Older servers omit verified, but always explicitly return a boolean valid.
            require(item.get("valid") is Boolean)
            require(!item.has("verified") || item.get("verified") is Boolean)
            result[key]=ReceiptCheck(item.getBoolean("valid"),item.optBoolean("verified",true))
        }
        return result
    }
    fun jsonGet(path: String): JSONObject = JSONObject(getStr(path)
        ?: throw LocalRequestException(R.string.transfer_ui_no_server_response, "Нет ответа сервера"))
    fun jsonPost(path: String, body: JSONObject = JSONObject()): JSONObject =
        JSONObject(postStr(path, body.toString().toRequestBody("application/json".toMediaTypeOrNull()))
            ?: throw LocalRequestException(R.string.transfer_ui_request_save_failed, "Не удалось сохранить изменение"))
    fun maintenanceAction(path: String): Boolean = post(path)
    fun parsePhoto(o: JSONObject)=Photo(o.getString("name"),o.getString("path"),o.getString("source"),o.getLong("modTime"),o.optBoolean("isVideo"),o.optString("revision"),o.optLong("size"))

}
