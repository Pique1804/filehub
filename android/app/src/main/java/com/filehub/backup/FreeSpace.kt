package com.filehub.backup

import android.content.Context
import android.net.Uri
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive

/** Only exact, currently reachable server copies may be offered for deletion. */
object FreeSpace {
    private const val MAX_PER_CONFIRMATION = 500
    private const val RECENT_SECONDS = 7 * 24 * 60 * 60L

    data class Candidate(
        val item: MediaScanner.Item,
        val receipt: BackupLedger.Receipt,
        val independentCopy: BackupCopyState = BackupCopyState.Unknown,
    )

    enum class Exclusion { NoReceipt, LocalChanged, LocalUnavailable, RemoteCopyMissing, CheckUnavailable, Limit }
    enum class Kept { Favorite, FavoriteUnknown, Recent, Album }

    data class Protection(
        val favorites: Boolean = true,
        val recent: Boolean = true,
        val albums: Set<String> = emptySet(),
    ) {
        fun keep(item: MediaScanner.Item, nowSeconds: Long): Kept? = when {
            favorites && item.favorite == true -> Kept.Favorite
            favorites && item.favorite == null -> Kept.FavoriteUnknown
            recent && maxOf(item.dateAdded, item.modified) >= nowSeconds - RECENT_SECONDS -> Kept.Recent
            item.album in albums -> Kept.Album
            else -> null
        }
    }

    data class Review(
        val scope: String,
        val uploaded: Int,
        val verified: List<Candidate>,
        val available: Boolean,
        val excluded: Map<Exclusion, Int> = emptyMap(),
    ) {
        fun selectable(protection: Protection, nowSeconds: Long): List<Candidate> =
            if (available) verified.filter { protection.keep(it.item, nowSeconds) == null } else emptyList()

        internal fun exactSelection(keys: Set<String>, protection: Protection, nowSeconds: Long): List<Candidate> {
            val selected = selectable(protection, nowSeconds).filter { it.receipt.key in keys }
            return if (selected.size == keys.size) selected else emptyList()
        }
    }

    private data class Verification(val verified: List<Candidate>, val excluded: Map<Exclusion, Int>)

    fun candidateCount(ctx: Context, selectedOnly: Boolean = false): Int {
        val p = Prefs(ctx)
        return try {
            BackupLedger(ctx).use { ledger ->
                val records = ledger.all(p.backupScope())
                MediaScanner.scan(ctx, if (selectedOnly) p.selectedAlbums else emptySet())
                    .count { BackupLedger.matches(records[it.backupKey], it) }
            }
        } catch (e: Exception) { 0 }
    }

    fun sameScope(ctx: Context, review: Review): Boolean = Prefs(ctx).backupScope() == review.scope

    suspend fun review(ctx: Context, fileChecks: Boolean): Review {
        val prefs = Prefs(ctx)
        val scope = prefs.backupScope()
        if (!prefs.isConfigured() || Net.cleanupVerificationServer(ctx) == null) return Review(scope, 0, emptyList(), false)
        var uploaded = 0
        val excluded = mutableMapOf<Exclusion, Int>()
        return try {
            val records = BackupLedger(ctx).use { it.all(scope) }
            val candidates = ArrayList<Candidate>()
            for (item in MediaScanner.scan(ctx)) {
                val receipt = records[item.backupKey]
                when {
                    receipt == null -> excluded.increment(Exclusion.NoReceipt)
                    !BackupLedger.matchesRevision(receipt, item) -> excluded.increment(Exclusion.LocalChanged)
                    else -> {
                        uploaded++
                        if (receipt.valid) candidates.add(Candidate(item, receipt))
                        else excluded.increment(Exclusion.RemoteCopyMissing)
                    }
                }
            }
            val result = verifyCurrent(ctx, scope, prefs.source, candidates)
                ?: return Review(scope, uploaded, emptyList(), false, excluded)
            for ((reason, count) in result.excluded) excluded.increment(reason, count)
            val reviewed = if (fileChecks) {
                result.verified.chunked(ReceiptVerification.BATCH_SIZE).flatMap { batch ->
                    if (Prefs(ctx).backupScope() != scope) return Review(scope, uploaded, emptyList(), false, excluded)
                    val proof = Api.backupCopies(prefs.source, batch.map { it.receipt })
                    batch.map { candidate ->
                        candidate.copy(independentCopy = proof[candidate.receipt.key] ?: BackupCopyState.Unknown)
                    }
                }
            } else result.verified
            if (Prefs(ctx).backupScope() != scope) return Review(scope, uploaded, emptyList(), false, excluded)
            Review(scope, uploaded, reviewed, true, excluded)
        } catch (e: CancellationException) { throw e }
        catch (e: Exception) { Review(scope, uploaded, emptyList(), false, excluded) }
    }

