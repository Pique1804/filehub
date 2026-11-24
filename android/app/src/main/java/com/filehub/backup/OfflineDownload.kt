package com.filehub.backup

import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.Build
import android.os.SystemClock
import androidx.core.app.NotificationCompat
import androidx.work.BackoffPolicy
import androidx.work.Constraints
import androidx.work.CoroutineWorker
import androidx.work.ExistingWorkPolicy
import androidx.work.ForegroundInfo
import androidx.work.NetworkType
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.WorkInfo
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import androidx.work.workDataOf
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.awaitCancellation
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import java.io.FileOutputStream
import java.io.IOException
import java.io.InputStream
import java.io.OutputStream
import java.util.UUID
import java.util.concurrent.TimeUnit
import javax.net.ssl.SSLHandshakeException
import javax.net.ssl.SSLPeerUnverifiedException

object OfflineDownload {
    const val Channel = "offline-downloads"
    const val OpenAction = "com.filehub.backup.DOWNLOADS"
    internal const val Account = "offline-account"
    internal const val File = "offline-file"
    private const val FileTag = "$File:"
    internal const val Received = "received"
    internal const val Total = "total"
    internal const val HttpStatus = "http-status"
    internal const val MaxAttempts = 4

    internal fun accountTag(ctx: Context) = "offline-downloads:" + Offline.digest(Prefs(ctx).accountScope())

    fun observe(ctx: Context) = WorkManager.getInstance(ctx).getWorkInfosByTagFlow(accountTag(ctx))

    fun key(info: WorkInfo): Pair<String, String>? = info.tags.firstOrNull { it.startsWith(FileTag) }
        ?.removePrefix(FileTag)?.split('\u001f', limit = 2)?.takeIf { it.size == 2 }?.let { it[0] to it[1] }

    fun enqueue(ctx: Context, source: String, path: String) {
        if (Offline.isPinned(ctx, source, path)) return
        val fileKey = "$source\u001f$path"
        val account = accountTag(ctx)
        val request = OneTimeWorkRequestBuilder<OfflineDownloadWorker>()
            .setInputData(workDataOf(Account to account, File to fileKey))
            .setConstraints(Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).build())
            .setBackoffCriteria(BackoffPolicy.EXPONENTIAL, 15, TimeUnit.SECONDS)
            .addTag(account)
            .addTag(FileTag + fileKey)
            .build()
        WorkManager.getInstance(ctx).enqueueUniqueWork(account + ":" + Offline.digest(fileKey), ExistingWorkPolicy.KEEP, request)
    }

    fun cancel(ctx: Context, id: UUID) {
        WorkManager.getInstance(ctx).cancelWorkById(id)
    }

    internal fun isCurrent(ctx: Context, tag: String) = Prefs(ctx).isConfigured() && accountTag(ctx) == tag
}

class OfflineDownloadWorker(ctx: Context, params: WorkerParameters) : CoroutineWorker(ctx, params) {
    companion object {
        private val queue = Mutex()
        private const val NotificationId = 44
    }

    override suspend fun doWork(): Result = withContext(Dispatchers.IO) {
        queue.withLock { download() }
    }

