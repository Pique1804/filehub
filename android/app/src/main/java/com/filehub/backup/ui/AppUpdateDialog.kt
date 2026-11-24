package com.filehub.backup.ui

import android.content.Context
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.DialogProperties
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.filehub.backup.R
import com.filehub.backup.Updater
import com.filehub.backup.localizedSize
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.math.RoundingMode
import java.text.NumberFormat

internal data class AppUpdateState(
    val info: Updater.Info,
    val progress: Updater.Progress? = null,
    val result: String? = null,
)

internal class AppUpdateModel : ViewModel() {
    private val mutableState = MutableStateFlow<AppUpdateState?>(null)
    private var download: Job? = null
    val state = mutableState.asStateFlow()

    fun offer(info: Updater.Info) {
        if (mutableState.value == null) mutableState.value = AppUpdateState(info)
    }

    fun start(context: Context) {
        val offer = mutableState.value ?: return
        if (offer.progress != null || offer.result != null) return
        mutableState.value = offer.copy(progress = Updater.Progress.Connecting)
        download = viewModelScope.launch {
            val result = withContext(Dispatchers.IO) {
                Updater.downloadAndInstall(context, offer.info) { progress ->
                    mutableState.value = offer.copy(progress = progress)
                }
            }
            mutableState.value = offer.copy(result = result)
        }
    }

    fun close() {
        if (mutableState.value?.progress == Updater.Progress.OpeningInstaller) return
        download?.cancel()
        download = null
        mutableState.value = null
    }
}

@Composable
internal fun AppUpdateDialog(state: AppUpdateState, onUpdate: () -> Unit, onClose: () -> Unit) {
    val progress = state.progress
    val downloading = progress != null
    AlertDialog(
        onDismissRequest = onClose,
        properties = DialogProperties(dismissOnBackPress = !downloading, dismissOnClickOutside = !downloading),
        shape = Fh.CardShape,
        containerColor = Fh.Card,
        titleContentColor = Fh.Text,
        textContentColor = Fh.Text2,
        title = { Text(appString(R.string.update_available, state.info.versionName), fontFamily = Golos, fontWeight = FontWeight.Bold) },
        text = {
            if (progress != null) {
                AppUpdateProgress(progress)
            } else {
                Text(state.result ?: state.info.notes.ifBlank { appString(R.string.update_prompt) }, fontFamily = Golos)
            }
        },
        confirmButton = {
            if (state.result != null) {
                TextButton(onClick = onClose) { Text(appString(R.string.action_close)) }
            } else if (!downloading) {
                TextButton(onClick = onUpdate) { Text(appString(R.string.action_update)) }
            }
        },
        dismissButton = {
            if (downloading && progress != Updater.Progress.OpeningInstaller) {
                TextButton(onClick = onClose) { Text(appString(R.string.action_cancel)) }
            } else if (!downloading && state.result == null) {
                TextButton(onClick = onClose) { Text(appString(R.string.action_later)) }
            }
        },
    )
}

@Composable
private fun AppUpdateProgress(progress: Updater.Progress) {
    val resources = LocalAppResources.current
    val download = progress as? Updater.Progress.Downloading
    val fraction = download?.fraction
    val stage = when (progress) {
        Updater.Progress.Connecting -> appString(R.string.update_connecting)
        is Updater.Progress.Downloading -> appString(R.string.update_downloading)
        Updater.Progress.Verifying -> appString(R.string.update_verifying)
        Updater.Progress.OpeningInstaller -> appString(R.string.update_opening_installer)
    }
    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Text(stage, fontFamily = Golos)
        if (fraction != null) {
            LinearProgressIndicator(progress = { fraction.toFloat() }, modifier = Modifier.fillMaxWidth())
        } else {
            LinearProgressIndicator(modifier = Modifier.fillMaxWidth())
        }
        if (download != null) {
            val downloaded = localizedSize(resources, download.downloaded)
            val total = download.total
            val detail = if (total != null && fraction != null) {
                val percent = NumberFormat.getPercentInstance(resources.configuration.locales[0]).apply {
                    roundingMode = RoundingMode.DOWN
                }.format(fraction)
                appString(R.string.update_download_progress, downloaded, localizedSize(resources, total), percent)
            } else {
                appString(R.string.update_downloaded_bytes, downloaded)
            }
            Text(detail, fontFamily = Golos, style = MaterialTheme.typography.bodySmall)
        }
    }
}
