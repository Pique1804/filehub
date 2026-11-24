package com.filehub.backup

import android.app.job.JobInfo
import android.app.job.JobParameters
import android.app.job.JobScheduler
import android.app.job.JobService
import android.content.ComponentName
import android.content.Context
import android.provider.MediaStore

/** Content-triggered job: fires within seconds of a new photo/video appearing in
 *  MediaStore (even when the app is closed) and kicks off an immediate backup —
 *  the "instant on capture" behaviour. Content jobs are one-shot, so it reschedules
 *  itself each run. The periodic WorkManager backup remains the fallback. */
class MediaWatchJob : JobService() {
    override fun onStartJob(params: JobParameters): Boolean {
        if (Prefs(this).auto) Backup.runNow(this)
        schedule(this) // re-arm (a content job fires once)
        jobFinished(params, false)
        return false
    }

    override fun onStopJob(params: JobParameters): Boolean = false

    companion object {
        private const val JOB_ID = 7001

        fun schedule(ctx: Context) {
            val sched = ctx.getSystemService(Context.JOB_SCHEDULER_SERVICE) as JobScheduler
            val ji = JobInfo.Builder(JOB_ID, ComponentName(ctx, MediaWatchJob::class.java))
                .addTriggerContentUri(JobInfo.TriggerContentUri(MediaStore.Images.Media.EXTERNAL_CONTENT_URI, JobInfo.TriggerContentUri.FLAG_NOTIFY_FOR_DESCENDANTS))
                .addTriggerContentUri(JobInfo.TriggerContentUri(MediaStore.Video.Media.EXTERNAL_CONTENT_URI, JobInfo.TriggerContentUri.FLAG_NOTIFY_FOR_DESCENDANTS))
                .setTriggerContentUpdateDelay(1000)  // wait 1 s for the write to settle
                .setTriggerContentMaxDelay(8000)     // but fire within 8 s
                .build()
            runCatching { sched.schedule(ji) }
        }

        fun cancel(ctx: Context) {
            (ctx.getSystemService(Context.JOB_SCHEDULER_SERVICE) as JobScheduler).cancel(JOB_ID)
        }
    }
}
