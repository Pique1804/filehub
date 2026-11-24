package com.filehub.backup

import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.res.Resources
import android.content.pm.ServiceInfo
import android.os.Build
import androidx.core.app.NotificationCompat
import androidx.work.CoroutineWorker
import androidx.work.ForegroundInfo
import androidx.work.WorkerParameters
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.delay
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import java.util.concurrent.atomic.AtomicInteger
import java.util.concurrent.atomic.AtomicBoolean

/** All triggers feed one durable queue. Photos and videos have separate lanes. */
class BackupWorker(ctx: android.content.Context, params: WorkerParameters) : CoroutineWorker(ctx,params) {
    companion object { private val backupMutex=Mutex() }

    override suspend fun doWork(): Result = withContext(Dispatchers.IO) {
        backupMutex.withLock {
            try { runBackup() }
            catch(e: CancellationException) {
                Prefs(applicationContext).apply {
                    syncRunning=false
                    status=applicationContext.appResources().getString(R.string.transfer_ui_paused)
                }
                throw e
            } catch(e: Exception) {
                val text=applicationContext.appResources()
                Prefs(applicationContext).apply {
                    syncRunning=false
                    status=text.getString(
                        R.string.transfer_ui_retry_status, text.transferError(e,R.string.backup_offline),
                    )
                }
                Result.retry()
            }
        }
    }

    private suspend fun runBackup(): Result {
        val ctx=applicationContext; val p=Prefs(ctx)
        if(!p.isConfigured()) return Result.success()
        val originalScope=p.backupScope()
        val server = p.server
        if (!Net.supportsBackgroundBackup(server)) {
            p.syncRunning = false
            p.status = ctx.appResources().getString(R.string.backup_https_required)
            return Result.success()
        }
        val user = p.user
        val password = p.pass
        if (p.backupScope() != originalScope) return Result.retry()
        val preparing=SyncProgress(phaseCodes=listOf(TransferPhase.PREPARING))
        val initialResources=ctx.appResources()
        p.status=preparing.activity(initialResources); p.syncDone=0
        // Replace any progress left over from an earlier, interrupted attempt.
        setProgress(preparing.workData(initialResources))
        setForeground(fgInfo(preparing, initialResources))
        return Net.withBackupSession(server, user, password) {
            runAuthenticatedBackup(ctx, p, originalScope)
        } ?: run {
            p.status=ctx.appResources().getString(R.string.transfer_ui_login_failed)
            Result.retry()
        }
    }