    private suspend fun download(): Result = coroutineScope {
        val ctx = applicationContext
        val account = inputData.getString(OfflineDownload.Account) ?: return@coroutineScope Result.failure()
        val file = inputData.getString(OfflineDownload.File)?.split('\u001f', limit = 2)
            ?.takeIf { it.size == 2 } ?: return@coroutineScope Result.failure()
        val (source, path) = file
        val prefs = Prefs(ctx)
        if (runAttemptCount >= OfflineDownload.MaxAttempts || !OfflineDownload.isCurrent(ctx, account)) {
            return@coroutineScope Result.failure()
        }
        suspend fun retryOrFail(temporary: Boolean, status: Int = 0): Result {
            currentCoroutineContext().ensureActive()
            if (temporary && hasOfflineRetryRemaining(runAttemptCount) && OfflineDownload.isCurrent(ctx, account)) {
                return Result.retry()
            }
            return Result.failure(workDataOf(OfflineDownload.HttpStatus to status))
        }
        val originalScope = prefs.accountScope()
        val server = prefs.server
        val user = prefs.user
        val pass = prefs.pass
        if (!OfflineDownload.isCurrent(ctx, account) || !Net.restore(server, user, pass)) {
            return@coroutineScope Result.failure()
        }
        val call = Net.offlineDownload(server, user, source, path) ?: return@coroutineScope Result.failure()
        val destination = Offline.fileFor(ctx, originalScope, source, path)
        val temporary = destination.resolveSibling(destination.name + ".part")
        val cancelCall = launch(start = CoroutineStart.UNDISPATCHED) {
            try {
                awaitCancellation()
            } finally {
                call.cancel()
            }
        }
        try {
            setForeground(notification(0, -1))
            destination.parentFile?.mkdirs()
            val response = try {
                call.execute()
            } catch (error: IOException) {
                return@coroutineScope retryOrFail(retryableOfflineTransport(error))
            }
            response.use {
                if (response.code != 200) {
                    return@coroutineScope retryOrFail(retryableOfflineStatus(response.code), response.code)
                }
                val body = response.body ?: return@coroutineScope Result.failure()
                val total = body.contentLength()
                setProgress(workDataOf(OfflineDownload.Received to 0L, OfflineDownload.Total to total))
                var lastUpdate = SystemClock.elapsedRealtime()
                FileOutputStream(temporary).use { out ->
                    body.byteStream().use { input ->
                        copyOfflineDownload(input, out, total) { received ->
                            currentCoroutineContext().ensureActive()
                            val now = SystemClock.elapsedRealtime()
                            if (now - lastUpdate >= 750) {
                                if (!OfflineDownload.isCurrent(ctx, account)) throw IOException("Account changed")
                                lastUpdate = now
                                setProgress(workDataOf(OfflineDownload.Received to received, OfflineDownload.Total to total))
                                setForeground(notification(received, total))
                            }
                        }
                    }
                }
            }
            currentCoroutineContext().ensureActive()
            Offline.publish(ctx, originalScope, source, path, temporary)
            Result.success()
        } catch (cancelled: CancellationException) {
            throw cancelled
        } catch (error: OfflineReadFailure) {
            retryOrFail(retryableOfflineTransport(error.networkError))
        } catch (_: Exception) {
            currentCoroutineContext().ensureActive()
            Result.failure()
        } finally {
            cancelCall.cancel()
            temporary.delete()
        }
    }

    private fun notification(received: Long, total: Long): ForegroundInfo {
        val ctx = applicationContext
        val text = ctx.appResources()
        val open = PendingIntent.getActivity(ctx, NotificationId,
            Intent(ctx, MainActivity::class.java)
                .setAction(OfflineDownload.OpenAction)
                .addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE)
        val progress = if (total > 0) (received.toDouble() / total * 100).toInt().coerceIn(0, 100) else 0
        val notification = NotificationCompat.Builder(ctx, OfflineDownload.Channel)
            .setContentTitle(text.getString(R.string.downloads_title))
            .setContentText(text.getString(R.string.downloads_in_progress))
            .setSmallIcon(android.R.drawable.stat_sys_download)
            .setContentIntent(open)
            .setVisibility(NotificationCompat.VISIBILITY_PRIVATE)
            .setOnlyAlertOnce(true)
            .setOngoing(true)
            .setProgress(100, progress, total <= 0)
            .build()
        return if (Build.VERSION.SDK_INT >= 29) {
            ForegroundInfo(NotificationId, notification, ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC)
        } else ForegroundInfo(NotificationId, notification)
    }
}

internal fun hasOfflineRetryRemaining(attempt: Int) = attempt < OfflineDownload.MaxAttempts - 1

internal fun retryableOfflineStatus(status: Int) = when (status) {
    408, 429, 500, 502, 503, 504 -> true
    else -> false
}

internal fun retryableOfflineTransport(error: IOException) =
    error !is SSLHandshakeException && error !is SSLPeerUnverifiedException

internal class OfflineReadFailure(val networkError: IOException) : IOException(networkError)

internal suspend fun copyOfflineDownload(
    input: InputStream,
    output: OutputStream,
    total: Long,
    onProgress: suspend (Long) -> Unit,
) {
    val buffer = ByteArray(128 * 1024)
    var received = 0L
    while (true) {
        val count = try {
            input.read(buffer)
        } catch (error: IOException) {
            throw OfflineReadFailure(error)
        }
        if (count == -1) break
        output.write(buffer, 0, count)
        received += count
        onProgress(received)
    }
    if (total >= 0 && received != total) throw OfflineReadFailure(IOException("Incomplete download"))
    output.flush()
}
