package com.filehub.backup.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.ArrowBack
import androidx.compose.material.icons.outlined.Close
import androidx.compose.material.icons.outlined.Folder
import androidx.compose.material.icons.outlined.Storage
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import coil.compose.AsyncImage
import coil.request.CachePolicy
import coil.request.ImageRequest
import com.filehub.backup.Api
import com.filehub.backup.Entry
import com.filehub.backup.Source
import com.filehub.backup.R
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

@Composable
fun TextPrompt(title: String, initial: String, onResult: (String?) -> Unit) {
    var text by remember { mutableStateOf(initial) }
    AlertDialog(
        onDismissRequest = { onResult(null) },
        shape = RoundedCornerShape(16.dp),
        containerColor = Fh.Card,
        title = { Text(title, color = Fh.Text, fontSize = 16.5.sp, fontWeight = FontWeight.Bold, fontFamily = Golos) },
        text = {
            OutlinedTextField(
                text, { text = it }, singleLine = true, modifier = Modifier.fillMaxWidth(),
                shape = RoundedCornerShape(12.dp),
                colors = OutlinedTextFieldDefaults.colors(
                    focusedBorderColor = Fh.Accent,
                    unfocusedBorderColor = Fh.Border,
                    cursorColor = Fh.Accent,
                    focusedTextColor = Fh.Text,
                    unfocusedTextColor = Fh.Text,
                )
            )
        },
        confirmButton = {
            Button(
                onClick = { onResult(text.trim().ifBlank { null }) },
                shape = RoundedCornerShape(12.dp),
                colors = ButtonDefaults.buttonColors(containerColor = Fh.Accent, contentColor = Fh.OnAccent)
            ) { Text("OK", fontWeight = FontWeight.Bold, fontFamily = Golos) }
        },
        dismissButton = {
            TextButton(onClick = { onResult(null) }) {
                Text(appString(R.string.action_cancel), color = Fh.Muted, fontWeight = FontWeight.SemiBold, fontFamily = Golos)
            }
        }
    )
}

/** Row of the folder picker: 42dp AccentSoft icon square + name, prototype-style. */
@Composable
private fun PickerRow(icon: ImageVector, name: String, first: Boolean, onClick: () -> Unit) {
    Column {
        if (!first) HorizontalDivider(color = Fh.Divider, thickness = 1.dp)
        Row(
            Modifier.fillMaxWidth().clickable(onClick = onClick).padding(horizontal = 14.dp, vertical = 12.dp),
            verticalAlignment = Alignment.CenterVertically
        ) {
            Box(
                Modifier.size(42.dp).background(Fh.AccentSoft, RoundedCornerShape(12.dp)),
                contentAlignment = Alignment.Center
            ) { Icon(icon, null, tint = Fh.AccentDeep, modifier = Modifier.size(20.dp)) }
            Spacer(Modifier.width(12.dp))
            Text(name, maxLines = 1, overflow = TextOverflow.Ellipsis, color = Fh.Text,
                fontSize = 14.sp, fontWeight = FontWeight.SemiBold, fontFamily = Golos)
        }
    }
}

