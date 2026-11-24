package com.filehub.backup

import android.content.Context
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import androidx.work.*
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.combine
import java.util.concurrent.TimeUnit

object Backup {
    const val ONESHOT="filehub-now"
    const val PERIODIC="filehub-auto"
    const val WORK_TAG="filehub-transfer"
    private val scheduler=CoroutineScope(SupervisorJob()+Dispatchers.IO)
    @Synchronized fun generation(ctx: Context)=ctx.getSharedPreferences("filehub_queue",Context.MODE_PRIVATE).getLong("generation",0)
    @Synchronized private fun changed(ctx: Context) {
        ctx.getSharedPreferences("filehub_queue",Context.MODE_PRIVATE).edit().putLong("generation",generation(ctx)+1).commit()
    }
    fun acknowledge(ctx: Context,generation: Long) {ctx.getSharedPreferences("filehub_queue",Context.MODE_PRIVATE).edit().putLong("completed",generation).commit()}
    fun hasPending(ctx: Context)=generation(ctx)>ctx.getSharedPreferences("filehub_queue",Context.MODE_PRIVATE).getLong("completed",0)
    fun workInfos(ctx: Context)=WorkManager.getInstance(ctx).let { wm ->
        combine(wm.getWorkInfosForUniqueWorkFlow(ONESHOT),wm.getWorkInfosForUniqueWorkFlow(PERIODIC)){a,b->a+b}
    }
    fun stop(ctx: Context) {
        val wm=WorkManager.getInstance(ctx);wm.cancelAllWorkByTag(WORK_TAG);wm.cancelUniqueWork("filehub-wakeup");wm.cancelUniqueWork("filehub-retry")
        acknowledge(ctx,generation(ctx))
    }
    private fun constraints(p: Prefs)=Constraints.Builder()
        .setRequiredNetworkType(if(p.wifiOnly) NetworkType.UNMETERED else NetworkType.CONNECTED)
        .setRequiresBatteryNotLow(true).build()
    fun networkAllowed(ctx: Context): Boolean {
        if(!Prefs(ctx).wifiOnly)return true
        val cm=ctx.getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager
        return cm.getNetworkCapabilities(cm.activeNetwork)?.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_METERED)==true
    }
    fun ensureScheduled(ctx: Context) {
        val prefs = Prefs(ctx)
        if (!Net.supportsBackgroundBackup(prefs.server)) {
            prefs.status = ctx.appResources().getString(R.string.backup_https_required)
            return
        }
        val req=OneTimeWorkRequestBuilder<BackupWorker>().addTag(WORK_TAG)
            .setBackoffCriteria(BackoffPolicy.EXPONENTIAL,30,TimeUnit.SECONDS).setConstraints(constraints(prefs)).build()
        WorkManager.getInstance(ctx).enqueueUniqueWork(ONESHOT,ExistingWorkPolicy.KEEP,req)
    }
    /** Events update a generation, rather than appending unbounded chains. */
    fun runNow(ctx: Context) {
        if (!Net.supportsBackgroundBackup(Prefs(ctx).server)) {
            ensureScheduled(ctx)
            return
        }
        changed(ctx);ensureScheduled(ctx)
        val wake=OneTimeWorkRequestBuilder<BackupWakeWorker>().setInitialDelay(3,TimeUnit.SECONDS)
            .setBackoffCriteria(BackoffPolicy.LINEAR,30,TimeUnit.SECONDS).build()
        WorkManager.getInstance(ctx).enqueueUniqueWork("filehub-wakeup",ExistingWorkPolicy.KEEP,wake)
    }
    /** Configuration changes deliberately pause the current request once; TUS state survives. */
    fun settingsChanged(ctx: Context) {
        scheduler.launch {
            WorkManager.getInstance(ctx).cancelUniqueWork(ONESHOT).result.get()
            if(Prefs(ctx).auto || hasPending(ctx))runNow(ctx)
        }
    }
    fun retryLater(ctx: Context,delayMs: Long) {
        val req=OneTimeWorkRequestBuilder<BackupRetryWorker>().setInitialDelay(delayMs.coerceAtLeast(30000),TimeUnit.MILLISECONDS)
            .setConstraints(constraints(Prefs(ctx))).build()
        WorkManager.getInstance(ctx).enqueueUniqueWork("filehub-retry",ExistingWorkPolicy.REPLACE,req)
    }
    fun retryFailed(ctx: Context) {scheduler.launch {BackupLedger(ctx).use {it.resetRetries(Prefs(ctx).backupScope())};runNow(ctx)}}
    fun setAuto(ctx: Context,on: Boolean) {
        val wm=WorkManager.getInstance(ctx)
        val p=Prefs(ctx)
        if (on && !Net.supportsBackgroundBackup(p.server)) {
            wm.cancelUniqueWork(PERIODIC)
            MediaWatchJob.cancel(ctx)
            p.status = ctx.appResources().getString(R.string.backup_https_required)
            return
        }
        if(on){
            val req=PeriodicWorkRequestBuilder<BackupTriggerWorker>(p.intervalMin.coerceAtLeast(15),TimeUnit.MINUTES).setConstraints(constraints(p)).build()
            wm.enqueueUniquePeriodicWork(PERIODIC,ExistingPeriodicWorkPolicy.UPDATE,req);MediaWatchJob.schedule(ctx)
        }else{wm.cancelUniqueWork(PERIODIC);MediaWatchJob.cancel(ctx);stop(ctx)}
    }
}
