package com.filehub.backup.ui

import android.content.Context
import android.net.Uri
import android.provider.OpenableColumns
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Folder
import androidx.compose.material.icons.rounded.Close
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.filehub.backup.Api
import com.filehub.backup.Prefs
import com.filehub.backup.ManualUploads
import com.filehub.backup.R
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

private fun shareDisplayName(ctx: Context, uri: Uri): String {
    var name = "file_${System.currentTimeMillis()}"
    if (!ManualUploads.canReadSharedUri(ctx, uri)) return name
    try {
        ctx.contentResolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)?.use { c ->
            if (c.moveToFirst()) {
                val i = c.getColumnIndex(OpenableColumns.DISPLAY_NAME)
                if (i >= 0) c.getString(i)?.let { name = it }
            }
        }
    } catch (_: Exception) {}
    return name
}

/** 42dp rounded-12 uppercase file-extension badge, tinted by type (prototype «Недавние» rows). */
@Composable
private fun ShareExtBadge(name: String) {
    val ext = name.substringAfterLast('.', "").uppercase().take(4).ifBlank { "FILE" }
    val (bg, fg) = when (ext) {
        "JPG", "JPEG" -> Color(0xFFDCEDFF) to Color(0xFF005ECB)
        "PNG" -> Color(0xFFD7F0FA) to Color(0xFF00688F)
        "MP4", "MOV" -> Color(0xFFF3E5FB) to Color(0xFF8324A8)
        "ZIP", "RAR" -> Color(0xFFF6ECD4) to Color(0xFF8A6A00)
        "PDF" -> Color(0xFFFBE4DE) to Color(0xFFB3341F)
        "TXT", "DOC", "DOCX" -> Color(0xFFD8F1EC) to Color(0xFF00745F)
        else -> Color(0xFFDCEDFF) to Color(0xFF005ECB)
    }
    Box(
        Modifier.size(42.dp).background(bg, RoundedCornerShape(12.dp)),
        contentAlignment = Alignment.Center
    ) {
        Text(ext, color = fg, fontSize = 10.5.sp, fontWeight = FontWeight.ExtraBold,
            fontFamily = Golos, letterSpacing = 0.4.sp)
    }
}