/** Browse the server (disks → folders) and pick a destination folder. */
@Composable
fun FolderPickerDialog(onPick: (source: String, path: String) -> Unit, onDismiss: () -> Unit) {
    val ctx = LocalContext.current
    var source by remember { mutableStateOf<String?>(null) }
    var path by remember { mutableStateOf("") }
    var sources by remember { mutableStateOf<List<Source>>(emptyList()) }
    var dirs by remember { mutableStateOf<List<Entry>>(emptyList()) }
    LaunchedEffect(Unit) { sources = withContext(Dispatchers.IO) { Api.sources() } }
    LaunchedEffect(source, path) { if (source != null) dirs = withContext(Dispatchers.IO) { Api.list(source!!, path).filter { it.isDir } } }

    Dialog(onDismissRequest = onDismiss) {
        Surface(shape = RoundedCornerShape(16.dp), color = Fh.Card) {
            Column(Modifier.padding(16.dp)) {
                Text(appString(R.string.folder_picker_title), color = Fh.Text, fontSize = 16.5.sp,
                    fontWeight = FontWeight.Bold, fontFamily = Golos)
                Text(
                    if (source == null) appString(R.string.folder_picker_choose_storage) else source + (if (path.isNotEmpty()) " / $path" else ""),
                    color = Fh.Muted, fontSize = 12.5.sp, fontFamily = Golos,
                    maxLines = 1, overflow = TextOverflow.Ellipsis
                )
                Spacer(Modifier.height(10.dp))
                LazyColumn(Modifier.heightIn(max = 340.dp)) {
                    if (source == null) {
                        items(sources) { s ->
                            PickerRow(Icons.Outlined.Storage, s.name, first = s === sources.firstOrNull()) {
                                source = s.name; path = ""
                            }
                        }
                    } else {
                        item {
                            PickerRow(Icons.AutoMirrored.Outlined.ArrowBack, "..", first = true) {
                                if (path.isEmpty()) source = null else path = path.substringBeforeLast('/', "")
                            }
                        }
                        items(dirs) { e ->
                            PickerRow(Icons.Outlined.Folder, e.name, first = false) {
                                path = if (path.isEmpty()) e.name else "$path/${e.name}"
                            }
                        }
                    }
                }
                Spacer(Modifier.height(12.dp))
                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End, verticalAlignment = Alignment.CenterVertically) {
                    TextButton(onClick = onDismiss) {
                        Text(appString(R.string.action_cancel), color = Fh.Muted, fontWeight = FontWeight.SemiBold, fontFamily = Golos)
                    }
                    if (source != null) {
                        Spacer(Modifier.width(8.dp))
                        Button(
                            onClick = { onPick(source!!, path) },
                            shape = RoundedCornerShape(14.dp),
                            colors = ButtonDefaults.buttonColors(containerColor = Fh.Accent, contentColor = Fh.OnAccent)
                        ) { Text(appString(R.string.folder_picker_select), fontWeight = FontWeight.Bold, fontFamily = Golos) }
                    }
                }
            }
        }
    }
}

@Composable
fun ImageViewer(url: String, name: String, onClose: () -> Unit) {
    val context = LocalContext.current
    val original = remember(context, url) {
        ImageRequest.Builder(context).data(url)
            .diskCachePolicy(CachePolicy.DISABLED)
            .memoryCachePolicy(CachePolicy.DISABLED)
            .build()
    }
    Dialog(onDismissRequest = onClose, properties = DialogProperties(usePlatformDefaultWidth = false)) {
        Box(Modifier.fillMaxSize().background(Fh.Dark)) {
            AsyncImage(original, name, Modifier.fillMaxSize(), contentScale = ContentScale.Fit)
            Row(
                Modifier.fillMaxWidth().statusBarsPadding().padding(horizontal = 14.dp, vertical = 10.dp)
                    .align(Alignment.TopStart),
                verticalAlignment = Alignment.CenterVertically
            ) {
                Text(name, Modifier.weight(1f), color = Color.White, fontSize = 15.sp,
                    fontWeight = FontWeight.SemiBold, fontFamily = Golos,
                    maxLines = 1, overflow = TextOverflow.Ellipsis)
                Spacer(Modifier.width(10.dp))
                Surface(onClick = onClose, shape = CircleShape, color = Color(0x1AFFFFFF), modifier = Modifier.size(48.dp)) {
                    Box(contentAlignment = Alignment.Center) {
                        Icon(Icons.Outlined.Close, appString(R.string.action_close), tint = Color.White, modifier = Modifier.size(18.dp))
                    }
                }
            }
        }
    }
}

/** Shared empty and unavailable states, with a real retry action when provided. */
@Composable
fun FhEmptyState(icon: ImageVector, title: String, description: String, modifier: Modifier = Modifier,
                 actionLabel: String? = null, onAction: (() -> Unit)? = null) {
    Box(modifier.fillMaxWidth().padding(24.dp), contentAlignment = Alignment.Center) {
        Column(Modifier.widthIn(max = 340.dp), horizontalAlignment = Alignment.CenterHorizontally) {
            Box(Modifier.size(64.dp).background(Fh.AccentSoft, Fh.CardShape), contentAlignment = Alignment.Center) {
                Icon(icon, null, Modifier.size(28.dp), tint = Fh.AccentDeep)
            }
            Spacer(Modifier.height(18.dp))
            Text(title, style = MaterialTheme.typography.titleLarge, color = Fh.Text,
                textAlign = androidx.compose.ui.text.style.TextAlign.Center)
            Spacer(Modifier.height(8.dp))
            Text(description, style = MaterialTheme.typography.bodyMedium, color = Fh.Muted,
                textAlign = androidx.compose.ui.text.style.TextAlign.Center)
            if (actionLabel != null && onAction != null) {
                Spacer(Modifier.height(16.dp))
                TextButton(onClick = onAction, modifier = Modifier.heightIn(min = 48.dp)) { Text(actionLabel) }
            }
        }
    }
}
