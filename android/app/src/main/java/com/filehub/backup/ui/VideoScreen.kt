package com.filehub.backup.ui

import android.content.Intent
import android.graphics.drawable.ColorDrawable
import android.net.Uri
import android.os.Build
import android.view.WindowManager
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.CloudDownload
import androidx.compose.material.icons.outlined.OpenInNew
import androidx.compose.material.icons.rounded.ArrowBack
import androidx.compose.material.icons.rounded.Close
import androidx.compose.material.icons.rounded.Settings
import androidx.compose.material.icons.rounded.ViewList
import androidx.compose.material.icons.rounded.Forward10
import androidx.compose.material.icons.rounded.Pause
import androidx.compose.material.icons.rounded.PlayArrow
import androidx.compose.material.icons.rounded.Replay10
import androidx.compose.material.icons.rounded.SkipNext
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.semantics.onClick
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import androidx.compose.ui.window.DialogWindowProvider
import androidx.core.view.WindowCompat
import androidx.core.view.WindowInsetsCompat
import androidx.core.view.WindowInsetsControllerCompat
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.media3.common.C
import androidx.media3.common.MediaItem
import androidx.media3.common.MimeTypes
import androidx.media3.common.PlaybackException
import androidx.media3.common.Player
import androidx.media3.common.TrackSelectionOverride
import androidx.media3.common.Tracks
import androidx.media3.datasource.okhttp.OkHttpDataSource
import androidx.media3.datasource.HttpDataSource
import androidx.media3.datasource.DefaultDataSource
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.source.DefaultMediaSourceFactory
import androidx.media3.ui.AspectRatioFrameLayout
import androidx.media3.ui.PlayerView
import com.filehub.backup.Api
import com.filehub.backup.LibraryVideo
import com.filehub.backup.Net
import com.filehub.backup.R
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.util.Locale

private val WatchBackground = Color.Black
private val WatchPanel = Color(0xFF151C2A)
private val WatchText = Color(0xFFF7F8FF)
private val WatchMuted = Color(0xFFD4DEEF)
private val WatchAccent = Color(0xFF9BC8FF)
private val WatchSpeeds = listOf(.75f, 1f, 1.25f, 1.5f, 2f)

