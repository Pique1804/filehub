package com.filehub.backup.ui

import android.app.DownloadManager
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.webkit.MimeTypeMap
import android.widget.Toast
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Close
import androidx.compose.material.icons.outlined.DeleteOutline
import androidx.compose.material.icons.outlined.Description
import androidx.compose.material.icons.outlined.FolderOpen
import androidx.compose.material.icons.outlined.PlayCircleOutline
import androidx.compose.material.icons.rounded.ArrowBack
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import androidx.core.content.FileProvider
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.work.WorkInfo
import com.filehub.backup.Offline
import com.filehub.backup.OfflineDownload
import com.filehub.backup.R
import com.filehub.backup.appResources
import com.filehub.backup.localizedSize
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.io.File
import java.util.Locale

@Composable
fun OfflineScreen(onClose: () -> Unit) {
    val ctx = LocalContext.current
    val resources = LocalAppResources.current
    val account = OfflineDownload.accountTag(ctx)
    val work = remember(ctx, account) { OfflineDownload.observe(ctx) }
    val entries by work.collectAsStateWithLifecycle(initialValue = null)
    val currentEntries = entries.orEmpty().filter { account in it.tags }
    var revision by remember { mutableIntStateOf(0) }
    var opened by rememberSaveable(account) { mutableStateOf<ArrayList<String>?>(null) }
    val finished = currentEntries.filter { it.state.isFinished }.map { it.id }.toSet()
    var saved by remember(account) { mutableStateOf(emptyList<Pair<String, String>>()) }
    LaunchedEffect(account, finished, revision) {
        saved = withContext(Dispatchers.IO) { Offline.list(ctx) }
    }
    val pending = currentEntries.mapNotNull { info ->
        if (info.state == WorkInfo.State.SUCCEEDED || info.state == WorkInfo.State.CANCELLED) return@mapNotNull null
        val file = OfflineDownload.key(info) ?: return@mapNotNull null
        (file to info).takeUnless { file in saved }
    }.sortedBy { it.second.state.isFinished }.distinctBy { it.first }

    Dialog(onDismissRequest = onClose, properties = DialogProperties(usePlatformDefaultWidth = false)) {
        Surface(Modifier.fillMaxSize(), color = Fh.Bg) {
            Column(Modifier.fillMaxSize().safeDrawingPadding()) {
                Row(Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 8.dp),
                    verticalAlignment = Alignment.CenterVertically) {
                    IconButton(onClick = onClose) {
                        Icon(Icons.Rounded.ArrowBack, appString(R.string.action_close))
                    }
                    Text(appString(R.string.downloads_title), style = MaterialTheme.typography.headlineSmall,
                        modifier = Modifier.padding(start = 8.dp))
                }
                HorizontalDivider()
                LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(16.dp),
                    verticalArrangement = Arrangement.spacedBy(10.dp)) {
                    items(pending, key = { it.second.id }) { (file, info) ->
                        val (source, path) = file
                        val name = path.substringAfterLast('/')
                        val failed = info.state == WorkInfo.State.FAILED
                        val received = info.progress.getLong(OfflineDownload.Received, -1)
                        val total = info.progress.getLong(OfflineDownload.Total, -1)
                        OutlinedCard(Modifier.fillMaxWidth()) {
                            Column(Modifier.padding(start = 14.dp, end = 8.dp, top = 5.dp, bottom = 12.dp)) {
                                Row(verticalAlignment = Alignment.CenterVertically) {
                                    Text(name, Modifier.weight(1f), maxLines = 2, overflow = TextOverflow.Ellipsis,
                                        style = MaterialTheme.typography.titleSmall)
                                    if (failed) {
                                        TextButton(onClick = { OfflineDownload.enqueue(ctx, source, path) }) {
                                            Text(appString(R.string.video_retry))
                                        }
                                    } else IconButton(onClick = { OfflineDownload.cancel(ctx, info.id) }) {
                                        Icon(Icons.Outlined.Close, appString(R.string.downloads_cancel))
                                    }
                                }
                                if (failed) {
                                    val status = info.outputData.getInt(OfflineDownload.HttpStatus, 0)
                                    Text(if (status > 0) appString(R.string.transfer_ui_download_http_error, status)
                                        else appString(R.string.file_ui_offline_save_failed),
                                        color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall)
                                } else {
                                    val detail = when {
                                        info.state != WorkInfo.State.RUNNING && info.runAttemptCount > 0 -> appString(R.string.downloads_retry_scheduled)
                                        info.state != WorkInfo.State.RUNNING -> appString(R.string.downloads_queued)
                                        received < 0L -> appString(R.string.downloads_queued)
                                        total > 0L -> appString(R.string.downloads_progress_bytes,
                                            localizedSize(resources, received), localizedSize(resources, total))
                                        received == 0L -> appString(R.string.downloads_in_progress)
                                        else -> appString(R.string.downloads_received_bytes, localizedSize(resources, received))
                                    }
                                    Text(detail, color = Fh.Muted, style = MaterialTheme.typography.bodySmall)
                                    if (info.state == WorkInfo.State.RUNNING) {
                                        Spacer(Modifier.height(8.dp))
                                        if (total > 0L && received >= 0L) {
                                            val fraction = (received.toDouble() / total).toFloat().coerceIn(0f, 1f)
                                            LinearProgressIndicator(progress = { fraction }, modifier = Modifier.fillMaxWidth())
                                        } else LinearProgressIndicator(Modifier.fillMaxWidth())
                                    }
                                }
                            }
                        }
                    }
                    if (saved.isNotEmpty()) item {
                        Text(appString(R.string.downloads_saved), style = MaterialTheme.typography.titleMedium,
                            modifier = Modifier.padding(top = 6.dp))
                    }
                    items(saved, key = { it }) { (source, path) ->
                        val name = path.substringAfterLast('/')
                        val file = Offline.localFile(ctx, source, path)
                        val mime = offlineMime(name)
                        val video = mime.startsWith("video/")
                        OutlinedCard(Modifier.fillMaxWidth()) {
                            Row(Modifier.fillMaxWidth().padding(start = 14.dp, end = 4.dp),
                                verticalAlignment = Alignment.CenterVertically) {
                                Row(Modifier.weight(1f).clickable {
                                    if (file == null) {
                                        Toast.makeText(ctx, resources.getString(R.string.file_ui_offline_missing), Toast.LENGTH_SHORT).show()
                                        revision++
                                    } else {
                                        if (video) opened = arrayListOf(source, path)
                                        else openLocal(ctx, offlineUri(ctx, file, name), mime)
                                    }
                                }.padding(vertical = 12.dp), verticalAlignment = Alignment.CenterVertically) {
                                    Icon(if (video) Icons.Outlined.PlayCircleOutline else Icons.Outlined.Description,
                                        null, tint = Fh.AccentDeep)
                                    Column(Modifier.padding(horizontal = 12.dp)) {
                                        Text(name, maxLines = 2, overflow = TextOverflow.Ellipsis,
                                            style = MaterialTheme.typography.titleSmall)
                                        Text(localizedSize(resources, file?.length() ?: 0), color = Fh.Muted,
                                            style = MaterialTheme.typography.bodySmall)
                                    }
                                }
                                IconButton(onClick = {
                                    if (Offline.unpin(ctx, source, path)) revision++
                                    else Toast.makeText(ctx, resources.getString(R.string.downloads_remove_failed), Toast.LENGTH_SHORT).show()
                                }) {
                                    Icon(Icons.Outlined.DeleteOutline, appString(R.string.file_ui_offline_remove))
                                }
                            }
                        }
                    }
                    if (entries != null && saved.isEmpty() && pending.isEmpty()) item {
                        Text(appString(R.string.downloads_empty), color = Fh.Muted,
                            modifier = Modifier.padding(vertical = 16.dp))
                    }
                    item {
                        TextButton(onClick = {
                            runCatching { ctx.startActivity(Intent(DownloadManager.ACTION_VIEW_DOWNLOADS)) }
                                .onFailure { Toast.makeText(ctx, resources.getString(R.string.file_ui_offline_no_app), Toast.LENGTH_SHORT).show() }
                        }) {
                            Icon(Icons.Outlined.FolderOpen, null, Modifier.size(18.dp))
                            Spacer(Modifier.width(8.dp))
                            Text(appString(R.string.downloads_system))
                        }
                    }
                }
            }
        }
    }

    opened?.let { (source, path) ->
        val name = path.substringAfterLast('/')
        val file = remember(source, path) { Offline.localFile(ctx, source, path) }
        if (file != null) {
            val uri = remember(file) { offlineUri(ctx, file, name) }
            VideoScreen(source, path, name, localUri = uri, onClose = { opened = null })
        }
    }
}

internal fun offlineUri(ctx: Context, file: File, name: String): Uri =
    FileProvider.getUriForFile(ctx, "${ctx.packageName}.fileprovider", file, name)

internal fun offlineMime(name: String): String {
    val ext = name.substringAfterLast('.', "").lowercase(Locale.ROOT)
    return MimeTypeMap.getSingleton().getMimeTypeFromExtension(ext)
        ?: if (ext in setOf("avi", "mkv", "mpg", "mpeg", "m4v", "ts", "wmv")) "video/*" else "*/*"
}

internal fun openLocal(ctx: Context, uri: Uri, mime: String) {
    val intent = Intent(Intent.ACTION_VIEW).apply {
        setDataAndType(uri, mime)
        addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
    }
    runCatching { ctx.startActivity(intent) }.onFailure {
        Toast.makeText(ctx, ctx.appResources().getString(R.string.file_ui_offline_no_app), Toast.LENGTH_SHORT).show()
    }
}
