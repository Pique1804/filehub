package com.filehub.backup

import android.content.Context
import android.util.Base64
import kotlinx.coroutines.delay
import kotlinx.coroutines.suspendCancellableCoroutine
import okhttp3.Call
import okhttp3.Callback
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.Request
import okhttp3.RequestBody
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import okio.BufferedSink
import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.io.InputStream
import java.net.URI
import java.security.MessageDigest
import java.util.concurrent.TimeUnit
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException

internal enum class UploadPhase { TRANSFERRING, VERIFYING, RECONNECTING }

internal enum class UploadError(val diagnostic: String) {
    CREATE_FAILED("Создание загрузки: HTTP"),
    NO_UPLOAD_ID("Сервер не вернул номер загрузки"),
    INVALID_ADDRESS("Некорректный адрес загрузки"),
    OLD_SERVER("Обнови сервер Filehub для надёжной докачки"),
    RECEIPT_FAILED("Подтверждение загрузки: HTTP"),
    EMPTY_RESPONSE("Пустой ответ сервера"),
    WRONG_SOURCE("Сервер вернул другую папку загрузки"),
    OFFSET_FAILED("Позиция докачки: HTTP"),
    NO_OFFSET("Сервер не вернул позицию докачки"),
    FILE_CHANGED("Файл изменился"),
    ORIGINAL_UNVERIFIED("Не удалось проверить исходный файл"),
    FILE_MISMATCH("Контрольная сумма или размер не совпали"),
    FILE_CHANGED_DURING_UPLOAD("Файл изменился во время загрузки"),
    STILL_FINALIZING("Сервер ещё подтверждает сохранение"),
    OPEN_FAILED("Не удалось открыть файл"),
    WRITE_FAILED("Передача файла: HTTP"),
    NO_RECEIPT("Нет подтверждения сохранения"),
    RESUME_LATER("Загрузка будет продолжена позже"),
    PERSONAL_QUOTA_FULL("Достигнут лимит личной папки"),
}

internal class UploadException(val reason: UploadError, val code: Int? = null) :
    IOException(if (code == null) reason.diagnostic else "${reason.diagnostic} $code")

internal fun Response.checkPersonalQuota() {
    if (code == 507 && (header("X-Filehub-Quota") == "full" || peekBody(1024).string().contains("ERR_PERSONAL_QUOTA"))) {
        throw UploadException(UploadError.PERSONAL_QUOTA_FULL)
    }
}

/** Both photos and videos use bounded TUS chunks and a persistent server receipt. */
class TransferConnectionException(cause: IOException) : IOException("Нет связи с сервером",cause)

object ResumableUploader {
    private const val TUS = "1.0.0"
    private const val CHUNK = 8 * 1024 * 1024
    private val client get() = Net.client.newBuilder().retryOnConnectionFailure(false)
        .readTimeout(5,TimeUnit.MINUTES).writeTimeout(2,TimeUnit.MINUTES).build()
    private fun b64(s: String) = Base64.encodeToString(s.toByteArray(Charsets.UTF_8),Base64.NO_WRAP)
    private fun hex(b: ByteArray) = b.joinToString("") { "%02x".format(it.toInt() and 255) }

    private fun sessionKey(scope: String, item: MediaScanner.Item) = hex(MessageDigest.getInstance("SHA-256")
        .digest("$scope|${item.backupKey}|${item.size}|${item.modified}".toByteArray()))

    private fun sameServer(url: String): Boolean = try {
        val a=URI(Net.base()); val b=URI(url)
        a.scheme.equals(b.scheme,true) && a.host.equals(b.host,true) && a.port==b.port &&
            b.path.matches(Regex("/files/[a-f0-9]{16,64}")) && b.query==null && b.fragment==null
    } catch(e: Exception) { false }

    @OptIn(kotlinx.coroutines.ExperimentalCoroutinesApi::class)
    internal suspend fun execute(request: Request): Response = suspendCancellableCoroutine { cont ->
        val call=client.newCall(request)
        cont.invokeOnCancellation { call.cancel() }
        call.enqueue(object: Callback {
            override fun onFailure(call: Call,e: IOException) { if(!cont.isCancelled) cont.resumeWithException(TransferConnectionException(e)) }
            override fun onResponse(call: Call,response: Response) {
                cont.resume(response) { response.close() }
            }
        })
    }

