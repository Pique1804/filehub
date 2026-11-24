package com.filehub.backup.ui

import android.Manifest
import android.content.Intent
import android.net.Uri
import android.os.Build
import android.os.SystemClock
import android.provider.Settings
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.KeyboardArrowRight
import androidx.compose.material.icons.outlined.Check
import androidx.compose.material.icons.outlined.CloudUpload
import androidx.compose.material.icons.outlined.DeleteSweep
import androidx.compose.material.icons.outlined.Folder
import androidx.compose.material.icons.outlined.Lock
import androidx.compose.material.icons.outlined.Logout
import androidx.compose.material.icons.outlined.SystemUpdate
import androidx.compose.material.icons.outlined.VideoLibrary
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.filehub.backup.*
import com.filehub.backup.R
import com.filehub.backup.Updater
import com.filehub.backup.BuildConfig
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

@Composable
fun SettingsScreen(prefs: Prefs, onLogout: () -> Unit, onUpdateAvailable: (Updater.Info) -> Unit) {
    val ctx = LocalContext.current
    val scope = rememberCoroutineScope()
    val text = LocalAppResources.current
    var showMaintenance by remember {mutableStateOf(false)}
    if(showMaintenance)MaintenanceDialog(prefs){showMaintenance=false}
    var auto by remember { mutableStateOf(prefs.auto) }
    var interval by remember { mutableStateOf(prefs.intervalMin) }
    var wifiOnly by remember { mutableStateOf(prefs.wifiOnly) }
    var source by remember { mutableStateOf(prefs.source) }
    var folder by remember { mutableStateOf(prefs.folder) }
    var status by remember { mutableStateOf(prefs.status) }
    var localStatus by remember { mutableStateOf<Int?>(null) }
    var checking by remember { mutableStateOf(false) }
    var updateStatus by remember { mutableStateOf("") }
    var signingOut by remember { mutableStateOf(false) }
    val serverLogoutUnavailable = appString(R.string.settings_signout_server_unavailable)
    var pickFolder by remember { mutableStateOf(false) }
    var pickAlbums by remember { mutableStateOf(false) }
    var availableAlbums by remember { mutableStateOf<Map<String, Int>>(emptyMap()) }
    var selectedAlbums by remember { mutableStateOf(prefs.selectedAlbums) }
    var appLock by remember { mutableStateOf(prefs.appLock) }
    val bioOk = remember { biometricAvailable(ctx) }

    val appearance = LocalAppearance.current
    DisposableEffect(ctx, prefs) {
        val storage = ctx.getSharedPreferences("filehub", android.content.Context.MODE_PRIVATE)
        val listener = android.content.SharedPreferences.OnSharedPreferenceChangeListener { _, key ->
            if (key == "status") {
                status = prefs.status
                localStatus = null
            }
        }
        storage.registerOnSharedPreferenceChangeListener(listener)
        onDispose { storage.unregisterOnSharedPreferenceChangeListener(listener) }
    }

    // server status: time one cheap API call
    var online by remember { mutableStateOf<Boolean?>(null) }
    var pingMs by remember { mutableStateOf<Long?>(null) }
    var tmdbConnected by remember { mutableStateOf<Boolean?>(null) }
    var showTmdb by remember { mutableStateOf(false) }
    LaunchedEffect(Net.user) {
        tmdbConnected = withContext(Dispatchers.IO) { Api.tmdbSettings() }
    }
    if (showTmdb) TmdbSettingsDialog(tmdbConnected == true, onDismiss = { showTmdb = false }) {
        tmdbConnected = it
        showTmdb = false
    }
    LaunchedEffect(Unit) {
        val result = withContext(Dispatchers.IO) {
            val t0 = SystemClock.elapsedRealtime()
            val ok = Api.isOnline()
            ok to (SystemClock.elapsedRealtime() - t0)
        }
        online = result.first
        pingMs = if (result.first) result.second else null
    }

    val notifications=rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { }
    fun requestNotifications() {
        if(Build.VERSION.SDK_INT>=33 && androidx.core.content.ContextCompat.checkSelfPermission(ctx,Manifest.permission.POST_NOTIFICATIONS)!=android.content.pm.PackageManager.PERMISSION_GRANTED)
            notifications.launch(Manifest.permission.POST_NOTIFICATIONS)
    }
    val perm = rememberLauncherForActivityResult(ActivityResultContracts.RequestMultiplePermissions()) { res ->
        if (res[if (Build.VERSION.SDK_INT >= 33) Manifest.permission.READ_MEDIA_IMAGES else Manifest.permission.READ_EXTERNAL_STORAGE] == true ||
            res[Manifest.permission.READ_MEDIA_VIDEO] == true ||
            (Build.VERSION.SDK_INT >= 34 && res[Manifest.permission.READ_MEDIA_VISUAL_USER_SELECTED] == true)) {
            auto = true; prefs.auto = true; Backup.setAuto(ctx, true); Backup.runNow(ctx); requestNotifications()
            if (Build.VERSION.SDK_INT >= 34 && res[Manifest.permission.READ_MEDIA_VISUAL_USER_SELECTED] == true &&
                res[Manifest.permission.READ_MEDIA_IMAGES] != true && res[Manifest.permission.READ_MEDIA_VIDEO] != true)
                localStatus = R.string.backup_partial_permission
        } else localStatus = R.string.backup_permission_needed
    }
    // "free up space": delete phone copies of media already on the server
    var freeable by remember { mutableStateOf(-1) }
    var freedCount by remember { mutableStateOf(0) }
    var showCleanup by remember { mutableStateOf(false) }
    fun refreshFreeable() { scope.launch { freeable = withContext(Dispatchers.IO) { FreeSpace.candidateCount(ctx) } } }
    LaunchedEffect(Unit) { refreshFreeable() }
    fun mediaPerms() = if (Build.VERSION.SDK_INT >= 34)
        arrayOf(Manifest.permission.READ_MEDIA_IMAGES, Manifest.permission.READ_MEDIA_VIDEO, Manifest.permission.READ_MEDIA_VISUAL_USER_SELECTED)
    else if (Build.VERSION.SDK_INT >= 33)
        arrayOf(Manifest.permission.READ_MEDIA_IMAGES, Manifest.permission.READ_MEDIA_VIDEO)
    else arrayOf(Manifest.permission.READ_EXTERNAL_STORAGE)

    LaunchedEffect(pickAlbums) {
        if (pickAlbums) availableAlbums = withContext(Dispatchers.IO) {
            runCatching { MediaScanner.scan(ctx).groupingBy { it.album }.eachCount().toSortedMap() }.getOrDefault(emptyMap())
        }
    }

    Box(Modifier.fillMaxSize().background(Fh.Bg), contentAlignment = Alignment.TopCenter) {
    Column(Modifier.widthIn(max = 760.dp).fillMaxHeight().fillMaxWidth().verticalScroll(rememberScrollState())) {
        Spacer(Modifier.height(22.dp))
        Text(
            appString(R.string.nav_settings), Modifier.padding(horizontal = 18.dp),
            fontFamily = Golos, fontSize = 26.sp, fontWeight = FontWeight.Bold,
            letterSpacing = (-0.5).sp, color = Fh.Text,
        )

        FhCard(Modifier.padding(start = 18.dp, end = 18.dp, top = 12.dp).fillMaxWidth()) {
            LanguagePicker(Modifier.padding(18.dp))
        }

        // ── Server status ──
        FhCard(Modifier.padding(start = 18.dp, end = 18.dp, top = 16.dp).fillMaxWidth()) {
            Row(Modifier.padding(horizontal = 16.dp, vertical = 14.dp), verticalAlignment = Alignment.CenterVertically) {
                val dot = when (online) {
                    true -> Fh.Green
                    false -> MaterialTheme.colorScheme.error
                    null -> Fh.Chevron
                }
                val halo = when (online) {
                    true -> Fh.GreenSoft
                    false -> MaterialTheme.colorScheme.errorContainer
                    null -> Fh.BgAlt
                }
                Box(Modifier.size(17.dp).background(halo, CircleShape), contentAlignment = Alignment.Center) {
                    Box(Modifier.size(9.dp).background(dot, CircleShape))
                }
                Spacer(Modifier.width(12.dp))
                Column(Modifier.weight(1f)) {
                    Text(prefs.user.ifBlank { text.getString(R.string.settings_profile) }, fontFamily = Golos, fontSize = 15.sp, fontWeight = FontWeight.Bold, color = Fh.Text)
                    val addr = prefs.server.removePrefix("https://").removePrefix("http://").trimEnd('/').ifBlank { text.getString(R.string.settings_server_missing) }
                    val state = when (online) {
                        true -> appString(R.string.settings_connected)
                        false -> appString(R.string.settings_disconnected)
                        null -> appString(R.string.settings_checking)
                    }
                    Text(
                        "$addr · $state", fontFamily = Golos, fontSize = 12.5.sp, color = Fh.Muted,
                        maxLines = 1, overflow = TextOverflow.Ellipsis,
                    )
                }

            }
        }

        FhCard(Modifier.padding(start = 18.dp, end = 18.dp, top = 12.dp).fillMaxWidth()) {
            Column(Modifier.padding(18.dp)) {
                Text(appString(R.string.settings_appearance), style = MaterialTheme.typography.titleMedium, color = Fh.Text)
                Spacer(Modifier.height(4.dp))
                Text(appString(R.string.settings_system_theme_help), style = MaterialTheme.typography.bodySmall, color = Fh.Muted)
                Spacer(Modifier.height(14.dp))
                FhSegmented(AppearanceMode.entries.map { it.value to appString(it.label) }, appearance.mode.value,
                    onSelect = { appearance.select(AppearanceMode.from(it)) })
            }
        }

        // ── Autosync ──
        FhCard(Modifier.padding(start = 18.dp, end = 18.dp, top = 12.dp).fillMaxWidth()) {
            Column(Modifier.padding(18.dp)) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    SettingsIconSquare(Icons.Outlined.CloudUpload)
                    Spacer(Modifier.width(12.dp))
                    Text(appString(R.string.backup_auto_upload), Modifier.weight(1f), fontFamily = Golos, fontSize = 16.sp, fontWeight = FontWeight.Bold, color = Fh.Text)
                    FhSwitch(checked = auto, label = appString(R.string.backup_auto_upload), onChecked = { on ->
                        if (on) perm.launch(mediaPerms())
                        else { auto = false; prefs.auto = false; Backup.setAuto(ctx, false) }
                    })
                }
                Spacer(Modifier.height(10.dp))
                Text(
                    appString(R.string.backup_auto_upload_help),
                    fontFamily = Golos, fontSize = 12.5.sp, lineHeight = 18.sp, color = Fh.Muted,
                )

                if (auto) {
                    Spacer(Modifier.height(16.dp))
                    HorizontalDivider(color = Fh.Divider, thickness = 1.dp)
                    Spacer(Modifier.height(16.dp))

                    FhSectionLabel(appString(R.string.backup_destination))
                    Spacer(Modifier.height(8.dp))
                    Row(
                        Modifier.fillMaxWidth().background(Fh.BgAlt, RoundedCornerShape(12.dp)).padding(horizontal = 14.dp, vertical = 12.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Icon(Icons.Outlined.Folder, null, Modifier.size(19.dp), tint = Fh.AccentDeep)
                        Spacer(Modifier.width(10.dp))
                        Column(Modifier.weight(1f)) {
                            val name = folder.substringAfterLast('/')
                            val parent = folder.substringBeforeLast('/', "")
                            Text(
                                name.ifBlank { folder }, fontFamily = Golos, fontSize = 13.5.sp, fontWeight = FontWeight.SemiBold,
                                color = Fh.Text, maxLines = 1, overflow = TextOverflow.Ellipsis,
                            )
                            Text(
                                if (parent.isBlank()) source else "$source / $parent",
                                fontFamily = Golos, fontSize = 11.5.sp, color = Fh.Muted, maxLines = 1, overflow = TextOverflow.Ellipsis,
                            )
                        }
                        Text(
                            appString(R.string.action_change), fontFamily = Golos, fontSize = 13.sp, fontWeight = FontWeight.SemiBold, color = Fh.AccentDeep,
                            modifier = Modifier
                                .clickable { pickFolder = true }
                                .heightIn(min = 48.dp).padding(start = 8.dp, top = 14.dp, bottom = 14.dp),
                        )
                    }

                    Spacer(Modifier.height(16.dp))
                    FhSectionLabel(appString(R.string.backup_phone_albums))
                    Spacer(Modifier.height(8.dp))
                    SettingsSoftButton(
                        text = if (selectedAlbums.isEmpty()) appString(R.string.backup_all_media) else appQuantity(R.plurals.backup_albums_selected, selectedAlbums.size),
                        onClick = { pickAlbums = true },
                    )
                    Spacer(Modifier.height(16.dp))
                    FhSectionLabel(appString(R.string.backup_frequency))
                    Spacer(Modifier.height(8.dp))
                    FhSegmented(
                        options = listOf("15" to appString(R.string.backup_every_15_minutes), "60" to appString(R.string.backup_every_hour), "1440" to appString(R.string.backup_every_day)),
                        selected = interval.toString(),
                        onSelect = { id ->
                            interval = id.toLong(); prefs.intervalMin = interval; if (auto) Backup.setAuto(ctx, true)
                        },
                    )

                    Spacer(Modifier.height(16.dp))
                    FhSectionLabel(appString(R.string.backup_upload_network))
                    Spacer(Modifier.height(8.dp))
                    FhSegmented(
                        options = listOf("wifi" to appString(R.string.backup_wifi_only), "any" to appString(R.string.backup_wifi_mobile)),
                        selected = if (wifiOnly) "wifi" else "any",
                        onSelect = { id ->
                            wifiOnly = id == "wifi"; prefs.wifiOnly = wifiOnly; if (auto) Backup.setAuto(ctx, true); Backup.settingsChanged(ctx)
                        },
                    )
                    Spacer(Modifier.height(8.dp))
                    Text(
                        if (wifiOnly) appString(R.string.backup_wifi_help) else appString(R.string.backup_mobile_help),
                        fontFamily = Golos, fontSize = 12.sp, lineHeight = 17.sp, color = Fh.Muted,
                    )

                    Spacer(Modifier.height(18.dp))
                    SettingsPrimaryButton(appString(R.string.backup_sync_now)) {
                        Backup.runNow(ctx)
                        localStatus = if (Net.supportsBackgroundBackup(prefs.server)) R.string.backup_started else R.string.backup_https_required
                    }
                    Spacer(Modifier.height(10.dp))
                    Text(
                        localStatus?.let { text.getString(it) } ?: status.ifBlank { text.getString(R.string.backup_not_started) },
                        Modifier.fillMaxWidth(), textAlign = TextAlign.Center,
                        fontFamily = Golos, fontSize = 12.5.sp, lineHeight = 18.sp, color = Fh.Muted,
                    )
                }
            }
        }

        FhCard(Modifier.padding(start = 18.dp, end = 18.dp, top = 12.dp).fillMaxWidth()) {
            Column(Modifier.fillMaxWidth().padding(horizontal = 8.dp, vertical = 4.dp)) {
                TextButton(onClick = { showMaintenance = true }, modifier = Modifier.fillMaxWidth().heightIn(min = 48.dp)) {
                    Text(appString(R.string.backup_management), modifier = Modifier.weight(1f))
                    Icon(Icons.AutoMirrored.Outlined.KeyboardArrowRight, null)
                }
                HorizontalDivider(color = Fh.Divider, modifier = Modifier.padding(horizontal = 10.dp))
                TextButton(onClick = { requestNotifications() }, modifier = Modifier.fillMaxWidth().heightIn(min = 48.dp)) {
                    Text(appString(R.string.backup_notifications), modifier = Modifier.weight(1f))
                    Icon(Icons.AutoMirrored.Outlined.KeyboardArrowRight, null)
                }
            }
        }
        // ── Free up space ──
        FhCard(Modifier.padding(start = 18.dp, end = 18.dp, top = 12.dp).fillMaxWidth()) {
            Column(Modifier.padding(18.dp)) {
                if (freedCount > 0 && freeable == 0) {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        SettingsIconSquare(Icons.Outlined.Check, bg = Fh.GreenSoft, tint = Fh.GreenDeep)
                        Spacer(Modifier.width(12.dp))
                        Column(Modifier.weight(1f)) {
                            Text(appString(R.string.action_done), fontFamily = Golos, fontSize = 15.sp, fontWeight = FontWeight.Bold, color = Fh.Text)
                            Text(
                                appString(R.string.cleanup_freed_count, freedCount),
                                fontFamily = Golos, fontSize = 12.5.sp, color = Fh.Muted,
                            )
                        }
                    }
                } else {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        SettingsIconSquare(Icons.Outlined.DeleteSweep)
                        Spacer(Modifier.width(12.dp))
                        Text(appString(R.string.cleanup_free_space), Modifier.weight(1f), fontFamily = Golos, fontSize = 16.sp, fontWeight = FontWeight.Bold, color = Fh.Text)
                    }
                    Spacer(Modifier.height(10.dp))
                    Text(
                        if (freeable > 0) appQuantity(R.plurals.cleanup_eligible, freeable)
                        else appString(R.string.cleanup_verified_only),
                        fontFamily = Golos, fontSize = 12.5.sp, lineHeight = 18.sp, color = Fh.Muted,
                    )
                    Spacer(Modifier.height(14.dp))
                    SettingsSoftButton(
                        text = appString(R.string.cleanup_review),
                        enabled = freeable >= 0,
                        onClick = { showCleanup = true },
                    )
                }
            }
        }

        // ── Background work (battery optimizations) ──
        FhCard(Modifier.padding(start = 18.dp, end = 18.dp, top = 12.dp).fillMaxWidth()) {
            Row(
                Modifier
                    .clickable(interactionSource = remember { MutableInteractionSource() }, indication = null) {
                        runCatching {
                            ctx.startActivity(Intent(Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS, Uri.parse("package:${ctx.packageName}")))
                        }
                    }
                    .padding(horizontal = 18.dp, vertical = 14.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Column(Modifier.weight(1f)) {
                    Text(appString(R.string.settings_background), fontFamily = Golos, fontSize = 14.5.sp, fontWeight = FontWeight.SemiBold, color = Fh.Text)
                    Text(appString(R.string.settings_background_help), fontFamily = Golos, fontSize = 12.sp, color = Fh.Muted)
                }
                Spacer(Modifier.width(10.dp))
                Text(appString(R.string.action_allow), fontFamily = Golos, fontSize = 13.sp, fontWeight = FontWeight.SemiBold, color = Fh.AccentDeep)
            }
        }

        tmdbConnected?.let { connected ->
            FhCard(Modifier.padding(start = 18.dp, end = 18.dp, top = 12.dp).fillMaxWidth()) {
                Row(
                    Modifier.fillMaxWidth().clickable { showTmdb = true }.padding(14.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    SettingsIconSquare(Icons.Outlined.VideoLibrary)
                    Spacer(Modifier.width(12.dp))
                    Column(Modifier.weight(1f)) {
                        Text(appString(R.string.tmdb_settings), fontFamily = Golos, fontSize = 14.5.sp,
                            fontWeight = FontWeight.SemiBold, color = Fh.Text)
                        Text(appString(if (connected) R.string.tmdb_connected else R.string.tmdb_disconnected),
                            fontFamily = Golos, fontSize = 12.sp, color = Fh.Muted)
                    }
                    Icon(Icons.AutoMirrored.Outlined.KeyboardArrowRight, null, tint = Fh.Muted)
                }
            }
        }

        // ── App / update check ──
        FhCard(Modifier.padding(start = 18.dp, end = 18.dp, top = 12.dp).fillMaxWidth()) {
            Column(Modifier.padding(18.dp)) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    SettingsIconSquare(Icons.Outlined.SystemUpdate)
                    Spacer(Modifier.width(12.dp))
                    Column(Modifier.weight(1f)) {
                        Text(appString(R.string.settings_app), fontFamily = Golos, fontSize = 16.sp, fontWeight = FontWeight.Bold, color = Fh.Text)
                        Text(appString(R.string.settings_version, BuildConfig.VERSION_NAME), fontFamily = Golos, fontSize = 12.5.sp, color = Fh.Muted)
                    }
                }
                Spacer(Modifier.height(14.dp))
                SettingsSoftButton(text = appString(R.string.update_check), enabled = !checking, loading = checking, onClick = {
                    checking = true
                    updateStatus = ""
                    scope.launch {
                        val latest = withContext(Dispatchers.IO) { Updater.latest() }
                        checking = false
                        when {
                            latest == null -> updateStatus = text.getString(R.string.update_check_failed)
                            latest.versionCode > Updater.currentCode(ctx) -> onUpdateAvailable(latest)
                            else -> updateStatus = text.getString(R.string.update_current)
                        }
                    }
                })
                if (updateStatus.isNotBlank()) {
                    Spacer(Modifier.height(8.dp))
                    Text(updateStatus, color = Fh.Muted, fontFamily = Golos, fontSize = 12.5.sp)
                }
            }
        }

        // ── App lock ──
        if (bioOk) {
            FhCard(Modifier.padding(start = 18.dp, end = 18.dp, top = 12.dp).fillMaxWidth()) {
                Row(Modifier.padding(horizontal = 18.dp, vertical = 14.dp), verticalAlignment = Alignment.CenterVertically) {
                    SettingsIconSquare(Icons.Outlined.Lock)
                    Spacer(Modifier.width(12.dp))
                    Column(Modifier.weight(1f)) {
                        Text(appString(R.string.settings_app_lock), fontFamily = Golos, fontSize = 14.5.sp, fontWeight = FontWeight.SemiBold, color = Fh.Text)
                        Text(appString(R.string.settings_app_lock_help), fontFamily = Golos, fontSize = 12.sp, color = Fh.Muted)
                    }
                    Spacer(Modifier.width(10.dp))
                    FhSwitch(checked = appLock, label = appString(R.string.settings_app_lock), onChecked = { appLock = it; prefs.appLock = it })
                }
            }
        }

        // ── Logout ──
        FhCard(Modifier.padding(start = 18.dp, end = 18.dp, top = 12.dp).fillMaxWidth()) {
            Row(
                Modifier
                    .clickable(enabled = !signingOut, interactionSource = remember { MutableInteractionSource() }, indication = null) {
                        signingOut = true
                        scope.launch {
                            val revoked = withContext(Dispatchers.IO) { Net.revokeSession() }
                            Backup.setAuto(ctx, false); prefs.logout(); Net.clear()
                            androidx.work.WorkManager.getInstance(ctx).cancelUniqueWork("filehub-manual")
                            androidx.work.WorkManager.getInstance(ctx).cancelUniqueWork("filehub-manual-retry")
                            runCatching { coil.Coil.imageLoader(ctx).memoryCache?.clear() }
                            if (!revoked) android.widget.Toast.makeText(ctx, serverLogoutUnavailable, android.widget.Toast.LENGTH_LONG).show()
                            onLogout()
                        }
                    }
                    .padding(horizontal = 18.dp, vertical = 14.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                SettingsIconSquare(Icons.Outlined.Logout, bg = MaterialTheme.colorScheme.errorContainer, tint = MaterialTheme.colorScheme.onErrorContainer)
                Spacer(Modifier.width(12.dp))
                Column(Modifier.weight(1f)) {
                    Text(appString(R.string.settings_signout), fontFamily = Golos, fontSize = 14.5.sp, fontWeight = FontWeight.SemiBold, color = Fh.Text)
                    Text(appString(R.string.settings_signout_help), fontFamily = Golos, fontSize = 12.sp, color = Fh.Muted)
                }
                Icon(Icons.AutoMirrored.Outlined.KeyboardArrowRight, null, Modifier.size(18.dp), tint = Fh.Chevron)
            }
        }

        Spacer(Modifier.height(20.dp))
        Text(
            appString(R.string.settings_footer, BuildConfig.VERSION_NAME),
            Modifier.fillMaxWidth(), textAlign = TextAlign.Center,
            fontFamily = Golos, fontSize = 12.sp, color = Fh.Faint,
        )
        Spacer(Modifier.height(32.dp))
    }

    }

    if (pickFolder) FolderPickerDialog(onPick = { s, p ->
        source = s; folder = p; prefs.source = s; prefs.folder = p; Backup.settingsChanged(ctx); pickFolder = false
    }, onDismiss = { pickFolder = false })

    if (showCleanup) CleanupBackupDialog(onClose = { showCleanup = false }, onDeleted = { count ->
        freedCount = count
        localStatus = R.string.cleanup_finished
        refreshFreeable()
    })

    if (pickAlbums) AlertDialog(
        onDismissRequest = { pickAlbums = false },
        title = { Text(appString(R.string.backup_select_albums), fontFamily = Golos) },
        text = {
            Column(Modifier.heightIn(max = 420.dp).verticalScroll(rememberScrollState())) {
                TextButton(onClick = { selectedAlbums = emptySet() }) { Text(appString(R.string.action_select_all_accessible)) }
                availableAlbums.forEach { (album, count) ->
                    Row(Modifier.fillMaxWidth().clickable {
                        selectedAlbums = if (album in selectedAlbums) selectedAlbums - album else selectedAlbums + album
                    }, verticalAlignment = Alignment.CenterVertically) {
                        Checkbox(checked = album in selectedAlbums, onCheckedChange = { checked ->
                            selectedAlbums = if (checked) selectedAlbums + album else selectedAlbums - album
                        })
                        Text("$album · $count", maxLines = 2, fontFamily = Golos, fontSize = 13.sp)
                    }
                }
            }
        },
        confirmButton = { TextButton(onClick = {
            prefs.selectedAlbums = selectedAlbums; pickAlbums = false
            if (auto) Backup.runNow(ctx)
        }) { Text(appString(R.string.action_save)) } },
        dismissButton = { TextButton(onClick = { selectedAlbums = prefs.selectedAlbums; pickAlbums = false }) { Text(appString(R.string.action_cancel)) } },
    )
}

