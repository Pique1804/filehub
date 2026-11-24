package com.filehub.backup

import android.content.Context
import android.content.ContentValues
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Process
import android.database.sqlite.SQLiteDatabase
import android.database.sqlite.SQLiteOpenHelper
import androidx.work.*
import androidx.core.app.NotificationCompat
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.withContext
import java.io.File
import java.io.FileOutputStream
import java.util.UUID

/** Shared URIs are copied while permission is held; transfers survive closing the screen. */
class ManualUploads(ctx: Context):SQLiteOpenHelper(ctx,"manual_uploads.db",null,1){
    override fun onCreate(db: SQLiteDatabase){db.execSQL("CREATE TABLE uploads(id TEXT PRIMARY KEY,account TEXT,source TEXT,folder TEXT,name TEXT,file TEXT,size INTEGER,modified INTEGER,state TEXT,error TEXT)")}
    override fun onUpgrade(db: SQLiteDatabase,oldVersion: Int,newVersion: Int)=Unit
    fun stage(ctx: Context,uri: Uri,name: String,source: String,folder: String){
        val dir=File(ctx.filesDir,"manual-uploads").apply {mkdirs()};val id=UUID.randomUUID().toString();val target=File(dir,id)
        val text=ctx.appResources()
        try{
            val input=ctx.contentResolver.openInputStream(uri)
                ?: error(text.getString(R.string.transfer_ui_share_open_failed,name))
            input.use {stream->FileOutputStream(target).use {out->stream.copyTo(out);out.fd.sync()}}
            check(target.length()>0){text.getString(R.string.transfer_ui_share_empty,name)}
            check(writableDatabase.insertOrThrow("uploads",null,ContentValues().apply {
                put("id",id);put("account",Prefs(ctx).accountScope());put("source",source);put("folder",folder);put("name",name.substringAfterLast('/').substringAfterLast('\\'))
                put("file",target.absolutePath);put("size",target.length());put("modified",target.lastModified()/1000);put("state","queued");put("error","")
            })!=-1L)
        }catch(e: Exception){target.delete();throw e}
    }

