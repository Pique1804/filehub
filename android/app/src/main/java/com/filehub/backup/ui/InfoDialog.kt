package com.filehub.backup.ui

import android.content.Intent
import android.net.Uri
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.filehub.backup.Api
import com.filehub.backup.localizedSize
import com.filehub.backup.R
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import org.json.JSONObject
import java.util.Locale

internal fun fileDateTime(ts: Long, locale: Locale): String {
    if (ts <= 0) return "—"
    val date = java.time.Instant.ofEpochSecond(ts).atZone(java.time.ZoneId.systemDefault())
    return java.time.format.DateTimeFormatter
        .ofLocalizedDateTime(java.time.format.FormatStyle.MEDIUM, java.time.format.FormatStyle.SHORT)
        .withLocale(locale)
        .format(date)
}

@Composable
fun InfoDialog(source: String, path: String, onChanged: () -> Unit = {}, onClose: () -> Unit) {
    val ctx = LocalContext.current
    val resources = LocalAppResources.current
    var showVersions by remember {mutableStateOf(false)}
    if(showVersions)VersionsDialog(source,path,onClose={showVersions=false},onRestored={onChanged();onClose()})
    var obj by remember { mutableStateOf<JSONObject?>(null) }
    var loading by remember { mutableStateOf(true) }
    LaunchedEffect(source, path) { obj = withContext(Dispatchers.IO) { Api.info(source, path) }; loading = false }

    AlertDialog(
        onDismissRequest = onClose,
        shape = RoundedCornerShape(22.dp),
        containerColor = Fh.Card,
        title = { Text(appString(R.string.file_ui_properties), color = Fh.Text, fontSize = 16.5.sp, fontWeight = FontWeight.Bold, fontFamily = Golos) },
        text = {
            when {
                loading -> Box(Modifier.fillMaxWidth().padding(20.dp), contentAlignment = Alignment.Center) {
                    CircularProgressIndicator(color = Fh.Accent)
                }
                obj == null -> Text(appString(R.string.file_ui_metadata_failed), color = Fh.Muted, fontSize = 14.sp, fontFamily = Golos)
                else -> {
                    val o = obj!!
                    Column {
                        InfoRow(appString(R.string.file_ui_name), o.optString("name"), first = true)
                        if (o.optBoolean("isDir")) {
                            InfoRow(appString(R.string.file_ui_type), appString(R.string.file_ui_folder))
                        } else {
                            InfoRow(appString(R.string.file_ui_size), localizedSize(resources, o.optLong("size")))
                            InfoRow(appString(R.string.file_ui_type), o.optString("mime", "—"))
                            if (o.has("width")) InfoRow(appString(R.string.file_ui_resolution), appString(R.string.file_ui_dimensions, o.optInt("width"), o.optInt("height")))
                            if (o.has("taken")) InfoRow(appString(R.string.file_ui_taken_at), fileDateTime(o.optLong("taken"), resources.configuration.locales[0]))
                            if (o.has("camera")) InfoRow(appString(R.string.file_ui_camera), o.optString("camera"))
                        }
                        InfoRow(appString(R.string.file_ui_modified_at), fileDateTime(o.optLong("modTime"), resources.configuration.locales[0]))
                        if (o.has("lat")) InfoRow("GPS", String.format(resources.configuration.locales[0], "%.5f / %.5f", o.optDouble("lat"), o.optDouble("lon")))
                    }
                }
            }
        },
        confirmButton = {
            Row(verticalAlignment = Alignment.CenterVertically) {
                if (obj?.has("lat") == true) TextButton(onClick = {
                    val lat = obj!!.optDouble("lat"); val lon = obj!!.optDouble("lon")
                    runCatching { ctx.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse("geo:$lat,$lon?q=$lat,$lon"))) }
                }) { Text(appString(R.string.file_ui_view_map), color = Fh.AccentDeep, fontWeight = FontWeight.SemiBold, fontFamily = Golos) }
                if(obj?.optBoolean("isDir")==false)TextButton(onClick={showVersions=true}){Text(appString(R.string.file_ui_version_history),color=Fh.AccentDeep)}
                TextButton(onClick = onClose) {
                    Text(appString(R.string.action_close), color = Fh.Accent, fontWeight = FontWeight.Bold, fontFamily = Golos)
                }
            }
        }
    )
}

@Composable
private fun InfoRow(label: String, value: String, first: Boolean = false) {
    Column {
        if (!first) HorizontalDivider(color = Fh.Divider, thickness = 1.dp)
        Row(Modifier.fillMaxWidth().padding(vertical = 8.dp)) {
            Text(label, Modifier.width(108.dp), color = Fh.Muted, fontSize = 12.5.sp,
                fontWeight = FontWeight.SemiBold, fontFamily = Golos)
            Text(value, Modifier.weight(1f), color = Fh.Text, fontSize = 14.sp,
                fontWeight = FontWeight.Medium, fontFamily = Golos)
        }
    }
}