    private suspend fun runAuthenticatedBackup(ctx: Context, p: Prefs, originalScope: String): Result {
        currentCoroutineContext().ensureActive()
        val policy = try {
            Api.profileStorage()
        } catch (_: java.io.IOException) {
            p.status=ctx.appResources().getString(R.string.transfer_ui_destination_unavailable)
            return Result.retry()
        } catch (_: org.json.JSONException) {
            p.status=ctx.appResources().getString(R.string.transfer_ui_destination_unavailable)
            return Result.retry()
        }
        if (Prefs(ctx).backupScope() != originalScope) return Result.retry()
        if (policy?.optBoolean("restricted") == true) {
            val personal = policy.optJSONObject("personal")
            val source = personal?.optString("source").orEmpty()
            val folder = personal?.optString("path").orEmpty()
            if (!policy.optBoolean("canWrite") || source.isBlank() || folder.isBlank()) {
                p.status=ctx.appResources().getString(R.string.transfer_ui_destination_unavailable)
                return Result.failure()
            }
            p.setBackupDestination(source, folder)
        } else {
            Api.ensureFolder(p.source,p.folder)
        }
        val scope=p.backupScope()
        if(Api.info(p.source,p.folder)?.optBoolean("isDir")!=true) {
            p.status=ctx.appResources().getString(R.string.transfer_ui_destination_unavailable); return Result.retry()
        }
        val ledger=BackupLedger(ctx)
        val monitor=Any()
        val sizes=LinkedHashMap<String,Long>(); val sent=HashMap<String,Long>(); val phases=HashMap<String,TransferPhase>()
        val names=HashMap<String,String>()
        var completedGeneration=Backup.generation(ctx)
        val processed=HashSet<String>()
        val networkLost=AtomicBoolean(false)
        fun progressKey(item: MediaScanner.Item)="${item.backupKey}|${item.size}|${item.modified}"
        var done=0; var failed=0; var wireBytes=0L
        var preparationPhase=TransferPhase.PREPARING
        var copyCheckDone=0; var copyCheckTotal=0
        var finalPhase: String?=null
        var previousBytes=0L; var previousTime=System.nanoTime()
        var lastWireTime=previousTime
        suspend fun publish() {
            val now=System.nanoTime()
            val progress=synchronized(monitor) {
                val bytes=sent.values.sum(); val totalBytes=sizes.values.sum()
                val speed=(wireBytes-previousBytes)*1e9/(now-previousTime).coerceAtLeast(1)
                previousBytes=wireBytes; previousTime=now
                val activeFiles=phases.entries.sortedBy { it.key }.map { (key,value) ->
                    val sentBytes=sent[key] ?: 0
                    val fileSize=sizes.getValue(key)
                    val pct=(sentBytes.toDouble()/fileSize.coerceAtLeast(1)*100).toInt().coerceIn(0,100)
                    val phase=if(value==TransferPhase.TRANSFERRING && sentBytes>=fileSize) TransferPhase.SAVING else value
                    ActiveTransfer(names.getValue(key),pct,phase)
                }
                val phaseCodes=if(finalPhase!=null) emptyList() else activeFiles.map { it.phase }.distinct().ifEmpty {
                    listOf(when {
                        preparationPhase == TransferPhase.RECONCILING -> preparationPhase
                        failed>0 -> TransferPhase.RETRY_WAIT
                        sizes.isNotEmpty() && done==sizes.size -> TransferPhase.DONE
                        else -> preparationPhase
                    })
                }
                SyncProgress(
                    done=done,total=sizes.size,bytes=bytes,totalBytes=totalBytes,bytesPerSecond=speed.toLong(),
                    phase=finalPhase.orEmpty(),idleSeconds=((now-lastWireTime)/1_000_000_000).coerceAtLeast(0),
                    phaseCodes=phaseCodes,transfers=activeFiles,copyCheckDone=copyCheckDone,copyCheckTotal=copyCheckTotal,
                )
            }
            val text=ctx.appResources()
            setProgress(progress.workData(text,synchronized(monitor) { failed }))
            setForeground(fgInfo(progress,text))
        }
        p.syncStartMs=System.currentTimeMillis(); p.syncRunning=true
        try {
            coroutineScope {
                val ticker=launch { while(isActive) { publish(); delay(1000) } }
                suspend fun transferPending(items: List<MediaScanner.Item>, records: Map<String, BackupLedger.Receipt>) {
                    val dueItems = items.filter { item ->
                        !BackupLedger.matches(records[item.backupKey], item) && progressKey(item) !in processed
                    }.filter { item ->
                        ledger.enqueue(scope, item)
                        ledger.due(scope, item)
                    }
                    for(item in dueItems) {
                        synchronized(monitor) {
                            sizes[progressKey(item)]=item.size; sent[progressKey(item)]=0
                            names[progressKey(item)]=item.name.take(96)
                        }
                        processed.add(progressKey(item))
                    }
                    p.syncTotal=sizes.size
                    suspend fun transfer(item: MediaScanner.Item) {
                        val job=currentCoroutineContext()
                        var lastNetworkCheck=0L
                        val check={
                            job.ensureActive()
                            if(isStopped || Prefs(ctx).backupScope()!=scope) throw CancellationException("Остановлено")
                            val now=android.os.SystemClock.elapsedRealtime()
                            if(now-lastNetworkCheck>=250){lastNetworkCheck=now;if(!Backup.networkAllowed(ctx))throw CancellationException("Ожидание Wi-Fi")}
                        }
                        try {
                            check(); ledger.state(scope,item,"uploading")
                            synchronized(monitor) {
                                if(phases.isEmpty()) lastWireTime=System.nanoTime()
                                phases[progressKey(item)]=TransferPhase.TRANSFERRING
                            }
                            val receipt=ResumableUploader.upload(ctx,p,item,ledger,scope,check,
                                { bytes,delta -> synchronized(monitor) {
                                    sent[progressKey(item)]=bytes; wireBytes+=delta
                                    if(delta>0) lastWireTime=System.nanoTime()
                                } },
                                { phase -> synchronized(monitor) { phases[progressKey(item)]=TransferPhase.fromUpload(phase) } })
                            check()
                            ledger.put(scope,item,receipt.path,receipt.sha256)
                            synchronized(monitor) { done++; p.syncDone=done }
                        } catch(e: CancellationException) { throw e }
                        catch(e: Exception) {
                            if(e is TransferConnectionException) networkLost.set(true)
                            val text=ctx.appResources()
                            val detail=text.transferError(e,R.string.transfer_ui_failed)
                            ledger.failed(scope,item,detail)
                            synchronized(monitor) { failed++ }
                            p.status=text.getString(R.string.transfer_ui_file_error,item.name,detail)
                        } finally { synchronized(monitor) { phases.remove(progressKey(item)) } }
                    }
                    val photos=dueItems.filter { !it.isVideo }; val videos=dueItems.filter { it.isVideo }
                    val photoIndex=AtomicInteger()
                    val lanes=List(2) { async(Dispatchers.IO) {
                        while(true) { val i=photoIndex.getAndIncrement(); if(i>=photos.size || networkLost.get()) break; transfer(photos[i]) }
                    }} + async(Dispatchers.IO) { for(item in videos) { if(networkLost.get()) break; transfer(item) } }
                    lanes.awaitAll()
                }
                try {
                    do {
                        val generation = Backup.generation(ctx)
                        val items = MediaScanner.scan(ctx, p.selectedAlbums)
                        transferPending(items, ledger.all(scope))
                        if (!networkLost.get()) {
                            val cutoff = System.currentTimeMillis() - ReceiptVerification.MAX_AGE_MS
                            val stale = ledger.all(scope).values.filter { it.verifiedAt < cutoff }.sortedBy { it.verifiedAt }
                            val missing = HashSet<String>()
                            ReceiptVerification.run(stale, { it.key },
                                request = { batch ->
                                    currentCoroutineContext().ensureActive()
                                    if (Prefs(ctx).backupScope() != scope) throw CancellationException("Настройки изменились")
                                    if (Backup.generation(ctx) != generation) null else Api.reconcile(p.source, batch)
                                },
                                persist = { receipt, valid ->
                                    if (valid) {
                                        ledger.verified(scope, receipt.key)
                                    } else {
                                        ledger.invalidate(scope, receipt.key)
                                        missing.add(receipt.key)
                                    }
                                },
                                progress = { checked, total -> synchronized(monitor) {
                                    preparationPhase = TransferPhase.RECONCILING
                                    copyCheckDone = checked
                                    copyCheckTotal = total
                                } })
                            synchronized(monitor) { preparationPhase = TransferPhase.PREPARING }
                            // Requeue copies the server confirmed as missing, even if another
                            // verification batch failed or a newer photo arrived in the meantime.
                            if (missing.isNotEmpty()) {
                                transferPending(items.filter { it.backupKey in missing }, ledger.all(scope))
                            }
                        }
                        completedGeneration=generation
                    } while(!networkLost.get() && Backup.generation(ctx)!=generation)
                } finally { ticker.cancel() }
            }
            p.lastRun=System.currentTimeMillis()
            val next=ledger.nextRetry(scope)
            val remaining=ledger.all(scope)
            val verificationPending=remaining.values.count {System.currentTimeMillis()-it.verifiedAt>ReceiptVerification.MAX_AGE_MS}
            p.status=ReceiptVerification.Summary(done,networkLost.get() || failed>0 || next!=null,
                verificationPending,remaining.values.count {!it.valid}).text(ctx.appResources())
            synchronized(monitor){finalPhase=p.status}
            publish()
            Backup.acknowledge(ctx,completedGeneration)
            // WorkManager persists this continuation even when auto-backup is disabled or the app exits.
            if(networkLost.get())Backup.retryLater(ctx,30000)
            else if(verificationPending>0) return Result.retry()
            else if(next!=null)Backup.retryLater(ctx,next-System.currentTimeMillis())
            return Result.success()
        } finally { ledger.close(); p.syncRunning=false }
    }