    fun stageShared(ctx: Context, uri: Uri, name: String, source: String, folder: String) {
        check(canReadSharedUri(ctx, uri)) { ctx.appResources().getString(R.string.share_read_error) }
        stage(ctx, uri, name, source, folder)
    }
    data class Job(val id: String,val source: String,val folder: String,val name: String,val file: String,val size: Long,val modified: Long)
    fun pending(account: String):List<Job> = readableDatabase.rawQuery("SELECT id,source,folder,name,file,size,modified FROM uploads WHERE account=? AND state<>'complete'",arrayOf(account)).use {c->buildList{while(c.moveToNext())add(Job(c.getString(0),c.getString(1),c.getString(2),c.getString(3),c.getString(4),c.getLong(5),c.getLong(6)))}}
    fun state(id: String,state: String,error: String=""){writableDatabase.execSQL("UPDATE uploads SET state=?,error=? WHERE id=?",arrayOf(state,error,id))}
    companion object{
        @Suppress("DEPRECATION")
        fun canReadSharedUri(ctx: Context, uri: Uri): Boolean {
            if (uri.scheme != "content") return false
            val authority = uri.authority?.substringAfterLast('@')?.takeIf { it.isNotBlank() } ?: return false
            val providers = try {
                ctx.packageManager.getPackageInfo(ctx.packageName, PackageManager.GET_PROVIDERS).providers.orEmpty()
            } catch (_: PackageManager.NameNotFoundException) { return false }
            if (providers.any { provider -> authority in provider.authority.orEmpty().split(';') }) return false
            return ctx.checkUriPermission(uri, Process.myPid(), Process.myUid(), Intent.FLAG_GRANT_READ_URI_PERMISSION) ==
                PackageManager.PERMISSION_GRANTED
        }

        fun schedule(ctx: Context){val req=OneTimeWorkRequestBuilder<ManualUploadWorker>().setConstraints(Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).build()).build();WorkManager.getInstance(ctx).enqueueUniqueWork("filehub-manual",ExistingWorkPolicy.APPEND_OR_REPLACE,req)}
    }
}
class ManualUploadWorker(ctx: Context,params: WorkerParameters):CoroutineWorker(ctx,params){
    companion object {private val transferMutex=Mutex()}
    override suspend fun doWork():Result=transferMutex.withLock {withContext(Dispatchers.IO){
        val ctx=applicationContext;val p=Prefs(ctx);val account=p.accountScope();if(!p.isConfigured())return@withContext Result.success()
        if(!Api.login(p.server,p.user,p.pass))return@withContext Result.retry()
        var failed=false
        ManualUploads(ctx).use {queue->BackupLedger(ctx).use {ledger->
            for(job in queue.pending(account)){
                currentCoroutineContext().ensureActive()
                if(Prefs(ctx).accountScope()!=account)return@withContext Result.success()
                val scope=account+"\u001fshare\u001f"+job.source+"\u001f"+job.folder
                val item=MediaScanner.Item(UUID.fromString(job.id).mostSignificantBits,job.name,Uri.fromFile(File(job.file)),isVideoName(job.name),0,job.size,job.modified,"__shared__")
                ledger.enqueue(scope,item)
                try{
                    setForeground(foreground(job.name,0,job.size))
                    Api.ensureFolder(job.source,job.folder)
                    queue.state(job.id,"uploading")
                    val context=currentCoroutineContext()
                    val receipt=ResumableUploader.upload(ctx,p,item,ledger,scope,{context.ensureActive();check(Prefs(ctx).accountScope()==account) {
                        ctx.appResources().getString(R.string.transfer_ui_share_account_changed)
                    }},
                        {bytes,_->setForegroundAsync(foreground(job.name,bytes,job.size))},{},targetSource=job.source,targetFolder=job.folder)
                    ledger.put(scope,item,receipt.path,receipt.sha256);queue.state(job.id,"complete")
                    // Only discard our private staging copy after the durable receipt.
                    File(job.file).delete()
                }catch(e: kotlinx.coroutines.CancellationException){throw e}
                catch(e: Exception){
                    queue.state(job.id,"retry",ctx.appResources().transferError(e,R.string.transfer_ui_failed))
                    failed=true
                }
            }
        }}
        if(failed){
            val retry=OneTimeWorkRequestBuilder<ManualRetryTriggerWorker>().setInitialDelay(60,java.util.concurrent.TimeUnit.SECONDS)
                .setConstraints(Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).build()).build()
            WorkManager.getInstance(ctx).enqueueUniqueWork("filehub-manual-retry",ExistingWorkPolicy.KEEP,retry)
        }
        Result.success()
    }
    }
    private fun foreground(name: String,bytes: Long,total: Long):ForegroundInfo{
        val text=applicationContext.appResources()
        val n=NotificationCompat.Builder(applicationContext,BACKUP_CHANNEL).setSmallIcon(android.R.drawable.stat_sys_upload)
            .setContentTitle(text.getString(R.string.transfer_ui_share_notification))
            .setContentText(text.getString(
                R.string.transfer_ui_share_progress,name,localizedSize(text,bytes),localizedSize(text,total),
            ))
            .setProgress(1000,(bytes.toDouble()/total.coerceAtLeast(1)*1000).toInt(),false).setOnlyAlertOnce(true).setOngoing(true).build()
        return if(android.os.Build.VERSION.SDK_INT>=29)ForegroundInfo(43,n,android.content.pm.ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC) else ForegroundInfo(43,n)
    }
}

class ManualRetryTriggerWorker(ctx: Context,params: WorkerParameters):CoroutineWorker(ctx,params){
    override suspend fun doWork():Result{ManualUploads.schedule(applicationContext);return Result.success()}
}