    // Keep the operation all-or-nothing if any selected item changes. Independent
    // backups are advisory and never replace the live remote and local hashes.
    suspend fun confirmedUris(ctx: Context, review: Review, selected: Set<String>, protection: Protection): List<Uri> {
        val prefs = Prefs(ctx)
        if (Net.cleanupVerificationServer(ctx) == null || !sameScope(ctx, review) || !review.available || selected.isEmpty()) return emptyList()
        val now = System.currentTimeMillis() / 1000
        val candidates = review.exactSelection(selected, protection, now)
        if (candidates.isEmpty()) return emptyList()
        return try {
            val result = verifyCurrent(ctx, review.scope, prefs.source, candidates) ?: return emptyList()
            if (result.verified.size != candidates.size) return emptyList()
            val job = currentCoroutineContext()
            val checkedAt = System.currentTimeMillis() / 1000
            val uris = result.verified.map { candidate ->
                job.ensureActive()
                val local = MediaScanner.cleanupItem(ctx, candidate.item) ?: return emptyList()
                if (!BackupLedger.matchesRevision(candidate.receipt, local) || protection.keep(local, checkedAt) != null) return emptyList()
                local.uri
            }
            if (Net.cleanupVerificationServer(ctx) != null && sameScope(ctx, review)) uris else emptyList()
        } catch (e: CancellationException) { throw e }
        catch (e: Exception) { emptyList() }
    }

    private suspend fun verifyCurrent(
        ctx: Context, scope: String, source: String, candidates: List<Candidate>,
    ): Verification? {
        val job = currentCoroutineContext()
        val verificationServer = Net.cleanupVerificationServer(ctx) ?: return null
        val verified = ArrayList<Candidate>()
        val excluded = mutableMapOf<Exclusion, Int>()
        var visited = 0
        for (batch in candidates.chunked(ReceiptVerification.BATCH_SIZE)) {
            if (Net.cleanupVerificationServer(ctx) != verificationServer || Prefs(ctx).backupScope() != scope) return null
            val checks = Api.reconcile(source, batch.map { it.receipt }, verificationServer) ?: return null
            if (Net.cleanupVerificationServer(ctx) != verificationServer) return null
            for (candidate in batch) {
                job.ensureActive()
                if (Prefs(ctx).backupScope() != scope) return null
                visited++
                val check = checks[candidate.receipt.key]
                if (check == null || !check.verified || !check.valid) {
                    excluded.increment(if (check?.verified == true) Exclusion.RemoteCopyMissing else Exclusion.CheckUnavailable)
                    continue
                }
                val local = MediaScanner.cleanupItem(ctx, candidate.item)
                if (local == null || !BackupLedger.matchesRevision(candidate.receipt, local)) {
                    excluded.increment(if (local == null) Exclusion.LocalUnavailable else Exclusion.LocalChanged)
                    continue
                }
                val hash = BackupLedger.hashLocal(ctx, local) { job.ensureActive() }
                if (hash == null || !candidate.receipt.sha256.equals(hash, ignoreCase = true) || !MediaScanner.unchanged(ctx, local)) {
                    excluded.increment(if (hash == null) Exclusion.LocalUnavailable else Exclusion.LocalChanged)
                    continue
                }
                verified.add(candidate.copy(item = local))
                if (verified.size == MAX_PER_CONFIRMATION) {
                    excluded.increment(Exclusion.Limit, candidates.size - visited)
                    return if (Prefs(ctx).backupScope() == scope) Verification(verified, excluded) else null
                }
            }
        }
        return if (Prefs(ctx).backupScope() == scope) Verification(verified, excluded) else null
    }

    private fun MutableMap<Exclusion, Int>.increment(reason: Exclusion, count: Int = 1) {
        if (count > 0) this[reason] = (this[reason] ?: 0) + count
    }
}