    private fun fgInfo(progress: SyncProgress, text: Resources): ForegroundInfo {
        val activity=progress.activity(text)
        val open=PendingIntent.getActivity(applicationContext,42,
            Intent(applicationContext,MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE)
        val detail=buildList {
            add(activity)
            if(progress.totalBytes>0) { add(progress.byteLabel(text)); add(progress.verifiedLabel(text)) }
            addAll(progress.activeFileLabels(text))
        }.joinToString("\n")
        val n=NotificationCompat.Builder(applicationContext,BACKUP_CHANNEL)
            .setContentTitle(if(progress.totalBytes>0) text.getString(R.string.backup_notification_percent, progress.percent) else text.getString(R.string.backup_notification_title))
            .setContentText(if(progress.totalBytes>0) "$activity · ${progress.byteLabel(text)}" else activity)
            .setStyle(NotificationCompat.BigTextStyle().bigText(detail))
            .setContentIntent(open).setOnlyAlertOnce(true).setSmallIcon(android.R.drawable.stat_sys_upload)
            .setProgress(1000,progress.barValue,progress.totalBytes==0L).setOngoing(true).build()
        return if(Build.VERSION.SDK_INT>=29) ForegroundInfo(42,n,ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC) else ForegroundInfo(42,n)
    }
}