    private suspend fun create(source: String, folder: String, item: MediaScanner.Item): String {
        val metadata="filename ${b64(item.name)},source ${b64(source)},path ${b64(folder)}"
        val req=Request.Builder().url(Net.url("/files/"))
            .header("Tus-Resumable",TUS).header("Upload-Length",item.size.toString())
            .header("Upload-Metadata",metadata).post(ByteArray(0).toRequestBody()).build()
        execute(req).use { r ->
            r.checkPersonalQuota()
            if(r.code!=201) throw UploadException(UploadError.CREATE_FAILED, r.code)
            val location=r.header("Location") ?: throw UploadException(UploadError.NO_UPLOAD_ID)
            val url=URI(Net.url("/files/")).resolve(location).toString()
            if(!sameServer(url)) throw UploadException(UploadError.INVALID_ADDRESS)
            return url
        }
    }

    private suspend fun receipt(url: String, source: String): Api.UploadReceipt? {
        val id=URI(url).path.substringAfterLast('/')
        execute(Request.Builder().url(Net.url("/api/backup/status?id=$id")).build()).use { r ->
            if(r.header("X-Filehub-Backup")!="1") throw UploadException(UploadError.OLD_SERVER)
            if(r.code==404 || r.code==410) return null
            if(!r.isSuccessful) throw UploadException(UploadError.RECEIPT_FAILED, r.code)
            val o=JSONObject(r.body?.string() ?: throw UploadException(UploadError.EMPTY_RESPONSE))
            if(o.optString("source")!=source) throw UploadException(UploadError.WRONG_SOURCE)
            return Api.UploadReceipt(o.getString("path"),o.getLong("size"),o.getString("sha256"))
        }
    }

    private suspend fun preflight(source: String, folder: String, item: MediaScanner.Item, hash: String? = null): JSONObject? {
        val destination=JSONObject().put("source",source).put("path",folder.trim('/'))
            .put("filename",item.name).put("size",item.size)
        if(hash!=null) destination.put("sha256",hash)
        val request=Request.Builder().url(Net.url("/api/upload/preflight"))
            .post(destination.toString().toRequestBody("application/json".toMediaType())).build()
        return try {
            execute(request).use { response ->
                if(response.code==200) response.body?.string()?.let(::JSONObject) else null
            }
        } catch(_: IOException) {
            null
        } catch(_: JSONException) {
            null
        }
    }

    private suspend fun offset(url: String): Long? {
        execute(Request.Builder().url(url).header("Tus-Resumable",TUS).head().build()).use { r ->
            r.checkPersonalQuota()
            if(r.code==404 || r.code==410) return null
            if(!r.isSuccessful) throw UploadException(UploadError.OFFSET_FAILED, r.code)
            return r.header("Upload-Offset")?.toLongOrNull() ?: throw UploadException(UploadError.NO_OFFSET)
        }
    }

    private fun skip(stream: InputStream, count: Long) {
        var left=count
        while(left>0) { val n=stream.skip(left); if(n>0) left-=n else if(stream.read()>=0) left-- else throw UploadException(UploadError.FILE_CHANGED) }
    }

