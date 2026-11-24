package com.filehub.backup

import android.content.Context
import androidx.work.CoroutineWorker
import androidx.work.WorkerParameters

/** Periodic scheduling feeds the same persistent queue as new-media events. */
class BackupTriggerWorker(ctx: Context, params: WorkerParameters) : CoroutineWorker(ctx,params) {
    override suspend fun doWork(): Result {
        if (Prefs(applicationContext).auto) Backup.runNow(applicationContext)
        return Result.success()
    }
}

/** Closes the request/worker-completion race without creating transfer chains. */
class BackupWakeWorker(ctx: Context,params: WorkerParameters): CoroutineWorker(ctx,params) {
    override suspend fun doWork(): Result {
        if(!Backup.hasPending(applicationContext))return Result.success()
        Backup.ensureScheduled(applicationContext)
        return Result.retry()
    }
}
class BackupRetryWorker(ctx: Context,params: WorkerParameters): CoroutineWorker(ctx,params) {
    override suspend fun doWork(): Result {Backup.runNow(applicationContext);return Result.success()}
}
