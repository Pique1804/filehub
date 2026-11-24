package com.filehub.backup

import android.content.Intent
import android.content.Context
import android.net.Uri
import android.os.Bundle
import android.os.Parcelable
import androidx.activity.compose.setContent
import androidx.activity.compose.BackHandler
import androidx.compose.ui.platform.LocalConfiguration
import androidx.fragment.app.FragmentActivity
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.compose.animation.animateColorAsState
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Folder
import androidx.compose.material.icons.outlined.PhotoLibrary
import androidx.compose.material.icons.outlined.Settings
import androidx.compose.material.icons.rounded.PlayCircle
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.saveable.rememberSaveableStateHolder
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.filehub.backup.ui.*
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

class MainActivity : FragmentActivity() {
    private val openDownloads = mutableStateOf(false)
    private val shared = mutableStateOf<List<Uri>>(emptyList())
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        openDownloads.value = savedInstanceState?.getBoolean("openDownloads") ?: false
        shared.value = extractShared(this, intent)
        handleDownloadsIntent(intent)
        setContent { AppTheme { Root(shared, openDownloads) } }
    }
    override fun onSaveInstanceState(outState: Bundle) {
        outState.putBoolean("openDownloads", openDownloads.value)
        super.onSaveInstanceState(outState)
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
        if (intent.action == Intent.ACTION_SEND || intent.action == Intent.ACTION_SEND_MULTIPLE) {
            shared.value = extractShared(this, intent)
        }
        handleDownloadsIntent(intent)
    }

    private fun handleDownloadsIntent(intent: Intent?) {
        if (intent?.action == OfflineDownload.OpenAction) {
            openDownloads.value = true
            intent.action = null
        }
    }
}

private fun extractShared(ctx: Context, intent: Intent?): List<Uri> {
    if (intent == null) return emptyList()
    @Suppress("DEPRECATION")
    return try {
        val items = when (intent.action) {
            Intent.ACTION_SEND -> listOfNotNull(intent.getParcelableExtra<Parcelable>(Intent.EXTRA_STREAM) as? Uri)
            Intent.ACTION_SEND_MULTIPLE -> intent.getParcelableArrayListExtra<Parcelable>(Intent.EXTRA_STREAM)
                ?.filterIsInstance<Uri>().orEmpty()
            else -> return emptyList()
        }
        val clips = intent.clipData
        val uris = items.ifEmpty { if (clips == null) emptyList() else List(clips.itemCount) { clips.getItemAt(it).uri }.filterNotNull() }
        uris.distinct().filter { ManualUploads.canReadSharedUri(ctx, it) }
    } catch (_: RuntimeException) {
        emptyList()
    }
}

@Composable
fun Root(shared: MutableState<List<Uri>>, openDownloads: MutableState<Boolean>) {
    val ctx = LocalContext.current
    val prefs = remember { Prefs(ctx) }
    var loggedIn by remember { mutableStateOf(false) }
    var checking by remember { mutableStateOf(true) }
    var locked by remember { mutableStateOf(prefs.appLock) }
    val unlock = rememberAppUnlock()

    LaunchedEffect(Unit) {
        if (prefs.isConfigured()) {
            // Unlocking the saved account and displaying its local cache must not
            // wait for the server. Network requests reauthenticate as needed.
            loggedIn = withContext(Dispatchers.IO) { Net.restore(prefs.server, prefs.user, prefs.pass) }
        }
        checking = false
    }

    // re-lock whenever the app goes to the background
    val owner = LocalLifecycleOwner.current
    DisposableEffect(owner) {
        val obs = LifecycleEventObserver { _, e -> if (e == Lifecycle.Event.ON_STOP && prefs.appLock) locked = true }
        owner.lifecycle.addObserver(obs)
        onDispose { owner.lifecycle.removeObserver(obs) }
    }
    LaunchedEffect(locked) { if (locked) unlock { locked = false } }

    when {
        locked -> LockScreen { unlock { locked = false } }
        checking -> Box(Modifier.fillMaxSize().background(MaterialTheme.colorScheme.background), contentAlignment = Alignment.Center) {
            CircularProgressIndicator()
        }
        !loggedIn -> LoginScreen(prefs) { loggedIn = true }
        shared.value.isNotEmpty() -> ShareReceiveScreen(shared.value, prefs) { shared.value = emptyList() }
        else -> MainScaffold(prefs, openDownloads) { loggedIn = false }
    }
}

