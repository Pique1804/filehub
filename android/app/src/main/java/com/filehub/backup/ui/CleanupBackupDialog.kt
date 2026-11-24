package com.filehub.backup.ui

import android.app.Activity
import android.os.Build
import android.provider.MediaStore
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.IntentSenderRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.selection.toggleable
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Checkbox
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.unit.dp
import com.filehub.backup.Api
import com.filehub.backup.BackupCopyState
import com.filehub.backup.BackupHealth
import com.filehub.backup.FreeSpace
import com.filehub.backup.Net
import com.filehub.backup.R
import com.filehub.backup.localizedSize
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

@Composable
internal fun BackupHealthSummary(health: BackupHealth?) {
    Text(appString(health?.statusMessage ?: R.string.backup_health_checking))
    if (health != null) {
        val resources = LocalAppResources.current
        val description = if (health.lastSuccessfulCheck > 0) {
            val date = java.text.DateFormat.getDateTimeInstance(
                java.text.DateFormat.MEDIUM, java.text.DateFormat.SHORT, resources.configuration.locales[0]
            ).format(java.util.Date(health.lastSuccessfulCheck * 1000))
            resources.getString(R.string.backup_health_checked_at, date)
        } else resources.getString(R.string.backup_health_no_date)
        Text(description, style = MaterialTheme.typography.bodySmall)
    }
}

@Composable
internal fun CleanupBackupDialog(onClose: () -> Unit, onDeleted: (Int) -> Unit) {
    val ctx = LocalContext.current
    val scope = rememberCoroutineScope()
    var health by remember { mutableStateOf<BackupHealth?>(null) }
    var review by remember { mutableStateOf<FreeSpace.Review?>(null) }
    var busy by remember { mutableStateOf(false) }
    var message by remember { mutableStateOf<Int?>(null) }
    var pendingCount by remember { mutableIntStateOf(0) }
    var protection by remember { mutableStateOf(FreeSpace.Protection()) }
    var selected by remember { mutableStateOf<Set<String>>(emptySet()) }
    var showAlbums by remember { mutableStateOf(false) }

    val deleteLauncher = rememberLauncherForActivityResult(ActivityResultContracts.StartIntentSenderForResult()) { result ->
        busy = false
        if (result.resultCode == Activity.RESULT_OK) {
            onDeleted(pendingCount)
            onClose()
        }
        pendingCount = 0
    }

    LaunchedEffect(Unit) {
        val status = withContext(Dispatchers.IO) {
            runCatching { Api.backupHealth() }.getOrDefault(BackupHealth())
        }
        health = status
        review = withContext(Dispatchers.IO) { FreeSpace.review(ctx, status.fileChecks) }
    }

    fun continueCleanup() {
        val checked = review ?: return
        if (Build.VERSION.SDK_INT < 30 || busy || selected.isEmpty()) return
        val chosen = selected
        val options = protection
        scope.launch {
            busy = true
            message = R.string.cleanup_rechecking
            val uris = withContext(Dispatchers.IO) { FreeSpace.confirmedUris(ctx, checked, chosen, options) }
            if (uris.isEmpty() || Net.cleanupVerificationServer(ctx) == null || !FreeSpace.sameScope(ctx, checked)) {
                busy = false
                review = checked.copy(verified = emptyList(), available = false)
                selected = emptySet()
                message = R.string.cleanup_recheck_failed
                return@launch
            }
            try {
                val request = MediaStore.createDeleteRequest(ctx.contentResolver, uris)
                pendingCount = uris.size
                message = null
                deleteLauncher.launch(IntentSenderRequest.Builder(request.intentSender).build())
            } catch (e: Exception) {
                busy = false
                message = R.string.cleanup_confirm_failed
            }
        }
    }

    val result = review
    val now = remember(result) { System.currentTimeMillis() / 1000 }
    val verified = result?.verified.orEmpty()
    val albums = remember(verified) { verified.map { it.item.album }.distinct().sorted() }
    val selectable = remember(result, protection, now) { result?.selectable(protection, now).orEmpty() }
    val selection = selectable.filter { it.receipt.key in selected }
    val controlsEnabled = !busy && result?.available == true && Build.VERSION.SDK_INT >= 30
    val resources = LocalAppResources.current

    fun protect(options: FreeSpace.Protection) {
        protection = options
        val eligible = result?.selectable(options, now).orEmpty().map { it.receipt.key }.toSet()
        selected = selected.intersect(eligible)
    }

    AlertDialog(
        onDismissRequest = { if (!busy) onClose() },
        title = { Text(appString(R.string.cleanup_title)) },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
                if (verified.isNotEmpty()) {
                    Text(appString(R.string.cleanup_selected_summary, selection.size,
                        localizedSize(resources, selection.sumOf { it.item.size })), style = MaterialTheme.typography.titleSmall)
                }
                LazyColumn(
                    Modifier.fillMaxWidth().heightIn(max = 380.dp),
                    verticalArrangement = Arrangement.spacedBy(7.dp),
                ) {
                    item {
                        Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                            BackupHealthSummary(health)
                            if (Net.cleanupVerificationServer(ctx) == null) Text(appString(R.string.cleanup_requires_https))
                            else if (result == null) Text(appString(R.string.cleanup_checking))
                            else {
                                Text(appString(R.string.cleanup_receipt_count, result.uploaded))
                                Text(appString(R.string.cleanup_verified_count, verified.size))
                                val copies = verified.count { it.independentCopy == BackupCopyState.Confirmed }
                                Text(appString(R.string.cleanup_separate_count, copies))
                                if (!result.available) Text(appString(R.string.cleanup_fresh_unavailable))
                                else if (verified.isEmpty()) Text(appString(R.string.cleanup_empty))
                                if (result.excluded.isNotEmpty()) {
                                    Text(appString(R.string.cleanup_excluded_heading), style = MaterialTheme.typography.titleSmall)
                                    result.excluded.forEach { (reason, count) ->
                                        Text(appString(reason.label, count), style = MaterialTheme.typography.bodySmall)
                                    }
                                }
                            }
                            if (Build.VERSION.SDK_INT < 30) Text(appString(R.string.cleanup_minimum_android))
                            message?.let { Text(appString(it)) }
                        }
                    }
                    if (verified.isNotEmpty() && Build.VERSION.SDK_INT >= 30) {
                        item {
                            HorizontalDivider()
                            CleanupChoice(appString(R.string.cleanup_keep_favorites), protection.favorites, controlsEnabled) {
                                protect(protection.copy(favorites = it))
                            }
                            CleanupChoice(appString(R.string.cleanup_keep_recent), protection.recent, controlsEnabled) {
                                protect(protection.copy(recent = it))
                            }
                            TextButton(enabled = controlsEnabled, onClick = { showAlbums = !showAlbums }) {
                                Text(appString(if (showAlbums) R.string.cleanup_hide_albums else R.string.cleanup_keep_albums, protection.albums.size))
                            }
                        }
                        if (showAlbums) {
                            items(albums, key = { "album:$it" }) { album ->
                                CleanupChoice(album, album in protection.albums, controlsEnabled) { keep ->
                                    val retained = if (keep) protection.albums + album else protection.albums - album
                                    protect(protection.copy(albums = retained))
                                }
                            }
                        }
                        item {
                            val allSelected = selection.size == selectable.size && selectable.isNotEmpty()
                            TextButton(enabled = controlsEnabled && selectable.isNotEmpty(), onClick = {
                                selected = if (allSelected) emptySet() else selectable.map { it.receipt.key }.toSet()
                            }) {
                                Text(appString(if (allSelected) R.string.cleanup_clear_selection else R.string.cleanup_select_available))
                            }
                        }
                    }
                    items(verified, key = { "file:${it.receipt.key}" }) { candidate ->
                        HorizontalDivider()
                        val keep = protection.keep(candidate.item, now)
                        val key = candidate.receipt.key
                        CleanupChoice(candidate.item.name, key in selected, controlsEnabled && keep == null) { checked ->
                            selected = if (checked) selected + key else selected - key
                        }
                        Text(appString(R.string.cleanup_file_detail, localizedSize(resources, candidate.item.size), candidate.item.album),
                            style = MaterialTheme.typography.bodySmall)
                        keep?.let { Text(appString(it.label), style = MaterialTheme.typography.bodySmall) }
                        val detail = when (candidate.independentCopy) {
                            BackupCopyState.Confirmed -> R.string.backup_copy_confirmed
                            BackupCopyState.SameVolume -> R.string.backup_copy_same_volume
                            BackupCopyState.Unconfirmed -> R.string.backup_copy_unconfirmed
                            BackupCopyState.Unavailable -> R.string.backup_copy_unavailable
                            BackupCopyState.Unknown -> R.string.backup_copy_unknown
                        }
                        Text(appString(detail), style = MaterialTheme.typography.bodySmall)
                    }
                }
            }
        },
        confirmButton = {
            TextButton(enabled = controlsEnabled && selection.isNotEmpty(), onClick = ::continueCleanup) {
                Text(appString(R.string.cleanup_continue))
            }
        },
        dismissButton = { TextButton(enabled = !busy, onClick = onClose) { Text(appString(R.string.action_close)) } },
    )
}

