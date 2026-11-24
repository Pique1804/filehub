package com.filehub.backup.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.DeleteForever
import androidx.compose.material.icons.outlined.Folder
import androidx.compose.material.icons.outlined.Restore
import androidx.compose.material.icons.rounded.Close
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import com.filehub.backup.Api
import com.filehub.backup.TrashItem
import com.filehub.backup.R
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

@Composable
private fun ago(ts: Long): String {
    val sec = (System.currentTimeMillis() / 1000 - ts).coerceAtLeast(0)
    return when {
        sec < 3600 -> appQuantity(R.plurals.file_ui_minutes_ago, (sec / 60).coerceAtLeast(1).toInt())
        sec < 86400 -> appQuantity(R.plurals.file_ui_hours_ago, (sec / 3600).toInt())
        else -> appQuantity(R.plurals.file_ui_days_ago, (sec / 86400).coerceAtMost(Int.MAX_VALUE.toLong()).toInt())
    }
}

/** Tinted extension square per the design handoff (42dp, radius 12). */
@Composable
private fun TrashBadge(name: String, isDir: Boolean) {
    if (isDir) {
        Box(Modifier.size(42.dp).background(Fh.AccentSoft, RoundedCornerShape(12.dp)), contentAlignment = Alignment.Center) {
            Icon(Icons.Outlined.Folder, null, Modifier.size(20.dp), tint = Fh.AccentDeep)
        }
    } else {
        val ext = name.substringAfterLast('.', "").take(4).uppercase()
        val (bg, fg) = when (ext) {
            "JPG", "JPEG" -> Color(0xFFDCEDFF) to Color(0xFF005ECB)
            "PNG" -> Color(0xFFD7F0FA) to Color(0xFF00688F)
            "MP4", "MOV" -> Color(0xFFF3E5FB) to Color(0xFF8324A8)
            "ZIP", "RAR" -> Color(0xFFF6ECD4) to Color(0xFF8A6A00)
            "PDF" -> Color(0xFFFBE4DE) to Color(0xFFB3341F)
            "TXT", "DOC", "DOCX" -> Color(0xFFD8F1EC) to Color(0xFF00745F)
            else -> Color(0xFFDCEDFF) to Color(0xFF005ECB)
        }
        Box(Modifier.size(42.dp).background(bg, RoundedCornerShape(12.dp)), contentAlignment = Alignment.Center) {
            Text(
                if (ext.isEmpty()) appString(R.string.file_ui_extension_missing) else ext,
                color = fg, fontFamily = Golos, fontSize = 10.5.sp, fontWeight = FontWeight.ExtraBold, letterSpacing = 0.4.sp
            )
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun TrashScreen(source: String, onClose: () -> Unit, onChanged: () -> Unit) {
    val scope = rememberCoroutineScope()
    var items by remember { mutableStateOf<List<TrashItem>>(emptyList()) }
    var loading by remember { mutableStateOf(true) }
    var confirmEmpty by remember { mutableStateOf(false) }
    var busy by remember { mutableStateOf(false) }

    fun reload() { scope.launch { loading = true; items = withContext(Dispatchers.IO) { Api.trashList(source) }; loading = false } }
    LaunchedEffect(source) { reload() }

    Dialog(onDismissRequest = onClose, properties = DialogProperties(usePlatformDefaultWidth = false)) {
        Surface(Modifier.fillMaxSize(), color = Fh.Bg) {
            Column(Modifier.fillMaxSize()) {
                Row(
                    Modifier.statusBarsPadding().fillMaxWidth().padding(horizontal = 18.dp, vertical = 12.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    FhRoundButton(onClick = onClose) { Icon(Icons.Rounded.Close, appString(R.string.action_close), Modifier.size(18.dp), tint = Fh.Text2) }
                    Column(Modifier.weight(1f).padding(horizontal = 12.dp)) {
                        Text(appString(R.string.file_ui_trash), fontFamily = Golos, fontSize = 22.sp, fontWeight = FontWeight.ExtraBold,
                            letterSpacing = (-0.5).sp, color = Fh.Text)
                        Text(source, fontFamily = Golos, fontSize = 12.5.sp, color = Fh.Muted)
                    }
                    if (items.isNotEmpty()) Surface(
                        onClick = { confirmEmpty = true },
                        shape = RoundedCornerShape(12.dp), color = Fh.AccentSoft,
                    ) {
                        Text(appString(R.string.file_ui_trash_empty_action), Modifier.padding(horizontal = 14.dp, vertical = 9.dp),
                            fontFamily = Golos, fontSize = 13.sp, fontWeight = FontWeight.Bold, color = Fh.AccentDeep)
                    }
                }
                if (loading || busy) LinearProgressIndicator(Modifier.fillMaxWidth(), color = Fh.Accent, trackColor = Fh.AccentSoft)
                if (!loading && items.isEmpty()) {
                    Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                        Text(appString(R.string.file_ui_trash_empty), fontFamily = Golos, fontSize = 13.5.sp, color = Fh.Muted)
                    }
                } else LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(start = 18.dp, end = 18.dp, bottom = 24.dp)) {
                    item {
                        FhSectionLabel(appString(R.string.file_ui_trash_retention),
                            Modifier.padding(start = 4.dp, bottom = 10.dp))
                    }
                    itemsIndexed(items, key = { _, t -> t.id }) { i, t ->
                        // Lazy rows that together read as one white rounded-20 card:
                        // first/last segments carry the rounded corners, dividers separate rows.
                        val shape = when {
                            items.size == 1 -> Fh.CardShape
                            i == 0 -> RoundedCornerShape(topStart = 20.dp, topEnd = 20.dp)
                            i == items.lastIndex -> RoundedCornerShape(bottomStart = 20.dp, bottomEnd = 20.dp)
                            else -> RoundedCornerShape(0.dp)
                        }
                        Column(Modifier.fillMaxWidth().background(Fh.Card, shape)) {
                            if (i > 0) HorizontalDivider(color = Fh.Divider)
                            Row(Modifier.fillMaxWidth().padding(horizontal = 14.dp, vertical = 12.dp),
                                verticalAlignment = Alignment.CenterVertically) {
                                TrashBadge(t.name, t.isDir)
                                Column(Modifier.weight(1f).padding(horizontal = 12.dp)) {
                                    Text(t.name, maxLines = 1, overflow = TextOverflow.Ellipsis,
                                        fontFamily = Golos, fontSize = 14.sp, fontWeight = FontWeight.SemiBold, color = Fh.Text)
                                    val from = t.origPath.substringBeforeLast('/', "")
                                    Text(if (from.isBlank()) ago(t.deletedAt) else appString(R.string.file_ui_trash_origin, from, ago(t.deletedAt)),
                                        maxLines = 1, overflow = TextOverflow.Ellipsis,
                                        fontFamily = Golos, fontSize = 12.sp, color = Fh.Muted)
                                }
                                IconButton(onClick = {
                                    busy = true
                                    scope.launch { withContext(Dispatchers.IO) { Api.trashRestore(source, t.id) }; busy = false; reload(); onChanged() }
                                }) { Icon(Icons.Outlined.Restore, appString(R.string.file_ui_trash_restore), Modifier.size(21.dp), tint = Fh.AccentDeep) }
                                IconButton(onClick = {
                                    busy = true
                                    scope.launch { withContext(Dispatchers.IO) { Api.trashDelete(source, t.id) }; busy = false; reload() }
                                }) { Icon(Icons.Outlined.DeleteForever, appString(R.string.file_ui_trash_delete_permanently), Modifier.size(21.dp), tint = MaterialTheme.colorScheme.error) }
                            }
                        }
                    }
                }
            }
        }
    }

    if (confirmEmpty) AlertDialog(
        onDismissRequest = { confirmEmpty = false },
        shape = RoundedCornerShape(22.dp), containerColor = Fh.Card, titleContentColor = Fh.Text, textContentColor = Fh.Text2,
        title = { Text(appString(R.string.file_ui_trash_empty_title), fontFamily = Golos, fontWeight = FontWeight.Bold) },
        text = { Text(appString(R.string.file_ui_trash_empty_warning), fontFamily = Golos) },
        confirmButton = {
            TextButton(onClick = {
                confirmEmpty = false; busy = true
                scope.launch { withContext(Dispatchers.IO) { Api.trashEmpty(source) }; busy = false; reload() }
            }) { Text(appString(R.string.file_ui_trash_empty_action), fontFamily = Golos, fontWeight = FontWeight.Bold, color = MaterialTheme.colorScheme.error) }
        },
        dismissButton = { TextButton(onClick = { confirmEmpty = false }) { Text(appString(R.string.action_cancel), fontFamily = Golos, color = Fh.Muted) } }
    )
}