@Composable
fun MainScaffold(prefs: Prefs, openDownloads: MutableState<Boolean>, onLogout: () -> Unit) {
    val ctx = LocalContext.current
    val updates: AppUpdateModel = viewModel()
    val update by updates.state.collectAsState()
    var tab by rememberSaveable { mutableStateOf(0) }
    val showDownloads = { openDownloads.value = true }
    val screenStates = rememberSaveableStateHolder()
    // Auto-check for a new app version on launch (the manual Settings check was easy
    // to miss, so published updates never reached the user).
    var checkedUpdate by rememberSaveable { mutableStateOf(false) }
    LaunchedEffect(Unit) {
        if (!checkedUpdate) {
            checkedUpdate = true
            val latest = withContext(Dispatchers.IO) { Updater.latest() }
            if (latest != null && latest.versionCode > Updater.currentCode(ctx)) updates.offer(latest)
        }
    }
    BackHandler(enabled = tab != 0) { tab = 0 }
    val destinations = listOf(appString(R.string.nav_photos) to Icons.Outlined.PhotoLibrary, appString(R.string.nav_files) to Icons.Outlined.Folder, appString(R.string.nav_settings) to Icons.Outlined.Settings, appString(R.string.video_library_nav) to Icons.Rounded.PlayCircle)
    val wide = LocalConfiguration.current.screenWidthDp >= 720
    Row(Modifier.fillMaxSize().background(Fh.Bg)) {
        if (wide) {
            NavigationRail(containerColor = Fh.Card, header = { Spacer(Modifier.height(16.dp)); FhWordmark(18); Spacer(Modifier.height(24.dp)) }) {
                destinations.forEachIndexed { index, (label, icon) ->
                    NavigationRailItem(selected = tab == index, onClick = { tab = index },
                        colors = NavigationRailItemDefaults.colors(selectedIconColor = Fh.AccentDeep, selectedTextColor = Fh.AccentDeep,
                            indicatorColor = Fh.AccentSoft, unselectedIconColor = Fh.Muted, unselectedTextColor = Fh.Muted),
                        icon = { Icon(icon, null) }, label = { Text(label) }, alwaysShowLabel = true)
                    Spacer(Modifier.height(12.dp))
                }
            }
        }
        Column(Modifier.weight(1f).fillMaxHeight()) {
            Box(Modifier.weight(1f).fillMaxWidth()) {
                screenStates.SaveableStateProvider("${prefs.accountScope()}:$tab") {
                    when (tab) {
                        0 -> PhotosScreen()
                        1 -> FilesScreen(onOpenDownloads = showDownloads)
                        3 -> VideosScreen(onOpenDownloads = showDownloads)
                        else -> SettingsScreen(prefs, onLogout, updates::offer)
                    }
                }
            }
            if (!wide) {
                HorizontalDivider(color = Fh.Divider)
                NavigationBar(containerColor = Fh.Card, tonalElevation = 0.dp, windowInsets = WindowInsets(0, 0, 0, 0)) {
                    destinations.forEachIndexed { index, (label, icon) ->
                        NavigationBarItem(selected = tab == index, onClick = { tab = index },
                            colors = NavigationBarItemDefaults.colors(selectedIconColor = Fh.AccentDeep, selectedTextColor = Fh.AccentDeep,
                                indicatorColor = Fh.AccentSoft, unselectedIconColor = Fh.Muted, unselectedTextColor = Fh.Muted),
                            icon = { Icon(icon, null) }, label = { Text(label) }, alwaysShowLabel = true)
                    }
                }
            }
        }
    }

    if (openDownloads.value) OfflineScreen(onClose = { openDownloads.value = false })

    update?.let { info ->
        AppUpdateDialog(info, onUpdate = { updates.start(ctx.applicationContext) }, onClose = updates::close)
    }
}
