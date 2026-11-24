package com.filehub.backup.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.InsertDriveFile
import androidx.compose.material.icons.rounded.Close
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import com.filehub.backup.Api
import com.filehub.backup.RecentFile
import com.filehub.backup.localizedSize
import com.filehub.backup.R
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

/** Newest files across all disks. Tap → jump to the folder that contains it. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun RecentsScreen(onOpen: (source: String, parentPath: String) -> Unit, onClose: () -> Unit) {
    var items by remember { mutableStateOf<List<RecentFile>>(emptyList()) }
    var loading by remember { mutableStateOf(true) }
    LaunchedEffect(Unit) { items = withContext(Dispatchers.IO) { Api.recents() }; loading = false }

    Dialog(onDismissRequest = onClose, properties = DialogProperties(usePlatformDefaultWidth = false)) {
        Surface(Modifier.fillMaxSize(), color = MaterialTheme.colorScheme.background) {
            Column(Modifier.fillMaxSize()) {
                TopAppBar(
                    title = { Text(appString(R.string.file_ui_recent)) },
                    navigationIcon = { IconButton(onClick = onClose) { Icon(Icons.Rounded.Close, appString(R.string.action_close)) } },
                    colors = TopAppBarDefaults.topAppBarColors(containerColor = MaterialTheme.colorScheme.surface)
                )
                if (loading) LinearProgressIndicator(Modifier.fillMaxWidth())
                if (!loading && items.isEmpty()) {
                    Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                        Text(appString(R.string.file_ui_recent_empty), color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                } else LazyColumn(Modifier.fillMaxSize()) {
                    items(items, key = { it.source + "|" + it.path }) { r ->
                        ListItem(
                            modifier = Modifier.clickable { onOpen(r.source, r.path.substringBeforeLast('/', "")) },
                            headlineContent = { Text(r.name, maxLines = 1, overflow = TextOverflow.Ellipsis, fontSize = 15.sp) },
                            supportingContent = { Text("${r.source} · ${localizedSize(LocalAppResources.current, r.size)}", fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant) },
                            leadingContent = { Icon(Icons.AutoMirrored.Outlined.InsertDriveFile, null, tint = MaterialTheme.colorScheme.onSurfaceVariant) }
                        )
                        HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
                    }
                }
            }
        }
    }
}