    internal suspend fun upload(ctx: Context, p: Prefs, item: MediaScanner.Item, ledger: BackupLedger,
        scope: String, checkActive: () -> Unit, onProgress: (Long,Long) -> Unit,
        onPhase: (UploadPhase) -> Unit, targetSource: String = p.source, targetFolder: String = p.folder): Api.UploadReceipt {
        val source=targetSource; val folder=targetFolder
        val legacy=ctx.getSharedPreferences("filehub_tus",Context.MODE_PRIVATE)
        val key=sessionKey(scope,item)
        val saved=ledger.transfer(scope,item)
        var url=(saved.url ?: legacy.getString(key,null))?.takeIf(::sameServer)
        var localHash=saved.hash?.takeIf { it.matches(Regex("[a-f0-9]{64}")) }
        suspend fun verify(remote: Api.UploadReceipt): Api.UploadReceipt {
            checkActive(); onPhase(UploadPhase.VERIFYING)
            if(localHash==null) {
                localHash=BackupLedger.hashLocal(ctx,item,checkActive)
                    ?: throw UploadException(UploadError.ORIGINAL_UNVERIFIED)
                ledger.saveTransfer(scope,item,url,localHash)
            }
            checkActive()
            val expectedPrefix=folder.trim('/').let { if(it.isEmpty()) "" else "$it/" }
            if(remote.size!=item.size || !remote.sha256.equals(localHash,true) || !remote.path.startsWith(expectedPrefix)) {
                ledger.saveTransfer(scope,item,null,null)
                throw UploadException(UploadError.FILE_MISMATCH)
            }
            if(!MediaScanner.unchanged(ctx,item)) throw UploadException(UploadError.FILE_CHANGED_DURING_UPLOAD)
            legacy.edit().remove(key).apply()
            onProgress(item.size,0)
            return remote
        }
        var failures=0
        var checkedExisting=false
        while(failures<4) {
            checkActive()
            try {
                url?.let { receipt(it,source) }?.let { return verify(it) }
                var at=url?.let { offset(it) }
                if(at==null || at<0 || at>item.size) {
                    if(!checkedExisting) {
                        checkedExisting=true
                        if(preflight(source,folder,item)?.optBoolean("candidate")==true) {
                            onPhase(UploadPhase.VERIFYING)
                            val hash=BackupLedger.hashLocal(ctx,item,checkActive)
                            checkActive()
                            if(hash!=null) {
                                val existing=preflight(source,folder,item,hash)
                                val savedPath=existing?.optString("path").orEmpty()
                                if(existing!=null && existing.optString("source")==source &&
                                    existing.optLong("size",-1)==item.size && existing.optString("sha256")==hash &&
                                    savedPath.isNotEmpty() && savedPath.substringBeforeLast('/',"")==folder.trim('/')) {
                                    url=null
                                    localHash=hash
                                    ledger.saveTransfer(scope,item,null,hash)
                                    return verify(Api.UploadReceipt(savedPath,item.size,hash))
                                }
                            }
                        }
                    }
                    // A new transfer hashes the bytes it actually sends.
                    localHash=null
                    url=create(source,folder,item); at=0
                    ledger.saveTransfer(scope,item,url,localHash)
                }
                checkActive(); onProgress(at,0); onPhase(UploadPhase.TRANSFERRING)
                if(at==item.size) {
                    // Retry finalization, never create a second upload for a complete one.
                    execute(Request.Builder().url(url!!).header("Tus-Resumable",TUS)
                        .header("Upload-Offset",at.toString()).patch(ByteArray(0).toRequestBody("application/offset+octet-stream".toMediaType())).build()).close()
                    val done=receipt(url!!,source) ?: throw UploadException(UploadError.STILL_FINALIZING)
                    return verify(done)
                }
                val digest=if(at==0L && localHash==null) MessageDigest.getInstance("SHA-256") else null
                val stream=ctx.contentResolver.openInputStream(item.uri) ?: throw UploadException(UploadError.OPEN_FAILED)
                stream.use { input ->
                    skip(input,at)
                    var position=at!!
                    while(position<item.size) {
                        checkActive()
                        val chunk=ByteArray(minOf(CHUNK.toLong(),item.size-position).toInt())
                        var used=0
                        while(used<chunk.size) {
                            checkActive()
                            val n=input.read(chunk,used,chunk.size-used)
                            if(n<0) throw UploadException(UploadError.FILE_CHANGED_DURING_UPLOAD)
                            if(n>0) used+=n
                        }
                        digest?.update(chunk)
                        val end=position+chunk.size
                        if(end==item.size && digest!=null) {
                            if(!MediaScanner.unchanged(ctx,item)) throw UploadException(UploadError.FILE_CHANGED_DURING_UPLOAD)
                            localHash=hex(digest.digest())
                            ledger.saveTransfer(scope,item,url,localHash)
                        }
                        val start=position
                        val body=object: RequestBody() {
                            override fun contentType()="application/offset+octet-stream".toMediaType()
                            override fun contentLength()=chunk.size.toLong()
                            override fun writeTo(sink: BufferedSink) {
                                var sent=0
                                while(sent<chunk.size) {
                                    checkActive()
                                    val n=minOf(64*1024,chunk.size-sent)
                                    sink.write(chunk,sent,n); sent+=n; onProgress(start+sent,n.toLong())
                                }
                            }
                        }
                        val req=Request.Builder().url(url!!).header("Tus-Resumable",TUS)
                            .header("Upload-Offset",position.toString()).patch(body).build()
                        execute(req).use { r ->
                            r.checkPersonalQuota()
                            if(r.code!=204 || r.header("Upload-Offset")?.toLongOrNull()!=end)
                                throw UploadException(UploadError.WRITE_FAILED, r.code)
                            if(end==item.size) {
                                val path=r.header("X-Filehub-Path")
                                val hash=r.header("X-Filehub-SHA256")
                                if(path!=null && hash!=null) return verify(Api.UploadReceipt(path,item.size,hash))
                            }
                        }
                        position=end
                    }
                }
                return verify(receipt(url!!,source) ?: throw UploadException(UploadError.NO_RECEIPT))
            } catch(e: IOException) {
                if (e is UploadException && e.reason == UploadError.PERSONAL_QUOTA_FULL) throw e
                checkActive(); failures++
                if(failures>=4) throw e
                onPhase(UploadPhase.RECONNECTING)
                delay(1000L shl (failures-1))
            }
        }
        throw UploadException(UploadError.RESUME_LATER)
    }
}
