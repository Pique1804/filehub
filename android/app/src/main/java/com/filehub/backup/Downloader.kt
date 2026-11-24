package com.filehub.backup

import android.content.ContentValues
import android.content.Context
import android.net.Uri
import android.os.Build
import android.os.Environment
import android.provider.MediaStore
import androidx.core.content.FileProvider
import okhttp3.Request
import okhttp3.ResponseBody
import java.io.File
import java.io.FileOutputStream
import java.io.IOException
import java.io.OutputStream
import java.security.MessageDigest

object Downloader {
    private class IncompleteDownload : IOException()

    private fun copy(body: ResponseBody,out: OutputStream){
        val count=body.byteStream().use {it.copyTo(out)}
        if(body.contentLength()>=0 && count!=body.contentLength())throw IncompleteDownload()
        out.flush()
    }
    fun toDevice(ctx: Context, source: String, path: String, name: String): String {
        val text = ctx.appResources()
        return try {
            Net.client.newCall(Request.Builder().url(Api.downloadUrl(source, path)).build()).execute().use { resp ->
                if (!resp.isSuccessful) return text.getString(R.string.transfer_ui_download_http_error, resp.code)
                val body = resp.body ?: return text.getString(R.string.transfer_ui_empty_response)
                val safeName = name.substringAfterLast('/').substringAfterLast('\\')
                if (Build.VERSION.SDK_INT >= 29) {
                    val resolver = ctx.contentResolver
                    val uri = resolver.insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, ContentValues().apply {
                        put(MediaStore.Downloads.DISPLAY_NAME, safeName)
                        put(MediaStore.Downloads.IS_PENDING, 1)
                    }) ?: return text.getString(R.string.transfer_ui_download_create_failed)
                    try {
                        val out = resolver.openOutputStream(uri)
                            ?: throw IOException(text.getString(R.string.transfer_ui_open_file_failed))
                        out.use { copy(body, it) }
                        check(resolver.update(uri, ContentValues().apply {
                            put(MediaStore.Downloads.IS_PENDING, 0)
                        }, null, null) > 0)
                    } catch (e: Exception) {
                        resolver.delete(uri, null, null)
                        throw e
                    }
                    text.getString(R.string.transfer_ui_download_saved_downloads, safeName)
                } else {
                    val dir = ctx.getExternalFilesDir(Environment.DIRECTORY_DOWNLOADS)
                        ?: return text.getString(R.string.transfer_ui_download_no_folder)
                    val out = File.createTempFile("filehub-", "-$safeName", dir)
                    try {
                        FileOutputStream(out).use { copy(body, it); it.fd.sync() }
                    } catch (e: Exception) {
                        out.delete()
                        throw e
                    }
                    text.getString(R.string.transfer_ui_download_saved_path, out.absolutePath)
                }
            }
        } catch (_: IncompleteDownload) {
            text.getString(R.string.transfer_ui_download_incomplete)
        } catch (e: Exception) {
            val detail = e.message?.takeIf { it.isNotBlank() }
            if (detail == null) text.getString(R.string.transfer_ui_download_failed)
            else text.getString(R.string.transfer_ui_download_error_detail, detail)
        }
    }
    fun cacheFile(ctx: Context,source: String,path: String,name: String):Uri?=try {
        val id=MessageDigest.getInstance("SHA-256").digest((Prefs(ctx).accountScope()+"|"+source+"|"+path).toByteArray()).joinToString(""){"%02x".format(it)}
        val dir=File(ctx.cacheDir,"open/$id").apply {mkdirs()}
        val destination=File(dir,name.substringAfterLast('/').substringAfterLast('\\'))
        val staged=File.createTempFile(".download-",".tmp",dir)
        try{
            Net.client.newCall(Request.Builder().url(Api.downloadUrl(source,path)).build()).execute().use {resp->
                if(!resp.isSuccessful)throw IOException("HTTP ${resp.code}")
                FileOutputStream(staged).use {copy(resp.body ?: throw IOException("Пустой ответ"),it);it.fd.sync()}
            }
            if(!staged.renameTo(destination))throw IOException("Не удалось сохранить файл")
        }finally{staged.delete()}
        FileProvider.getUriForFile(ctx,"${ctx.packageName}.fileprovider",destination)
    }catch(e: Exception){null}
}