/** 40dp tinted icon square used at the start of settings cards. */
@Composable
private fun SettingsIconSquare(
    icon: ImageVector,
    size: Dp = 40.dp,
    corner: Dp = 12.dp,
    bg: Color = Fh.AccentSoft,
    tint: Color = Fh.AccentDeep,
) {
    Box(Modifier.size(size).background(bg, RoundedCornerShape(corner)), contentAlignment = Alignment.Center) {
        Icon(icon, null, Modifier.size(20.dp), tint = tint)
    }
}

/** Full-width accent button, radius 14, white bold label. */
@Composable
private fun SettingsPrimaryButton(text: String, modifier: Modifier = Modifier, onClick: () -> Unit) {
    Surface(onClick = onClick, modifier = modifier.fillMaxWidth(), shape = RoundedCornerShape(12.dp), color = Fh.Accent) {
        Box(Modifier.padding(vertical = 14.dp), contentAlignment = Alignment.Center) {
            Text(text, fontFamily = Golos, fontSize = 15.sp, fontWeight = FontWeight.Bold, color = Fh.OnAccent)
        }
    }
}

/** Full-width secondary button: soft accent background, deep accent label. */
@Composable
private fun SettingsSoftButton(
    text: String,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
    loading: Boolean = false,
    onClick: () -> Unit,
) {
    Surface(
        onClick = onClick, enabled = enabled, modifier = modifier.fillMaxWidth(),
        shape = RoundedCornerShape(12.dp), color = if (enabled) Fh.AccentSoft else Fh.BgAlt,
    ) {
        Box(Modifier.heightIn(min = 48.dp).padding(horizontal = 8.dp, vertical = 13.dp), contentAlignment = Alignment.Center) {
            if (loading) CircularProgressIndicator(Modifier.size(18.dp), color = Fh.AccentDeep, strokeWidth = 2.dp)
            else Text(text, fontFamily = Golos, fontSize = 14.5.sp, fontWeight = FontWeight.Bold, color = if (enabled) Fh.AccentDeep else Fh.Faint)
        }
    }
}