@Composable
private fun CleanupChoice(label: String, checked: Boolean, enabled: Boolean, select: (Boolean) -> Unit) {
    Row(
        Modifier.fillMaxWidth().toggleable(value = checked, enabled = enabled, role = Role.Checkbox, onValueChange = select)
            .heightIn(min = 48.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        Checkbox(checked = checked, onCheckedChange = null, enabled = enabled)
        Text(label, style = MaterialTheme.typography.bodyMedium)
    }
}

private val FreeSpace.Exclusion.label: Int
    get() = when (this) {
        FreeSpace.Exclusion.NoReceipt -> R.string.cleanup_excluded_no_receipt
        FreeSpace.Exclusion.LocalChanged -> R.string.cleanup_excluded_changed
        FreeSpace.Exclusion.LocalUnavailable -> R.string.cleanup_excluded_local
        FreeSpace.Exclusion.RemoteCopyMissing -> R.string.cleanup_excluded_remote
        FreeSpace.Exclusion.CheckUnavailable -> R.string.cleanup_excluded_unconfirmed
        FreeSpace.Exclusion.Limit -> R.string.cleanup_excluded_limit
    }

private val FreeSpace.Kept.label: Int
    get() = when (this) {
        FreeSpace.Kept.Favorite -> R.string.cleanup_kept_favorite
        FreeSpace.Kept.FavoriteUnknown -> R.string.cleanup_kept_unknown
        FreeSpace.Kept.Recent -> R.string.cleanup_kept_recent
        FreeSpace.Kept.Album -> R.string.cleanup_kept_album
    }
