package com.filehub.backup.ui

import com.filehub.backup.R
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.layout.*
import androidx.compose.material3.*
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.work.WorkInfo
import com.filehub.backup.Backup
import com.filehub.backup.SyncProgress

// A short periodic trigger is not itself a transfer and must not hide the uploader.
internal fun activeBackup(infos: List<WorkInfo>): WorkInfo? = infos.firstOrNull {
    it.state == WorkInfo.State.RUNNING && it.tags.contains(Backup.WORK_TAG)
} ?: infos.firstOrNull {
    it.state == WorkInfo.State.ENQUEUED && it.tags.contains(Backup.WORK_TAG)
}

@Composable
internal fun SyncProgressBanner(info: WorkInfo, onClick: () -> Unit) {
    Surface(onClick = onClick, modifier = Modifier.fillMaxWidth().padding(horizontal = 18.dp, vertical = 6.dp),
        shape = Fh.CardShape, color = Fh.AccentSoft, border = BorderStroke(1.dp, Fh.Border)) {
        Column(Modifier.padding(12.dp)) { SyncProgressDetails(info, compact = true) }
    }
}

@Composable
internal fun SyncProgressDetails(info: WorkInfo, compact: Boolean = false) {
    val text = LocalAppResources.current
    if (info.state != WorkInfo.State.RUNNING) {
        Text(appString(R.string.backup_sync_queued), fontWeight = FontWeight.SemiBold, color = Fh.Text, fontSize = 13.sp)
        Text(appString(R.string.backup_sync_wait_reason), color = Fh.Text2, fontSize = 12.sp)
        return
    }
    val progress = SyncProgress.from(info.progress)
    val activeFiles = progress.activeFileLabels(text)
    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        Text(progress.activity(text), modifier = Modifier.weight(1f), color = Fh.Text, fontSize = 13.sp, fontWeight = FontWeight.SemiBold)
        if (progress.totalBytes > 0) Text("${progress.percent}%", color = Fh.AccentDeep, fontSize = 13.sp, fontWeight = FontWeight.Bold)
    }
    Spacer(Modifier.height(6.dp))
    if (progress.totalBytes > 0) {
        LinearProgressIndicator(progress = { progress.fraction }, modifier = Modifier.fillMaxWidth())
        Spacer(Modifier.height(5.dp))
        Text(appString(R.string.backup_sync_bytes_sent, progress.byteLabel(text)), color = Fh.Text2, fontSize = 12.sp)
        Text(progress.verifiedLabel(text), color = Fh.Text2, fontSize = 12.sp)
    } else LinearProgressIndicator(modifier = Modifier.fillMaxWidth())
    if (activeFiles.isNotEmpty()) {
        Spacer(Modifier.height(4.dp))
        Text(activeFiles.joinToString(if (compact) " · " else "\n"), color = Fh.Muted, fontSize = 11.sp,
            maxLines = if (compact) 1 else 3, overflow = TextOverflow.Ellipsis)
    }
}