@androidx.annotation.OptIn(androidx.media3.common.util.UnstableApi::class)
@Composable
fun VideoScreen(
    source: String,
    path: String,
    name: String,
    onDownloadOffline: (() -> Unit)? = null,
    localUri: Uri? = null,
    position: Double = 0.0,
    onPosition: ((Double) -> Unit)? = null,
    series: String? = null,
    episodes: List<LibraryVideo> = emptyList(),
    onEpisode: ((LibraryVideo) -> Unit)? = null,
    onClose: () -> Unit,
) {
    val ctx = LocalContext.current
    val resources = LocalAppResources.current
    val scope = rememberCoroutineScope()
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    val latestPositionCallback by rememberUpdatedState(onPosition)
    val activeEpisode = episodes.indexOfFirst { it.source == source && it.path == path }
    val nextEpisode = episodes.getOrNull(activeEpisode + 1).takeIf { activeEpisode >= 0 }
    var showEpisodes by remember { mutableStateOf(false) }
    var settings by remember { mutableStateOf(false) }
    var seasonFilter by remember { mutableStateOf<Int?>(null) }
    var chrome by remember { mutableStateOf(true) }
    var interaction by remember { mutableIntStateOf(0) }
    var fill by rememberSaveable { mutableStateOf(false) }
    var speed by rememberSaveable { mutableFloatStateOf(1f) }
    var jumpCue by remember { mutableIntStateOf(0) }
    var jumpCount by remember { mutableIntStateOf(0) }
    var tracks by remember(source, path) { mutableStateOf(Tracks.EMPTY) }
    var compatible by remember(source, path) {
        mutableStateOf(path.substringAfterLast('.').lowercase(Locale.ROOT) !in setOf("mp4", "m4v", "mov", "webm"))
    }
    var startAt by rememberSaveable(source, path) { mutableLongStateOf((position * 1000).toLong().coerceAtLeast(0)) }
    var attempt by remember(source, path) { mutableIntStateOf(0) }
    var playbackError by remember(source, path) { mutableStateOf<Int?>(null) }
    val failed = playbackError != null
    var completed by remember(source, path) { mutableStateOf(false) }
    var wantsPlayback by rememberSaveable(source, path) { mutableStateOf(true) }
    var requestedPlay by remember(source, path) { mutableStateOf(true) }
    var lastSaved by remember(source, path) { mutableLongStateOf(startAt) }
    var isPlaying by remember(source, path) { mutableStateOf(false) }
    var buffering by remember(source, path) { mutableStateOf(true) }
    var elapsed by remember(source, path) { mutableLongStateOf(startAt) }
    var duration by remember(source, path) { mutableLongStateOf(0) }
    var seekPreview by remember(source, path) { mutableStateOf<Float?>(null) }

    val player = remember(source, path, localUri, compatible, attempt) {
        ExoPlayer.Builder(ctx)
            .setMediaSourceFactory(DefaultMediaSourceFactory(DefaultDataSource.Factory(ctx, OkHttpDataSource.Factory(Net.client))))
            .build()
    }

    fun rememberPosition(force: Boolean = false) {
        val current = player.currentPosition
        if (completed || player.duration <= 0 || current == lastSaved) return
        if (force || kotlin.math.abs(current - lastSaved) >= 10_000) {
            lastSaved = current
            latestPositionCallback?.invoke(current / 1000.0)
        }
    }

    DisposableEffect(player) {
        val useHls = localUri == null && compatible
        val item = MediaItem.Builder()
            .setUri(localUri ?: Uri.parse(if (useHls) Api.hlsVideoUrl(source, path) else Api.downloadUrl(source, path, inline = true)))
            .apply { if (useHls) setMimeType(MimeTypes.APPLICATION_M3U8) }
            .build()
        playbackError = null
        buffering = true
        duration = 0
        seekPreview = null
        tracks = Tracks.EMPTY
        requestedPlay = wantsPlayback
        val listener = object : Player.Listener {
            override fun onIsPlayingChanged(playing: Boolean) {
                isPlaying = playing
            }

            override fun onPlayWhenReadyChanged(playWhenReady: Boolean, reason: Int) {
                requestedPlay = playWhenReady
            }

            override fun onTracksChanged(nextTracks: Tracks) {
                tracks = nextTracks
            }

            override fun onPlayerError(error: PlaybackException) {
                if (localUri == null && !useHls && error.errorCode in 3000..4999) {
                    startAt = maxOf(lastSaved, player.currentPosition)
                    compatible = true
                } else {
                    val status = (error.cause as? HttpDataSource.InvalidResponseCodeException)?.responseCode
                    playbackError = when {
                        status == 401 || status == 403 -> R.string.video_play_access_error
                        status == 404 -> R.string.video_play_missing
                        status == 422 || error.errorCode in 3000..4999 -> R.string.video_play_format_error
                        status != null && status >= 500 -> R.string.video_play_server_error
                        error.errorCode == PlaybackException.ERROR_CODE_IO_NETWORK_CONNECTION_FAILED ||
                            error.errorCode == PlaybackException.ERROR_CODE_IO_NETWORK_CONNECTION_TIMEOUT ->
                            R.string.video_play_connection_error
                        else -> R.string.video_play_error
                    }
                    buffering = false
                }
            }

            override fun onPlaybackStateChanged(state: Int) {
                buffering = state == Player.STATE_BUFFERING
                if (state == Player.STATE_ENDED) {
                    completed = true
                    lastSaved = 0
                    latestPositionCallback?.invoke(0.0)
                }
            }
        }
        val observer = LifecycleEventObserver { _, event ->
            if (event == Lifecycle.Event.ON_STOP) {
                player.pause()
                rememberPosition(true)
            }
        }
        lifecycle.addObserver(observer)
        player.addListener(listener)
        player.setMediaItem(item, startAt)
        player.prepare()
        player.playWhenReady = wantsPlayback
        onDispose {
            lifecycle.removeObserver(observer)
            player.removeListener(listener)
            player.release()
        }
    }

    LaunchedEffect(player, speed) {
        player.setPlaybackSpeed(speed)
    }
    LaunchedEffect(chrome, isPlaying, showEpisodes, settings, seekPreview != null, interaction) {
        if (chrome && isPlaying && !showEpisodes && !settings && seekPreview == null) {
            delay(3_200)
            chrome = false
        }
    }
    LaunchedEffect(jumpCount) {
        if (jumpCount != 0) {
            delay(720)
            jumpCue = 0
        }
    }
    LaunchedEffect(player) {
        while (true) {
            duration = player.duration.coerceAtLeast(0)
            elapsed = player.currentPosition
            if (duration > 0 && !completed) startAt = elapsed
            if (player.isPlaying) rememberPosition()
            delay(350)
        }
    }

    fun close() {
        rememberPosition(true)
        onClose()
    }

    fun choose(episode: LibraryVideo) {
        rememberPosition(true)
        onEpisode?.invoke(episode)
        showEpisodes = false
        chrome = true
        interaction++
    }

    fun seek(target: Long) {
        if (duration <= 0 || !player.isCurrentMediaItemSeekable) return
        completed = false
        player.seekTo(target.coerceIn(0, duration))
        elapsed = player.currentPosition
        startAt = elapsed
        interaction++
    }

    fun toggle() {
        if (requestedPlay && !completed) {
            wantsPlayback = false
            player.pause()
            rememberPosition(true)
        } else {
            if (completed) seek(0)
            wantsPlayback = true
            player.play()
        }
        chrome = true
        interaction++
    }

    fun toggleChrome() {
        chrome = !chrome
        interaction++
    }

    fun skip(direction: Int) {
        seek(player.currentPosition + direction * 10_000L)
        jumpCue = direction
        jumpCount++
    }

    fun setTrack(type: Int, group: Tracks.Group?, index: Int = 0) {
        player.trackSelectionParameters = player.trackSelectionParameters.buildUpon()
            .clearOverridesOfType(type)
            .setTrackTypeDisabled(type, group == null)
            .apply { if (group != null) setOverrideForType(TrackSelectionOverride(group.mediaTrackGroup, index)) }
            .build()
    }

    fun openExternal() {
        if (localUri != null) {
            openLocal(ctx, localUri, offlineMime(name))
            return
        }
        scope.launch {
            val url = withContext(Dispatchers.IO) { Api.streamUrl(source, path) }
            if (url == null) {
                android.widget.Toast.makeText(ctx, resources.getString(R.string.photo_ui_link_failed), android.widget.Toast.LENGTH_SHORT).show()
                return@launch
            }
            val mime = java.net.URLConnection.guessContentTypeFromName(name) ?: "video/*"
            val intent = Intent(Intent.ACTION_VIEW).apply { setDataAndType(Uri.parse(url), mime) }
            runCatching { ctx.startActivity(Intent.createChooser(intent, resources.getString(R.string.photo_ui_player_chooser))) }
                .onFailure {
                    android.widget.Toast.makeText(ctx, resources.getString(R.string.photo_ui_external_player_missing), android.widget.Toast.LENGTH_LONG).show()
                }
        }
    }

    Dialog(
        onDismissRequest = {
            when {
                settings -> settings = false
                showEpisodes -> showEpisodes = false
                else -> close()
            }
        },
        // Compose 1.6 measures non-default-width dialogs using screenHeightDp,
        // which can exclude the system-bar area. Size this window explicitly.
        properties = DialogProperties(dismissOnClickOutside = false, decorFitsSystemWindows = false),
    ) {
        ImmersiveVideoWindow(isPlaying || (buffering && requestedPlay))
        val safe = WindowInsets.safeDrawing
        val gestures = appString(R.string.video_controls)
        val visible = chrome || completed || failed
        val current = episodes.getOrNull(activeEpisode)
        val episodeTitle = current?.let {
            listOfNotNull(
                it.season.takeIf { n -> n > 0 }?.let { n -> appString(R.string.video_library_season, n) },
                it.episode.takeIf { n -> n > 0 }?.let { n -> appString(R.string.video_library_episode, n) },
                name,
            ).joinToString(" · ")
        } ?: name

        BoxWithConstraints(Modifier.fillMaxSize().background(WatchBackground)) {
            val compact = maxHeight < 360.dp
            val landscape = maxWidth >= maxHeight
            val side = maxWidth.coerceAtMost(384.dp)
            val insetPadding = if (compact) 8.dp else 14.dp
            AndroidView(
                factory = { viewContext ->
                    PlayerView(viewContext).apply {
                        useController = false
                        setKeepContentOnPlayerReset(false)
                        setBackgroundColor(android.graphics.Color.BLACK)
                    }
                },
                update = {
                    it.player = player
                    it.resizeMode = if (fill) AspectRatioFrameLayout.RESIZE_MODE_ZOOM else AspectRatioFrameLayout.RESIZE_MODE_FIT
                },
                modifier = Modifier.matchParentSize(),
            )
            Box(
                Modifier.matchParentSize()
                    .semantics { onClick(label = gestures) { toggleChrome(); true } }
                    .pointerInput(player, requestedPlay, duration, chrome, completed) {
                        detectTapGestures(
                            onTap = { toggleChrome() },
                            onDoubleTap = { point ->
                                when {
                                    point.x < size.width * .38f -> skip(-1)
                                    point.x > size.width * .62f -> skip(1)
                                    else -> toggle()
                                }
                            },
                        )
                    },
            )

            if (visible) {
                Box(Modifier.matchParentSize().background(Brush.verticalGradient(
                    0f to Color.Black.copy(alpha = .78f), .26f to Color.Transparent,
                    .64f to Color.Transparent, 1f to Color.Black.copy(alpha = .91f),
                )))
                Row(
                    Modifier.align(Alignment.TopCenter).fillMaxWidth()
                        .windowInsetsPadding(safe.only(WindowInsetsSides.Top + WindowInsetsSides.Horizontal))
                        .padding(horizontal = insetPadding, vertical = if (compact) 2.dp else 8.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    IconButton(onClick = ::close) {
                        Icon(Icons.Rounded.ArrowBack, appString(R.string.action_close), tint = WatchText)
                    }
                    Column(Modifier.weight(1f).padding(start = 3.dp, end = 8.dp)) {
                        Text(series ?: name, color = WatchText, style = MaterialTheme.typography.titleMedium,
                            fontWeight = FontWeight.SemiBold, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        if (series != null) Text(episodeTitle, color = WatchMuted, style = MaterialTheme.typography.bodySmall,
                            maxLines = 1, overflow = TextOverflow.Ellipsis)
                    }
                    if (episodes.isNotEmpty()) IconButton(onClick = {
                        seasonFilter = current?.season
                        showEpisodes = true
                        settings = false
                    }) {
                        Icon(Icons.Rounded.ViewList, appString(R.string.video_library_episodes), tint = WatchText)
                    }
                    IconButton(onClick = { settings = !settings; showEpisodes = false; interaction++ }) {
                        Icon(Icons.Rounded.Settings, appString(R.string.video_settings), tint = WatchText)
                    }
                }
                if (!buffering && !failed) {
                    Row(Modifier.align(Alignment.Center), horizontalArrangement = Arrangement.spacedBy(if (compact) 18.dp else 24.dp),
                        verticalAlignment = Alignment.CenterVertically) {
                        VideoRoundButton(Icons.Rounded.Replay10, appString(R.string.video_back_ten), { skip(-1) })
                        VideoRoundButton(
                            if (requestedPlay && !completed) Icons.Rounded.Pause else Icons.Rounded.PlayArrow,
                            appString(if (requestedPlay && !completed) R.string.video_pause else R.string.video_play),
                            ::toggle, large = true,
                        )
                        VideoRoundButton(Icons.Rounded.Forward10, appString(R.string.video_forward_ten), { skip(1) })
                    }
                }
                Column(
                    Modifier.align(Alignment.BottomCenter).fillMaxWidth()
                        .windowInsetsPadding(safe.only(WindowInsetsSides.Bottom + WindowInsetsSides.Horizontal))
                        .padding(horizontal = insetPadding + 8.dp, vertical = if (compact) 2.dp else 8.dp),
                ) {
                    val displayed = seekPreview?.let { (it * duration).toLong() } ?: elapsed
                    Slider(
                        value = seekPreview ?: (elapsed.toFloat() / duration.coerceAtLeast(1)).coerceIn(0f, 1f),
                        modifier = Modifier.fillMaxWidth().height(34.dp),
                        onValueChange = { seekPreview = it },
                        onValueChangeFinished = {
                            seekPreview?.let { seek((it * duration).toLong()) }
                            seekPreview = null
                        },
                        enabled = duration > 0 && player.isCurrentMediaItemSeekable,
                        colors = SliderDefaults.colors(thumbColor = WatchAccent, activeTrackColor = WatchAccent,
                            inactiveTrackColor = Color.White.copy(alpha = .28f)),
                    )
                    Row(Modifier.fillMaxWidth().heightIn(min = 34.dp), verticalAlignment = Alignment.CenterVertically) {
                        Text(videoClock(displayed), color = WatchText, style = MaterialTheme.typography.labelMedium)
                        Text("  /  " + if (duration > 0) videoClock(duration) else "–:––",
                            color = WatchMuted, style = MaterialTheme.typography.labelMedium)
                        Spacer(Modifier.weight(1f))
                        if (nextEpisode != null) TextButton(
                            onClick = { choose(nextEpisode) },
                            colors = ButtonDefaults.textButtonColors(contentColor = WatchText),
                        ) {
                            Icon(Icons.Rounded.SkipNext, null, Modifier.size(18.dp))
                            Spacer(Modifier.width(5.dp))
                            Text(appString(R.string.video_next_episode), maxLines = 1)
                        }
                    }
                }
            }
            if (jumpCue != 0 && !failed) {
                Surface(
                    Modifier.align(if (jumpCue < 0) Alignment.CenterStart else Alignment.CenterEnd)
                        .windowInsetsPadding(safe.only(WindowInsetsSides.Horizontal)).padding(horizontal = 20.dp),
                    shape = CircleShape,
                    color = Color.Black.copy(alpha = .64f),
                ) {
                    Text(if (jumpCue < 0) "−10" else "+10", Modifier.padding(horizontal = 11.dp, vertical = 9.dp),
                        color = WatchText, fontWeight = FontWeight.SemiBold)
                }
            }
            if (buffering && !failed) {
                CircularProgressIndicator(Modifier.align(Alignment.Center).size(34.dp), color = WatchAccent)
            }
            playbackError?.let { message ->
                Column(
                    Modifier.align(Alignment.Center).background(WatchPanel, RoundedCornerShape(16.dp)).padding(16.dp),
                    horizontalAlignment = Alignment.CenterHorizontally,
                ) {
                    Text(appString(message), color = WatchText)
                    Button(onClick = {
                        startAt = maxOf(elapsed, lastSaved)
                        compatible = true
                        attempt++
                    }) { Text(appString(R.string.video_retry)) }
                    TextButton(onClick = ::openExternal) {
                        Text(appString(R.string.photo_ui_external_player), color = WatchMuted)
                    }
                }
            }

            if (showEpisodes || settings) {
                val dismiss: () -> Unit = { showEpisodes = false; settings = false; interaction++ }
                Box(Modifier.matchParentSize().background(Color.Black.copy(alpha = .52f))
                    .clickable(onClickLabel = appString(R.string.action_close), onClick = dismiss))
                Surface(
                    modifier = (if (landscape) Modifier.align(Alignment.CenterEnd).width(side).fillMaxHeight()
                        else Modifier.align(Alignment.BottomCenter).fillMaxWidth().fillMaxHeight(.66f))
                        .windowInsetsPadding(safe.only(WindowInsetsSides.Bottom + WindowInsetsSides.Horizontal + WindowInsetsSides.Top))
                        .padding(if (compact) 6.dp else 10.dp),
                    color = WatchPanel, contentColor = WatchText, shape = RoundedCornerShape(17.dp),
                ) {
                    Column {
                        Row(Modifier.fillMaxWidth().padding(start = 16.dp, end = 4.dp),
                            verticalAlignment = Alignment.CenterVertically) {
                            Text(appString(if (settings) R.string.video_settings else R.string.video_library_episodes),
                                Modifier.weight(1f), style = MaterialTheme.typography.titleMedium)
                            IconButton(onClick = dismiss) { Icon(Icons.Rounded.Close, appString(R.string.action_close)) }
                        }
                        if (settings) {
                            LazyColumn(contentPadding = PaddingValues(start = 14.dp, end = 14.dp, bottom = 12.dp)) {
                                item {
                                    Text(appString(R.string.video_speed), color = WatchMuted)
                                    Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(5.dp)) {
                                        WatchSpeeds.forEach { rate ->
                                            FilterChip(selected = speed == rate, onClick = { speed = rate },
                                                label = { Text("${rate}×") })
                                        }
                                    }
                                    Text(appString(R.string.video_frame), color = WatchMuted)
                                    Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                                        FilterChip(selected = !fill, onClick = { fill = false },
                                            label = { Text(appString(R.string.video_fit)) })
                                        FilterChip(selected = fill, onClick = { fill = true },
                                            label = { Text(appString(R.string.video_fill)) })
                                    }
                                }
                                listOf(C.TRACK_TYPE_AUDIO to R.string.video_audio, C.TRACK_TYPE_TEXT to R.string.video_subtitles)
                                    .forEach { (type, label) ->
                                        val choices = tracks.groups.filter { it.type == type }.flatMap { group ->
                                            (0 until group.length).filter { group.isTrackSupported(it) }.map { group to it }
                                        }
                                        if (choices.isNotEmpty() && (type == C.TRACK_TYPE_TEXT || choices.size > 1)) {
                                            item { Text(appString(label), color = WatchMuted, modifier = Modifier.padding(top = 10.dp)) }
                                            if (type == C.TRACK_TYPE_TEXT) item {
                                                FilterChip(selected = choices.none { (group, idx) -> group.isTrackSelected(idx) },
                                                    onClick = { setTrack(type, null) }, label = { Text(appString(R.string.video_subtitles_off)) })
                                            }
                                            items(choices) { (group, idx) ->
                                                val format = group.getTrackFormat(idx)
                                                val trackLabel = format.label ?: format.language ?: appString(R.string.video_track, choices.indexOf(group to idx) + 1)
                                                FilterChip(selected = group.isTrackSelected(idx), onClick = { setTrack(type, group, idx) },
                                                    label = { Text(trackLabel) })
                                            }
                                        }
                                    }
                                if (episodes.isEmpty()) item {
                                    TextButton(onClick = ::openExternal) {
                                        Icon(Icons.Outlined.OpenInNew, null, Modifier.size(18.dp))
                                        Spacer(Modifier.width(6.dp))
                                        Text(appString(R.string.photo_ui_external_player))
                                    }
                                }
                                if (onDownloadOffline != null) item {
                                    TextButton(onClick = {
                                        player.pause()
                                        settings = false
                                        onDownloadOffline()
                                    }) {
                                        Icon(Icons.Outlined.CloudDownload, null, Modifier.size(18.dp))
                                        Spacer(Modifier.width(6.dp))
                                        Text(appString(R.string.photo_ui_download_offline))
                                    }
                                }
                            }
                        } else {
                            val seasons = episodes.groupBy { it.season }
                            val chosen = seasonFilter ?: current?.season ?: seasons.keys.firstOrNull()
                            if (seasons.size > 1) Row(
                                Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()).padding(horizontal = 12.dp),
                                horizontalArrangement = Arrangement.spacedBy(6.dp),
                            ) {
                                seasons.keys.forEach { season ->
                                    FilterChip(selected = chosen == season, onClick = { seasonFilter = season },
                                        label = { Text(if (season > 0) appString(R.string.video_library_season, season)
                                        else appString(R.string.video_library_episodes)) })
                                }
                            }
                            val videos = seasons[chosen].orEmpty()
                            val active = videos.indexOfFirst { it.source == source && it.path == path }
                            val listState = androidx.compose.foundation.lazy.rememberLazyListState()
                            LaunchedEffect(chosen, active) { if (active >= 0) listState.scrollToItem(active) }
                            LazyColumn(state = listState, contentPadding = PaddingValues(horizontal = 8.dp, vertical = 6.dp)) {
                                items(videos, key = { video -> video.source + "\u0000" + video.path }) { episode ->
                                    val selected = episode.source == source && episode.path == path
                                    Row(
                                        Modifier.fillMaxWidth().background(if (selected) Color(0xFF263A54) else WatchPanel,
                                            RoundedCornerShape(9.dp))
                                            .clickable { if (selected) dismiss() else choose(episode) }
                                            .padding(horizontal = 10.dp, vertical = 9.dp),
                                        verticalAlignment = Alignment.CenterVertically,
                                        horizontalArrangement = Arrangement.spacedBy(10.dp),
                                    ) {
                                        VideoPreview(Api.thumbUrl(episode.source, episode.path, 192), Modifier.width(80.dp).aspectRatio(16f / 9f))
                                        Column(Modifier.weight(1f)) {
                                            Text(episode.name, maxLines = 2, overflow = TextOverflow.Ellipsis)
                                            if (episode.episode > 0) Text(appString(R.string.video_library_episode, episode.episode),
                                                color = WatchMuted, style = MaterialTheme.typography.labelSmall)
                                        }
                                        if (selected) Icon(Icons.Rounded.PlayArrow, null, tint = WatchAccent)
                                    }
                                }
                            }
                        }
                    }
                }
            }

        }
    }
}