/** Shown when another app shares files INTO filehub (ACTION_SEND). Pick a folder, upload. */
@Composable
fun ShareReceiveScreen(uris: List<Uri>, prefs: Prefs, onDone: () -> Unit) {
    val ctx = LocalContext.current
    val scope = rememberCoroutineScope()
    val text = LocalAppResources.current
    var source by remember { mutableStateOf(prefs.source) }
    var folder by remember { mutableStateOf(prefs.folder) }
    var pick by remember { mutableStateOf(false) }
    var uploading by remember { mutableStateOf(false) }
    var done by remember { mutableStateOf(0) }
    var result by remember { mutableStateOf<String?>(null) }
    var names by remember(uris) { mutableStateOf(emptyList<String>()) }
    LaunchedEffect(uris) {
        names = withContext(Dispatchers.IO) { uris.map { shareDisplayName(ctx, it) } }
    }

    Column(
        Modifier.fillMaxSize().background(Fh.Bg).statusBarsPadding()
            .navigationBarsPadding().padding(horizontal = 18.dp)
    ) {
        // Header: back/cancel + big title
        Row(Modifier.fillMaxWidth().padding(top = 14.dp), verticalAlignment = Alignment.CenterVertically) {
            Column(Modifier.weight(1f)) {
                Text(appString(R.string.share_title), color = Fh.Text, fontSize = 26.sp,
                    fontWeight = FontWeight.ExtraBold, fontFamily = Golos, letterSpacing = (-0.5).sp)
                Text(appQuantity(R.plurals.share_file_count, uris.size), color = Fh.Muted, fontSize = 12.5.sp, fontFamily = Golos)
            }
            FhRoundButton(onClick = onDone) {
                Icon(Icons.Rounded.Close, appString(R.string.action_cancel), tint = Fh.Text2, modifier = Modifier.size(18.dp))
            }
        }

        Spacer(Modifier.height(16.dp))
        FhSectionLabel(appString(R.string.backup_destination))
        Spacer(Modifier.height(8.dp))
        Row(
            Modifier.fillMaxWidth().background(Fh.BgAlt, RoundedCornerShape(14.dp))
                .padding(horizontal = 14.dp, vertical = 12.dp),
            verticalAlignment = Alignment.CenterVertically
        ) {
            Icon(Icons.Outlined.Folder, null, tint = Fh.AccentDeep, modifier = Modifier.size(19.dp))
            Spacer(Modifier.width(10.dp))
            Text("$source / $folder", Modifier.weight(1f), maxLines = 1, overflow = TextOverflow.Ellipsis,
                color = Fh.Text, fontSize = 13.5.sp, fontWeight = FontWeight.SemiBold, fontFamily = Golos)
            TextButton(onClick = { pick = true }, enabled = !uploading) {
                Text(appString(R.string.action_change), color = if (uploading) Fh.Faint else Fh.AccentDeep,
                    fontSize = 13.sp, fontWeight = FontWeight.SemiBold, fontFamily = Golos)
            }
        }

        Spacer(Modifier.height(16.dp))
        FhSectionLabel(appString(R.string.nav_files))
        Spacer(Modifier.height(8.dp))
        FhCard(Modifier.weight(1f).fillMaxWidth()) {
            Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState())) {
                names.forEachIndexed { i, n ->
                    if (i > 0) HorizontalDivider(color = Fh.Divider, thickness = 1.dp)
                    Row(
                        Modifier.fillMaxWidth().padding(horizontal = 14.dp, vertical = 12.dp),
                        verticalAlignment = Alignment.CenterVertically
                    ) {
                        ShareExtBadge(n)
                        Spacer(Modifier.width(12.dp))
                        Text(n, maxLines = 1, overflow = TextOverflow.Ellipsis, color = Fh.Text,
                            fontSize = 14.sp, fontWeight = FontWeight.SemiBold, fontFamily = Golos)
                    }
                }
            }
        }

        Spacer(Modifier.height(12.dp))
        if (uploading) {
            LinearProgressIndicator(
                progress = { if (uris.isEmpty()) 0f else done.toFloat() / uris.size },
                modifier = Modifier.fillMaxWidth(), color = Fh.Accent, trackColor = Fh.Track
            )
            Spacer(Modifier.height(10.dp))
        }
        result?.let {
            Text(it, color = Fh.AccentDeep, fontSize = 13.sp, fontWeight = FontWeight.SemiBold, fontFamily = Golos)
            Spacer(Modifier.height(10.dp))
        }
        Button(
            enabled = !uploading && names.size == uris.size,
            onClick = {
                if (result != null) { onDone(); return@Button }
                val attachments = uris.zip(names)
                val destinationSource = source
                val destinationFolder = folder
                uploading = true; done = 0
                scope.launch {
                    try {
                        withContext(Dispatchers.IO) {
                            ManualUploads(ctx).use { queue ->
                                for ((uri, name) in attachments) {
                                    queue.stageShared(ctx, uri, name, destinationSource, destinationFolder)
                                    done++
                                }
                            }
                            ManualUploads.schedule(ctx)
                        }
                        result=text.getString(R.string.share_queued, done)
                    }catch(e: Exception){
                        if(done>0)ManualUploads.schedule(ctx)
                        result=text.getString(R.string.share_stage_error, done, e.message ?: text.getString(R.string.share_read_error))
                    }finally{uploading=false}
                }
            },
            modifier = Modifier.fillMaxWidth().padding(bottom = 14.dp),
            shape = RoundedCornerShape(14.dp),
            colors = ButtonDefaults.buttonColors(containerColor = Fh.Accent, contentColor = Fh.OnAccent),
            contentPadding = PaddingValues(vertical = 14.dp)
        ) {
            Text(
                when {
                    uploading -> appString(R.string.share_preparing, done, uris.size)
                    result != null -> appString(R.string.action_done)
                    else -> appString(R.string.action_upload)
                },
                fontSize = 15.sp, fontWeight = FontWeight.Bold, fontFamily = Golos
            )
        }
    }

    if (pick) FolderPickerDialog(onPick = { s, p -> source = s; folder = p; pick = false }, onDismiss = { pick = false })
}