@Composable
private fun TmdbSettingsDialog(connected: Boolean, onDismiss: () -> Unit, onSaved: (Boolean) -> Unit) {
    val scope = rememberCoroutineScope()
    var credential by remember { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    var failed by remember { mutableStateOf(false) }
    fun update(value: String?) {
        busy = true
        failed = false
        credential = ""
        scope.launch {
            val result = withContext(Dispatchers.IO) { Api.updateTmdbSettings(value) }
            busy = false
            if (result == null) failed = true else onSaved(result)
        }
    }
    AlertDialog(
        onDismissRequest = { if (!busy) onDismiss() },
        title = { Text(appString(R.string.tmdb_settings)) },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedTextField(credential, { credential = it.take(2048) }, Modifier.fillMaxWidth(),
                    label = { Text(appString(R.string.tmdb_credential)) }, singleLine = true,
                    enabled = !busy, visualTransformation = PasswordVisualTransformation())
                if (busy) LinearProgressIndicator(Modifier.fillMaxWidth())
                if (failed) Text(appString(R.string.tmdb_settings_failed), color = MaterialTheme.colorScheme.error)
                if (connected) TextButton(onClick = { update(null) }, enabled = !busy) {
                    Text(appString(R.string.tmdb_disconnect))
                }
            }
        },
        confirmButton = { TextButton(onClick = { update(credential.trim()) }, enabled = !busy && credential.isNotBlank()) {
            Text(appString(R.string.action_save))
        } },
        dismissButton = { TextButton(onClick = onDismiss, enabled = !busy) { Text(appString(R.string.action_cancel)) } },
    )
}