@Composable
private fun ImmersiveVideoWindow(keepAwake: Boolean) {
    val view = LocalView.current
    val window = (view.parent as? DialogWindowProvider)?.window ?: return
    DisposableEffect(window, view) {
        val controller = WindowCompat.getInsetsController(window, view)
        window.setBackgroundDrawable(ColorDrawable(android.graphics.Color.BLACK))
        if (Build.VERSION.SDK_INT >= 28) {
            window.attributes = window.attributes.apply {
                layoutInDisplayCutoutMode = if (Build.VERSION.SDK_INT >= 30) {
                    WindowManager.LayoutParams.LAYOUT_IN_DISPLAY_CUTOUT_MODE_ALWAYS
                } else {
                    WindowManager.LayoutParams.LAYOUT_IN_DISPLAY_CUTOUT_MODE_SHORT_EDGES
                }
            }
        }
        window.setLayout(WindowManager.LayoutParams.MATCH_PARENT, WindowManager.LayoutParams.MATCH_PARENT)
        controller.systemBarsBehavior = WindowInsetsControllerCompat.BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE
        controller.hide(WindowInsetsCompat.Type.systemBars())
        onDispose { controller.show(WindowInsetsCompat.Type.systemBars()) }
    }
    DisposableEffect(window, keepAwake) {
        if (keepAwake) window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
        onDispose { if (keepAwake) window.clearFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON) }
    }
}

@Composable
private fun VideoRoundButton(
    icon: androidx.compose.ui.graphics.vector.ImageVector, label: String, action: () -> Unit, large: Boolean = false,
) {
    FilledIconButton(
        onClick = action,
        modifier = Modifier.size(if (large) 64.dp else 48.dp),
        colors = IconButtonDefaults.filledIconButtonColors(
            containerColor = if (large) WatchText else Color.Black.copy(alpha = .6f),
            contentColor = if (large) WatchBackground else WatchText,
        ),
    ) { Icon(icon, label, Modifier.size(if (large) 35.dp else 25.dp)) }
}

private fun videoClock(milliseconds: Long): String {
    val seconds = milliseconds.coerceAtLeast(0) / 1000
    val minutes = seconds / 60
    return if (minutes >= 60) String.format(Locale.ROOT, "%d:%02d:%02d", minutes / 60, minutes % 60, seconds % 60)
    else String.format(Locale.ROOT, "%d:%02d", minutes, seconds % 60)
}
