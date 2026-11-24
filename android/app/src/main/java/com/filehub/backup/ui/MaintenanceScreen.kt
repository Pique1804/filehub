package com.filehub.backup.ui

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import com.filehub.backup.*
import com.filehub.backup.R
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONObject
import java.net.URLEncoder

@Composable
fun MaintenanceDialog(prefs: Prefs,onClose: ()->Unit) {
    val ctx=LocalContext.current;val scope=rememberCoroutineScope()
    var health by remember {mutableStateOf<BackupHealth?>(null)}
    val text = LocalAppResources.current
    val latestText by rememberUpdatedState(text)
    var history by remember {mutableStateOf<List<BackupLedger.History>>(emptyList())}
    var duplicates by remember {mutableStateOf<JSONObject?>(null)}
    var error by remember {mutableStateOf("")}
    var scan by remember {mutableStateOf(false)}
    LaunchedEffect(Unit) {
        while(true){
            history=withContext(Dispatchers.IO){BackupLedger(ctx).use {it.history(prefs.backupScope())}}
            withContext(Dispatchers.IO){runCatching {Api.backupHealth()}}.onSuccess {health=it;error=""}.onFailure {health=BackupHealth();error=it.message ?: latestText.getString(R.string.backup_offline)}
            if(scan)withContext(Dispatchers.IO){runCatching {Api.jsonGet("/api/photos/duplicates")}}.onSuccess {duplicates=it}
            delay(5000)
        }
    }
    AlertDialog(onDismissRequest=onClose,title={Text(appString(R.string.backup_maintenance_title))},text={
        Column(Modifier.fillMaxWidth().heightIn(max=560.dp).verticalScroll(rememberScrollState())) {
            Text(appString(R.string.backup_originals),style=MaterialTheme.typography.titleSmall)
            BackupHealthSummary(health)
            health?.let { Text(appString(R.string.backup_pending_count, it.processingPending)) }
            Spacer(Modifier.height(16.dp))
            Text(appString(R.string.backup_duplicates_title),style=MaterialTheme.typography.titleSmall)
            Text(appString(R.string.backup_duplicates_help))
            TextButton(onClick={scope.launch {
                withContext(Dispatchers.IO){runCatching {Api.jsonPost("/api/photos/duplicates/scan")}}.onSuccess {scan=true}.onFailure {error=it.message ?: latestText.getString(R.string.backup_scan_error)}
            }}){Text(appString(R.string.backup_duplicates_scan))}
            duplicates?.let {d->
                val groups=d.optJSONArray("groups")
                val label = if (d.optString("state") == "running") R.string.backup_duplicates_scanning else R.string.backup_duplicates_checked
                Text(appString(label, d.optInt("checked"), groups?.length() ?: 0))
                if(groups!=null)for(i in 0 until minOf(groups.length(),30)){
                    val g=groups.getJSONObject(i);val ps=g.getJSONArray("photos")
                    Spacer(Modifier.height(8.dp));Text(appString(R.string.backup_duplicate_group, appQuantity(R.plurals.backup_duplicate_files, ps.length()), localizedSize(text, g.optLong("size"))))
                    for(j in 0 until ps.length()){val p=ps.getJSONObject(j);Text("${p.getString("source")} / ${p.getString("path")}",style=MaterialTheme.typography.bodySmall)}
                }
            }
            Spacer(Modifier.height(16.dp))
            Text(appString(R.string.backup_recent_history),style=MaterialTheme.typography.titleSmall)
            TextButton(onClick={Backup.retryFailed(ctx)}){Text(appString(R.string.backup_retry_failed))}
            if(history.isEmpty())Text(appString(R.string.backup_queue_empty))
            for(item in history){
                HorizontalDivider();Text(item.name)
                val state=when(item.state){"complete"->appString(R.string.backup_queue_completed);"retry"->appString(R.string.backup_queue_retry);"uploading"->appString(R.string.backup_queue_active);else->appString(R.string.backup_queue_waiting)}
                Text(state,style=MaterialTheme.typography.bodySmall)
                if(item.error.isNotBlank())Text(item.error,color=MaterialTheme.colorScheme.error,style=MaterialTheme.typography.bodySmall)
            }
            if(error.isNotBlank())Text(error,color=MaterialTheme.colorScheme.error)
        }
    },confirmButton={TextButton(onClick=onClose){Text(appString(R.string.action_close))}})
}

@Composable
fun VersionsDialog(source: String,path: String,onClose: ()->Unit,onRestored: ()->Unit) {
    val scope=rememberCoroutineScope();val resources=LocalAppResources.current
    var versions by remember {mutableStateOf<List<JSONObject>>(emptyList())}
    var status by remember {mutableStateOf(R.string.file_ui_versions_loading)};var busy by remember {mutableStateOf(false)}
    LaunchedEffect(source,path){
        val q="/api/photo/versions?source=${URLEncoder.encode(source,"UTF-8")}&path=${URLEncoder.encode(path,"UTF-8")}"
        withContext(Dispatchers.IO){runCatching {Api.jsonGet(q).getJSONArray("versions")}}.onSuccess {a->versions=(0 until a.length()).map {a.getJSONObject(it)};status=if(versions.isEmpty())R.string.file_ui_versions_empty else R.string.file_ui_versions_help}.onFailure {status=R.string.file_ui_versions_load_failed}
    }
    AlertDialog(onDismissRequest=onClose,title={Text(appString(R.string.file_ui_versions_title))},text={
        Column(Modifier.heightIn(max=400.dp).verticalScroll(rememberScrollState())){
            Text(appString(status))
            for(v in versions){
                Spacer(Modifier.height(8.dp));Text(fileDateTime(v.getLong("created"), resources.configuration.locales[0]))
                val operation=when(v.getString("operation")){"rotate"->R.string.file_ui_version_before_rotate;"crop"->R.string.file_ui_version_before_crop;"restore"->R.string.file_ui_version_before_restore;else->R.string.file_ui_version_saved}
                Text("${appString(operation)} · ${localizedSize(resources, v.getLong("size"))}")
                TextButton(enabled=!busy,onClick={scope.launch {
                    busy=true
                    val ok=withContext(Dispatchers.IO){Api.maintenanceAction("/api/photo/restore?id=${URLEncoder.encode(v.getString("id"),"UTF-8")}")}
                    busy=false;if(ok){onRestored();onClose()}else status=R.string.file_ui_version_restore_failed
                }}){Text(appString(R.string.file_ui_version_restore))}
            }
        }
    },confirmButton={TextButton(onClick=onClose){Text(appString(R.string.action_close))}})
}
