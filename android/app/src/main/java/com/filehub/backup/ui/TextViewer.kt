package com.filehub.backup.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Download
import androidx.compose.material.icons.rounded.Close
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import com.filehub.backup.Api
import com.filehub.backup.R
import com.filehub.backup.localizedSize
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

/** Full-screen viewer for plain-text / code / csv / log files (the ones that
 *  weren't openable before). Monospace, selectable, horizontally scrollable. */
@Composable
fun TextViewer(source: String, path: String, name: String, onDownload: () -> Unit, onClose: () -> Unit) {
    var text by remember(source, path) { mutableStateOf<String?>(null) }
    var loading by remember(source, path) { mutableStateOf(true) }
    var truncated by remember(source, path) { mutableStateOf(false) }

    LaunchedEffect(source, path) {
        loading = true
        val cap = 1 shl 20
        val t = withContext(Dispatchers.IO) { Api.fetchText(source, path, cap) }
        truncated = (t != null && t.toByteArray(Charsets.UTF_8).size >= cap)
        text = t
        loading = false
    }

    Dialog(onDismissRequest = onClose, properties = DialogProperties(usePlatformDefaultWidth = false)) {
        Surface(Modifier.fillMaxSize(), color = Fh.Bg) {
            Column(Modifier.fillMaxSize()) {
                Row(
                    Modifier.fillMaxWidth().statusBarsPadding()
                        .padding(horizontal = 14.dp, vertical = 10.dp),
                    verticalAlignment = Alignment.CenterVertically
                ) {
                    FhRoundButton(onClick = onClose) {
                        Icon(Icons.Rounded.Close, appString(R.string.action_close), tint = Fh.Text2, modifier = Modifier.size(18.dp))
                    }
                    Spacer(Modifier.width(10.dp))
                    Text(name, Modifier.weight(1f), maxLines = 1, overflow = TextOverflow.Ellipsis,
                        color = Fh.Text, fontSize = 15.sp, fontWeight = FontWeight.SemiBold, fontFamily = Golos)
                    Spacer(Modifier.width(10.dp))
                    FhRoundButton(onClick = onDownload) {
                        Icon(Icons.Outlined.Download, appString(R.string.file_ui_download), tint = Fh.Text2, modifier = Modifier.size(18.dp))
                    }
                }
                if (loading) {
                    Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                        CircularProgressIndicator(color = Fh.Accent)
                    }
                } else {
                    SelectionContainer(Modifier.weight(1f)) {
                        Text(
                            text ?: appString(R.string.file_ui_text_read_failed),
                            modifier = Modifier.fillMaxSize()
                                .verticalScroll(rememberScrollState())
                                .horizontalScroll(rememberScrollState())
                                .padding(14.dp),
                            color = Fh.Text,
                            fontFamily = FontFamily.Monospace, fontSize = 13.sp, lineHeight = 18.sp
                        )
                    }
                    if (truncated) Text(appString(R.string.file_ui_text_truncated, localizedSize(LocalAppResources.current, 1L shl 20)),
                        Modifier.fillMaxWidth().background(Fh.Card).padding(8.dp),
                        fontSize = 11.sp, color = Fh.Muted, fontFamily = Golos)
                }
            }
        }
    }
}
