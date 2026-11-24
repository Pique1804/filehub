package com.filehub.backup.ui

import android.Manifest
import android.content.res.Configuration
import android.os.Build
import androidx.annotation.StringRes
import androidx.activity.compose.BackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.Crossfade
import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.animateDpAsState
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.tween
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.gestures.detectVerticalDragGestures
import androidx.compose.foundation.lazy.grid.LazyGridState
import androidx.compose.animation.slideInVertically
import androidx.compose.animation.slideOutVertically
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.gestures.awaitEachGesture
import androidx.compose.foundation.gestures.awaitFirstDown
import androidx.compose.foundation.gestures.calculateZoom
import androidx.compose.foundation.gestures.scrollBy
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.GridItemSpan
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.lazy.grid.itemsIndexed
import androidx.compose.foundation.lazy.grid.rememberLazyGridState
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.pager.HorizontalPager
import androidx.compose.foundation.pager.rememberPagerState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.CheckCircle
import androidx.compose.material.icons.filled.Favorite
import androidx.compose.material.icons.filled.Star
import androidx.compose.material.icons.outlined.FavoriteBorder
import androidx.compose.material.icons.outlined.StarBorder
import androidx.compose.material.icons.outlined.CloudDone
import androidx.compose.material.icons.outlined.CloudOff
import androidx.compose.material.icons.outlined.CloudQueue
import androidx.compose.material.icons.outlined.CloudSync
import androidx.compose.material.icons.outlined.Crop
import androidx.compose.material.icons.outlined.Delete
import androidx.compose.material.icons.outlined.Download
import androidx.compose.material.icons.outlined.DriveFileMove
import androidx.compose.material.icons.outlined.Info
import androidx.compose.material.icons.outlined.Map
import androidx.compose.material.icons.outlined.RadioButtonUnchecked
import androidx.compose.material.icons.outlined.RotateRight
import androidx.compose.material.icons.outlined.PhotoAlbum
import androidx.compose.material.icons.outlined.Search
import androidx.compose.material.icons.outlined.SelectAll
import androidx.compose.material.icons.rounded.Close
import androidx.compose.material.icons.rounded.PlayCircle
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.lifecycle.repeatOnLifecycle
import androidx.paging.compose.collectAsLazyPagingItems
import androidx.paging.compose.itemKey
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.painter.Painter
import androidx.compose.ui.input.pointer.PointerEventPass
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.input.pointer.positionChange
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import androidx.compose.ui.viewinterop.AndroidView
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.media3.common.MediaItem
import androidx.media3.common.PlaybackException
import androidx.media3.common.Player
import androidx.media3.datasource.okhttp.OkHttpDataSource
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.source.DefaultMediaSourceFactory
import androidx.media3.ui.PlayerView
import androidx.work.WorkInfo
import androidx.work.WorkManager
import coil.compose.AsyncImage
import coil.compose.rememberAsyncImagePainter
import coil.imageLoader
import coil.request.ImageRequest
import com.filehub.backup.*
import com.filehub.backup.R
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONObject
import net.engawapg.lib.zoomable.ScrollGesturePropagation
import net.engawapg.lib.zoomable.rememberZoomState
import net.engawapg.lib.zoomable.zoomable
import java.text.NumberFormat
import java.time.Instant
import java.time.LocalDate
import java.time.YearMonth
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.time.format.FormatStyle
import kotlin.math.roundToInt

// One fixed grid-thumb width for ALL column counts: the server prewarms exactly
// this size, and pinch-zoom no longer refetches at a new size (instant). MUST match
// the server's gridThumbW. 192px is ~3× DPI at ~160px cells — visually identical in a
// grid but 4× cheaper to generate/transfer/decode than 320.
private const val GRID_THUMB = 192
private fun thumbWidthFor(@Suppress("UNUSED_PARAMETER") cols: Int) = GRID_THUMB
private fun pid(p: Photo) = p.source + "|" + p.path

// A memory's modTime carries the capture year.
@Composable
private fun yearsAgoLabel(modTimeSec: Long): String {
    val year = Instant.ofEpochSecond(modTimeSec).atZone(ZoneId.systemDefault()).year
    val count = (java.time.Year.now().value - year).coerceAtLeast(0)
    return if (count == 0) appString(R.string.photo_ui_this_year)
    else appQuantity(R.plurals.photo_ui_years_ago, count)
}

@OptIn(ExperimentalFoundationApi::class, ExperimentalMaterial3Api::class)
@Composable
fun PhotosScreen() {
    val ctx = LocalContext.current
    val resources = LocalAppResources.current
    val locale = resources.configuration.locales[0]
    val today = LocalDate.now()
    val todayLabel = appString(R.string.photo_ui_today)
    val yesterdayLabel = appString(R.string.photo_ui_yesterday)
    val sectionLabel: (PhotoTimeline.Section) -> String = remember(locale, today, todayLabel, yesterdayLabel) {
        val monthFormat = DateTimeFormatter.ofPattern("LLLL yyyy", locale)
        val dayFormat = DateTimeFormatter.ofLocalizedDate(FormatStyle.LONG).withLocale(locale)
        val todayKey = today.toString()
        val yesterdayKey = today.minusDays(1).toString()
        // Translate labels from the ISO group keys. The paging model and row identities stay unchanged.
        return@remember { section: PhotoTimeline.Section ->
            val key = section.key
            when {
                key == todayKey -> todayLabel
                key == yesterdayKey -> yesterdayLabel
                key.indexOf('-', 1) == key.lastIndexOf('-') ->
                    YearMonth.parse(key).format(monthFormat).replaceFirstChar { it.titlecase(locale) }
                else -> LocalDate.parse(key).format(dayFormat)
            }
        }
    }
    val prefs = remember { Prefs(ctx) }
    val scope = rememberCoroutineScope()
    var storagePolicy by remember(prefs.accountScope()) { mutableStateOf<JSONObject?>(null) }
    LaunchedEffect(prefs.accountScope()) {
        storagePolicy = withContext(Dispatchers.IO) { runCatching { Api.profileStorage() }.getOrNull() }
    }
    fun canModify(photo: Photo): Boolean {
        val policy = storagePolicy ?: return true
        if (!policy.optBoolean("canWrite", true)) return false
        if (!policy.optBoolean("restricted")) return true
        val personal = policy.optJSONObject("personal") ?: return false
        return personal.optString("source") == photo.source && photo.path.startsWith(personal.optString("path") + "/")
    }
    val model:PhotoLibraryModel=viewModel(key="photos:${prefs.accountScope()}")
    val local:LocalMediaStats=viewModel(key="local:${prefs.accountScope()}")
    val gallery by model.gallery.collectAsStateWithLifecycle()
    val grouped=gallery.timeline
    val paged=gallery.pages.collectAsLazyPagingItems()
    val loading by model.loading.collectAsStateWithLifecycle()
    val libraryCounts by model.counts.collectAsStateWithLifecycle()
    val libTotal=libraryCounts.first
    val libVideos=libraryCounts.second
    val libImages=libTotal-libVideos
    val favSet by model.favorites.collectAsStateWithLifecycle()
    val memories by model.memories.collectAsStateWithLifecycle()
    val mediaCounts by local.counts.collectAsStateWithLifecycle()
    val mediaTotal=mediaCounts.first
    val backedUpCount=mediaCounts.second
    val cols=rememberSaveable {mutableIntStateOf(prefs.gridCols)}
    var pendingAnchor by remember {mutableStateOf<String?>(null)}
    var searchResults by remember { mutableStateOf<List<Photo>?>(null) }
    var ocrHitCount by remember { mutableIntStateOf(0) }
    var searchIntent by remember { mutableStateOf<Api.FileSearchIntent?>(null) }
    var searchTotal by remember { mutableIntStateOf(0) }
    var searchCursor by remember { mutableStateOf("") }
    var searchPageJob by remember { mutableStateOf<Job?>(null) }
    var searchPageError by remember { mutableStateOf(false) }
    var searchBusy by remember { mutableStateOf(false) }
    var textSearchUnavailable by remember { mutableStateOf(false) }
    var sceneSearchUnavailable by remember { mutableStateOf(false) }
    var visualSearch by remember { mutableStateOf<Api.VisualPhotoSearch?>(null) }
    var query by rememberSaveable { mutableStateOf("") }
    var viewerIndex by remember { mutableStateOf(-1) }
    var auto by remember { mutableStateOf(prefs.auto) }
    var online by remember { mutableStateOf(false) }
    var showCleanup by remember { mutableStateOf(false) }
    var showSync by remember { mutableStateOf(false) }
    var showMap by remember { mutableStateOf(false) }
    var showAlbums by remember { mutableStateOf(false) }
    var showDatePicker by remember { mutableStateOf(false) }
    var pickAlbumFor by remember { mutableStateOf<List<Photo>?>(null) } // photos to add to a chosen album
    var favFilter by rememberSaveable { mutableStateOf(false) }
    var viewerPhotos by remember { mutableStateOf<List<Photo>>(emptyList()) }
    var infoPhoto by remember { mutableStateOf<Photo?>(null) }
    var cropPhoto by remember { mutableStateOf<Photo?>(null) }
    var storiesIndex by remember { mutableStateOf(-1) } // Instagram-style memories viewer
    val sel = rememberSelection()
    // status for the header pill — same data the old cloud icon reflected
    val workInfos by Backup.workInfos(ctx).collectAsStateWithLifecycle(emptyList())
    val backupInfo = activeBackup(workInfos)
    val syncRunning = backupInfo?.state == WorkInfo.State.RUNNING
    val lifecycle=LocalLifecycleOwner.current.lifecycle
    DisposableEffect(lifecycle,local) {
        val observer=LifecycleEventObserver {_,event->local.visible(lifecycle.currentState.isAtLeast(Lifecycle.State.RESUMED));if(event==Lifecycle.Event.ON_RESUME)local.refresh(force=true)}
        lifecycle.addObserver(observer);local.visible(lifecycle.currentState.isAtLeast(Lifecycle.State.RESUMED))
        onDispose {lifecycle.removeObserver(observer);local.visible(false)}
    }
    LaunchedEffect(lifecycle) {
        lifecycle.repeatOnLifecycle(Lifecycle.State.RESUMED) {
            while(true){online=withContext(Dispatchers.IO){Api.isOnline()};delay(30_000)}
        }
    }
    LaunchedEffect(lifecycle) {
        lifecycle.repeatOnLifecycle(Lifecycle.State.RESUMED) {
            while (true) {
                val status = Api.visualPhotoSearchStatus()
                if (status != null) visualSearch = status
                delay(15_000)
            }
        }
    }
    LaunchedEffect(cols.intValue,favFilter){model.filter(cols.intValue>=6,favFilter)}

    val notifications=rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { }
    fun requestNotifications() {
        if(Build.VERSION.SDK_INT>=33 && androidx.core.content.ContextCompat.checkSelfPermission(ctx,Manifest.permission.POST_NOTIFICATIONS)!=android.content.pm.PackageManager.PERMISSION_GRANTED)
            notifications.launch(Manifest.permission.POST_NOTIFICATIONS)
    }
    val perm = rememberLauncherForActivityResult(ActivityResultContracts.RequestMultiplePermissions()) { res ->
        if (res.values.any { it }) { auto = true; prefs.auto = true; Backup.setAuto(ctx, true); Backup.runNow(ctx); requestNotifications() }
    }
    fun mediaPerms() = if (Build.VERSION.SDK_INT >= 34)
        arrayOf(Manifest.permission.READ_MEDIA_IMAGES, Manifest.permission.READ_MEDIA_VIDEO, Manifest.permission.READ_MEDIA_VISUAL_USER_SELECTED)
    else if (Build.VERSION.SDK_INT >= 33)
        arrayOf(Manifest.permission.READ_MEDIA_IMAGES, Manifest.permission.READ_MEDIA_VIDEO)
    else arrayOf(Manifest.permission.READ_EXTERNAL_STORAGE)

    fun reload()=model.refresh()
    LaunchedEffect(workInfos){if(model.backupChanged(workInfos))local.refresh(force=true)}
    LaunchedEffect(model){model.start()}
    fun toggleFav(p:Photo)=model.toggleFavorite(p)
    fun rotatePhoto(p: Photo) {
        scope.launch {
            val ok = withContext(Dispatchers.IO) { Api.rotate(p.source, p.path, 90) }
            if (ok) {
                withContext(Dispatchers.IO) {
                    val il = ctx.imageLoader
                    il.memoryCache?.clear() // decoded thumbs reload from disk on scroll (fast)
                    il.diskCache?.remove("${prefs.accountScope()}|${pid(p)}@${GRID_THUMB}v2")
                    il.diskCache?.remove(Api.photoThumbUrl(p,1280))
                }
                viewerIndex = -1; viewerPhotos=emptyList(); reload()
                android.widget.Toast.makeText(ctx, resources.getString(R.string.photo_ui_rotated), android.widget.Toast.LENGTH_SHORT).show()
            } else android.widget.Toast.makeText(ctx, resources.getString(R.string.photo_ui_rotate_failed), android.widget.Toast.LENGTH_SHORT).show()
        }
    }
    LaunchedEffect(query, visualSearch) {
        searchPageJob?.cancel()
        searchPageJob = null
        searchPageError = false
        searchIntent = null
        searchCursor = ""
        searchTotal = 0
        if (query.isBlank()) {
            searchResults = null; searchBusy = false
            ocrHitCount = 0
            textSearchUnavailable = false; sceneSearchUnavailable = false
            return@LaunchedEffect
        }
        searchResults = emptyList(); searchBusy = true
        ocrHitCount = 0
        textSearchUnavailable = false; sceneSearchUnavailable = false
        // Keep OCR hits first and semantic hits in server relevance order. Search
        // uses its own ungrouped grid; the timeline's date grouping needs sorting.
        delay(300)
        val requested = query
        val textResult = withContext(Dispatchers.IO) { Api.searchPhotosText(requested) }
        searchResults = textResult.photos.distinctBy(::pid)
        textSearchUnavailable = !textResult.available
        searchIntent = textResult.intent
        searchCursor = textResult.nextCursor
        searchTotal = textResult.total
        if (textResult.intent == null && textResult.available) {
            ocrHitCount = searchResults.orEmpty().size
            if (visualSearch == null || visualSearch?.available == true) {
                val sceneResult = withContext(Dispatchers.IO) { Api.searchSemantic(requested) }
                searchResults = (textResult.photos + sceneResult.photos).distinctBy(::pid)
                sceneSearchUnavailable = !sceneResult.available
            } else {
                sceneSearchUnavailable = visualSearch?.enabled == true
            }
        }
        searchBusy = false
    }
    fun loadMoreSearchResults() {
        val cursor = searchCursor
        if (cursor.isEmpty() || searchPageJob != null) return
        val requested = query
        searchPageJob = scope.launch {
            searchPageError = false
            val page = withContext(Dispatchers.IO) { Api.searchPhotosText(requested, cursor) }
            if (query == requested && searchCursor == cursor) {
                if (page.available && page.intent == searchIntent) {
                    searchResults = (searchResults.orEmpty() + page.photos).distinctBy(::pid)
                    searchCursor = page.nextCursor
                    searchTotal = page.total
                } else {
                    searchPageError = true
                }
            }
            searchPageJob = null
        }
    }
    val photos=searchResults.orEmpty()
    val visibleCount=if(searchResults!=null)photos.size else grouped.photoCount
    val gridState = rememberLazyGridState()
    var contextPhotoId by remember { mutableStateOf<String?>(null) }
    LaunchedEffect(query) {
        contextPhotoId = null
        if (query.isNotBlank()) gridState.scrollToItem(0)
    }
    LaunchedEffect(gridState.isScrollInProgress) {
        if (gridState.isScrollInProgress) contextPhotoId = null
    }
    BackHandler(enabled = sel.inSelection) { sel.clear() }

    // batch actions
    suspend fun selectedPhotos(ids:Set<String>) = if(searchResults!=null)photos.filter {pid(it) in ids} else model.selected(ids)
    fun openPhoto(p:Photo) {scope.launch {
        val items=model.all()
        val index=items.indexOfFirst {pid(it)==pid(p)}
        if(index>=0){viewerPhotos=items;viewerIndex=index}
    }}
    LaunchedEffect(grouped) {
        pendingAnchor?.let {key->
            val section=grouped.sections.indexOfFirst {it.key<=key}.coerceAtLeast(0)
            grouped.sections.getOrNull(section)?.let {gridState.scrollToItem(it.itemStart+(if(memories.isNotEmpty()&&!favFilter)1 else 0)+(if(libTotal>0)1 else 0))}
            pendingAnchor=null
        }
    }
    fun downloadPhotos(ids: Set<String>) {
        scope.launch {val items=selectedPhotos(ids); withContext(Dispatchers.IO) { for (p in items) Downloader.toDevice(ctx, p.source, p.path, p.name) } }
    }
    var pendingDelete by remember { mutableStateOf(emptyList<Photo>()) }
    var pendingMove by remember { mutableStateOf(emptyList<Photo>()) }
    var movingPhotos by remember { mutableStateOf(false) }
    fun photosChanged(removed: List<Photo>) {
        if (removed.isEmpty()) return
        val ids = removed.mapTo(mutableSetOf(), ::pid)
        searchResults = searchResults?.filterNot { pid(it) in ids }
        reload()
    }
    fun preparePhotoAction(ids: Set<String>, move: Boolean) {
        scope.launch {
            val items = selectedPhotos(ids)
            if (items.isEmpty()) return@launch
            if (items.any { !canModify(it) }) {
                android.widget.Toast.makeText(ctx, resources.getString(R.string.photo_edit_access_denied), android.widget.Toast.LENGTH_SHORT).show()
                return@launch
            }
            if (move) pendingMove = items else pendingDelete = items
        }
    }
    fun deletePhotos(items: List<Photo>) {
        scope.launch {
            val deleted = withContext(Dispatchers.IO) { items.filter { Api.delete(it.source, it.path) } }
            photosChanged(deleted)
            val failed = items.size - deleted.size
            android.widget.Toast.makeText(ctx, if (failed == 0) resources.getString(R.string.photo_ui_moved_to_trash) else resources.getString(R.string.photo_ui_delete_failed_count, failed, items.size), android.widget.Toast.LENGTH_LONG).show()
        }
    }
    val galleryCell: @Composable (Photo, Int, () -> Unit) -> Unit = { photo, thumbWidth, onOpen ->
        val id = pid(photo)
        PhotoCell(photo, selected = id in sel.ids, inSelection = sel.inSelection, thumbW = thumbWidth,
            onClick = { if (sel.inSelection) sel.toggle(id) else onOpen() },
            onLongClick = { if (sel.inSelection) sel.toggle(id) else contextPhotoId = id },
        ) {
            if (contextPhotoId == id) {
                PhotoContextMenu(
                    onDismiss = { contextPhotoId = null },
                    onDownload = { downloadPhotos(setOf(id)) },
                    onAlbum = { pickAlbumFor = listOf(photo) },
                    onSelect = { sel.toggle(id) },
                    canModify = canModify(photo),
                    onMove = { pendingMove = listOf(photo) },
                    onDelete = { pendingDelete = listOf(photo) },
                )
            }
        }
    }

    Box(Modifier.fillMaxSize().background(MaterialTheme.colorScheme.background)) {
        Column(Modifier.fillMaxSize()) {
            Crossfade(sel.inSelection, label = "topbar") { selecting ->
                if (selecting) {
                    Row(Modifier.fillMaxWidth().padding(horizontal = 18.dp, vertical = 10.dp), verticalAlignment = Alignment.CenterVertically) {
                        FhRoundButton(onClick = { sel.clear() }) { Icon(Icons.Rounded.Close, appString(R.string.action_cancel), Modifier.size(18.dp), tint = Fh.Text2) }
                        Spacer(Modifier.width(12.dp))
                        Text("${sel.count}", fontSize = 20.sp, fontWeight = FontWeight.Bold, color = Fh.Text)
                        Spacer(Modifier.weight(1f))
                        FhRoundButton(onClick = { scope.launch {sel.selectAll(if(searchResults!=null)photos.map(::pid) else model.ids())} }) { Icon(Icons.Outlined.SelectAll, appString(R.string.photo_ui_select_all), Modifier.size(18.dp), tint = Fh.Text2) }
                    }
                } else {
                    Column(Modifier.fillMaxWidth().padding(horizontal = 18.dp)) {
                        Row(Modifier.fillMaxWidth().padding(top = 12.dp), verticalAlignment = Alignment.CenterVertically) {
                            Text(appString(R.string.nav_photos), Modifier.weight(1f), fontSize = 26.sp, fontWeight = FontWeight.Bold, color = Fh.Text, letterSpacing = (-0.5).sp, maxLines = 2)
                            // status pill = the old cloud button: shows backup state, opens the sync sheet
                            Surface(onClick = { showSync = true }, shape = Fh.TileShape, color = Fh.Card) {
                                Row(Modifier.heightIn(min = 48.dp).padding(horizontal = 10.dp, vertical = 7.dp), verticalAlignment = Alignment.CenterVertically) {
                                    val dot = when { syncRunning -> Fh.Accent; online -> Fh.Green; else -> Fh.Track }
                                    Box(Modifier.size(8.dp).background(dot, CircleShape))
                                    Spacer(Modifier.width(7.dp))
                                    Text(when { syncRunning -> appString(R.string.photo_ui_uploading); online -> appString(R.string.photo_ui_online); else -> appString(R.string.backup_offline) },
                                        fontSize = 12.5.sp, fontWeight = FontWeight.SemiBold, color = Fh.Text2)
                                }
                            }
                        }
                        Spacer(Modifier.height(12.dp))
                        OutlinedTextField(
                            value = query, onValueChange = { query = it },
                            modifier = Modifier.fillMaxWidth(),
                            placeholder = { Text(appString(R.string.photo_ui_search), maxLines = 1, fontSize = 14.sp, color = Fh.Muted) },
                            leadingIcon = { Icon(Icons.Outlined.Search, null, Modifier.size(18.dp), tint = Fh.Muted) },
                            trailingIcon = { if (query.isNotEmpty()) IconButton({ query = "" }) { Icon(Icons.Rounded.Close, appString(R.string.photo_ui_clear_search), tint = Fh.Muted) } },
                            singleLine = true, shape = RoundedCornerShape(16.dp),
                            colors = OutlinedTextFieldDefaults.colors(
                                focusedContainerColor = Fh.Card, unfocusedContainerColor = Fh.Card,
                                focusedBorderColor = Fh.Accent, unfocusedBorderColor = Fh.Border,
                            )
                        )
                        searchIntent?.let { intent ->
                            val filters = listOfNotNull(
                                when (intent.type) {
                                    "image" -> appString(R.string.file_search_images)
                                    "video" -> appString(R.string.file_search_videos)
                                    else -> null
                                },
                                if (intent.year != 0) appString(R.string.photo_ui_capture_year, intent.year) else null,
                            )
                            Text(filters.joinToString(" · "), Modifier.padding(top = 6.dp), fontSize = 12.sp, color = Fh.Muted)
                        }
                        if (query.isNotBlank() && searchIntent == null && visualSearch?.available == true && !sceneSearchUnavailable) {
                            Text(appString(R.string.photo_ui_scene_search_ready), Modifier.padding(top = 6.dp),
                                fontSize = 12.sp, color = Fh.Muted)
                        }
                        if (query.isNotBlank() && (sceneSearchUnavailable || textSearchUnavailable)) {
                            Text(
                                when {
                                    sceneSearchUnavailable && textSearchUnavailable -> appString(R.string.photo_ui_search_unavailable)
                                    sceneSearchUnavailable -> appString(R.string.photo_ui_scene_search_unavailable)
                                    else -> appString(R.string.photo_ui_text_search_unavailable)
                                },
                                Modifier.padding(top = 6.dp), fontSize = 12.sp, color = Fh.Muted
                            )
                        }
                        Spacer(Modifier.height(10.dp))
                        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                            TextButton(onClick = { showDatePicker = true }, enabled = grouped.sections.isNotEmpty()) { Text(appString(R.string.photo_ui_jump_to_date)) }
                            FhRoundButton(onClick = { showAlbums = true }) { Icon(Icons.Outlined.PhotoAlbum, appString(R.string.photo_ui_albums), Modifier.size(18.dp), tint = Fh.Text2) }
                            FhRoundButton(onClick = { showMap = true }) { Icon(Icons.Outlined.Map, appString(R.string.photo_ui_places), Modifier.size(18.dp), tint = Fh.Text2) }
                            FhRoundButton(onClick = { favFilter = !favFilter }) {
                                Icon(if (favFilter) Icons.Filled.Star else Icons.Outlined.StarBorder, appString(R.string.photo_ui_favorites),
                                    Modifier.size(18.dp), tint = if (favFilter) Fh.Accent else Fh.Text2)
                            }
                        }
                        Spacer(Modifier.height(4.dp))
                    }
                }
            }

            backupInfo?.let { SyncProgressBanner(it, onClick = { showSync = true }) }

            if(paged.loadState.refresh is androidx.paging.LoadState.Error && searchResults==null) {
                TextButton(onClick={paged.retry()}){Text(appString(R.string.photo_ui_cache_failed_retry))}
            }
            if ((loading || searchBusy) && visibleCount==0) LinearProgressIndicator(Modifier.fillMaxWidth())

            if (!loading && visibleCount==0) {
                FhEmptyState(Icons.Outlined.PhotoAlbum,
                    title = when { !online -> appString(R.string.photo_ui_server_unreachable); query.isNotBlank() -> appString(R.string.photo_ui_no_result); else -> appString(R.string.photo_ui_empty_library) },
                    description = when {
                        !online -> appString(R.string.photo_ui_connect_or_offline)
                        searchBusy -> appString(R.string.photo_ui_searching)
                        searchIntent != null -> appString(R.string.photo_ui_filters_empty)
                        query.isNotBlank() && sceneSearchUnavailable -> appString(R.string.photo_ui_no_text_result)
                        query.isNotBlank() -> appString(R.string.photo_ui_search_hint)
                        else -> appString(R.string.photo_ui_empty_library_hint)
                    }, modifier = Modifier.fillMaxSize(),
                    actionLabel = if (!online) appString(R.string.photo_ui_retry) else null,
                    onAction = if (!online) ({ reload() }) else null)

            } else {
                val effectiveCols = cols.intValue * maxOf(1, LocalConfiguration.current.screenWidthDp / 600)
                val groupedState=rememberUpdatedState(grouped)
                val thumbW = thumbWidthFor(cols.value)

                Box(Modifier.fillMaxSize().pointerInput(Unit) {
                    // pinch on the Initial pass: parent claims 2-finger gestures before the
                    // grid's scroll consumes them; single-finger drags fall through to scroll.
                    awaitEachGesture {
                        awaitFirstDown(requireUnconsumed = false, pass = PointerEventPass.Initial)
                        val startCols = cols.value
                        var acc = 1f
                        while (true) {
                            val event = awaitPointerEvent(PointerEventPass.Initial)
                            if (event.changes.size < 2) { // single-finger scroll: skip zoom math entirely
                                if (event.changes.none { it.pressed }) break
                                continue
                            }
                            if (event.changes.none { it.pressed }) break
                            if (event.changes.count { it.pressed } >= 2) {
                                acc *= event.calculateZoom()
                                val target = (startCols / acc).roundToInt().coerceIn(2, 6)
                                if (target != cols.value) {
                                    val leading=(if(memories.isNotEmpty()&&!favFilter)1 else 0)+(if(libTotal>0)1 else 0)
                                    val item=gridState.layoutInfo.visibleItemsInfo.firstOrNull {it.contentType=="photo"}?.index
                                    pendingAnchor=if((cols.intValue>=6)!=(target>=6))item?.let {groupedState.value.sectionAt((it-leading).coerceAtLeast(0))?.key}?.let {if(target>=6)it.take(7) else it.take(7)+"-31"} else null
                                    cols.intValue=target; prefs.gridCols=target
                                }
                                event.changes.forEach { it.consume() }
                            }
                        }
                    }
                }) {
                    LazyVerticalGrid(
                        columns = GridCells.Fixed(effectiveCols), state = gridState, modifier = Modifier.fillMaxSize(),
                        contentPadding = PaddingValues(start = 18.dp, end = 18.dp, bottom = 24.dp),
                        horizontalArrangement = Arrangement.spacedBy(6.dp), verticalArrangement = Arrangement.spacedBy(6.dp)
                    ) {
                        if (memories.isNotEmpty() && searchResults == null && !favFilter) {
                            item(span = { GridItemSpan(maxLineSpan) }, key = "memories", contentType = "memories") {
                                Column(Modifier.fillMaxWidth().padding(top = 8.dp)) {
                                    Row(Modifier.fillMaxWidth().padding(bottom = 10.dp), verticalAlignment = Alignment.CenterVertically) {
                                        Text(appString(R.string.photo_ui_memories), fontSize = 16.5.sp, fontWeight = FontWeight.Bold, color = Fh.Text)
                                        Spacer(Modifier.weight(1f))
                                        Text(appString(R.string.photo_ui_on_this_day), fontSize = 12.5.sp, color = Fh.Muted)
                                    }
                                    LazyRow(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                                        items(memories, key = { "m_" + pid(it) }) { mp ->
                                            Box(Modifier.size(118.dp, 158.dp).clip(RoundedCornerShape(16.dp)).background(Fh.BgAlt)
                                                .combinedClickableSimple { storiesIndex = memories.indexOf(mp).coerceAtLeast(0) }) {
                                                AsyncImage(
                                                    ImageRequest.Builder(ctx).data(Api.photoThumbUrl(mp, GRID_THUMB)).size(GRID_THUMB)
                                                        .memoryCacheKey(prefs.accountScope() + "|" + pid(mp) + "|"+mp.revision+"@" + GRID_THUMB + "v2")
                                                        .diskCacheKey(prefs.accountScope() + "|" + pid(mp) + "|"+mp.revision+"@" + GRID_THUMB + "v2").build(),
                                                    contentDescription = null, contentScale = ContentScale.Crop, modifier = Modifier.matchParentSize())
                                                // Keep the capture age legible on the thumbnail.
                                                Box(Modifier.matchParentSize().background(
                                                    androidx.compose.ui.graphics.Brush.verticalGradient(
                                                        0.45f to Color.Transparent, 1f to Color(0x8C080C18))))
                                                Text(yearsAgoLabel(mp.modTime), Modifier.align(Alignment.BottomStart).padding(start = 12.dp, bottom = 11.dp),
                                                    color = Color.White, fontSize = 12.5.sp, fontWeight = FontWeight.SemiBold)
                                                if (mp.isVideo) Icon(Icons.Rounded.PlayCircle, null, Modifier.align(Alignment.TopEnd).padding(6.dp).size(18.dp), tint = Color.White)
                                            }
                                        }
                                    }
                                    Spacer(Modifier.height(2.dp))
                                }
                            }
                        }
                        if (searchResults == null && (libImages + libVideos) > 0) {
                            item(span = { GridItemSpan(maxLineSpan) }, key = "lib_count", contentType = "count") {
                                val imageCount = appQuantity(R.plurals.photo_ui_image_count, libImages)
                                val txt = if (libVideos > 0) {
                                    "$imageCount · ${appQuantity(R.plurals.photo_ui_video_count, libVideos)}"
                                } else imageCount
                                Text(txt, Modifier.fillMaxWidth().padding(top = 14.dp, bottom = 2.dp),
                                    fontSize = 13.sp, color = Fh.Muted)
                            }
                        }
                        if (searchResults != null) {
                            item(span = { GridItemSpan(maxLineSpan) }, key = "search_count", contentType = "count") {
                                Text(appString(R.string.photo_ui_results_count, if (searchIntent == null) photos.size else searchTotal),
                                    Modifier.fillMaxWidth().padding(top = 14.dp, bottom = 6.dp),
                                    fontSize = 12.5.sp, color = Fh.Muted)
                            }
                            if (searchIntent != null) {
                                itemsIndexed(photos, key = { _, p -> pid(p) }, contentType = { _, _ -> "photo" }) { i, p ->
                                    galleryCell(p, thumbW) { viewerPhotos = photos; viewerIndex = i }
                                }
                                if (searchCursor.isNotEmpty()) {
                                    item(span = { GridItemSpan(maxLineSpan) }, key = "search_more") {
                                        Box(Modifier.fillMaxWidth(), contentAlignment = Alignment.Center) {
                                            TextButton(onClick = ::loadMoreSearchResults, enabled = searchPageJob == null) {
                                                val label = when {
                                                    searchPageJob != null -> R.string.photo_ui_searching
                                                    searchPageError -> R.string.photo_ui_retry
                                                    else -> R.string.photo_ui_load_more
                                                }
                                                Text(appString(label))
                                            }
                                        }
                                    }
                                }
                            }
                            if (searchIntent == null && ocrHitCount > 0) {
                                item(span = { GridItemSpan(maxLineSpan) }, key = "ocr_header", contentType = "header") {
                                    Text(appString(R.string.photo_ui_text_results, ocrHitCount), Modifier.fillMaxWidth().padding(top = 12.dp, bottom = 6.dp),
                                        fontSize = 16.sp, fontWeight = FontWeight.Bold, color = Fh.Text)
                                }
                                itemsIndexed(photos.take(ocrHitCount), key = { _, p -> pid(p) }, contentType = { _, _ -> "photo" }) { i, p ->
                                    galleryCell(p, thumbW) { viewerPhotos = photos; viewerIndex = i }
                                }
                            }
                            if (searchIntent == null && photos.size > ocrHitCount) {
                                item(span = { GridItemSpan(maxLineSpan) }, key = "scene_header", contentType = "header") {
                                    Text(appString(R.string.photo_ui_scene_results, photos.size - ocrHitCount), Modifier.fillMaxWidth().padding(top = 18.dp, bottom = 6.dp),
                                        fontSize = 16.sp, fontWeight = FontWeight.Bold, color = Fh.Text)
                                }
                                itemsIndexed(photos.drop(ocrHitCount), key = { _, p -> pid(p) }, contentType = { _, _ -> "photo" }) { i, p ->
                                    galleryCell(p, thumbW) { viewerPhotos = photos; viewerIndex = ocrHitCount + i }
                                }
                            }
                        } else {
                            items(count=paged.itemCount,key=paged.itemKey {row->when(row){is TimelineRow.Header->"h_${row.section.key}";is TimelineRow.Image->pid(row.photo)}},
                                span={i->if(grouped.headerAt(i)!=null)GridItemSpan(maxLineSpan) else GridItemSpan(1)},
                                contentType={i->if(grouped.headerAt(i)!=null)"header" else "photo"}) {i->
                                val row=paged[i]
                                val header=grouped.headerAt(i)
                                if(header!=null)Text(sectionLabel(header),Modifier.fillMaxWidth().padding(top=18.dp,bottom=6.dp),fontSize=17.sp,fontWeight=FontWeight.Bold,color=Fh.Text)
                                else if(row is TimelineRow.Image) {
                                    val p = row.photo
                                    galleryCell(p, thumbW) { openPhoto(p) }
                                } else Box(Modifier.aspectRatio(1f).background(Fh.Card))
                            }
                        }
                    }
                    if (searchResults == null) {
                        val leadingItems = (if (memories.isNotEmpty() && !favFilter) 1 else 0) +
                            (if ((libImages + libVideos) > 0) 1 else 0)
                        DateScrubber(gridState, grouped, leadingItems, sectionLabel)
                    }
                }
            }
    }

    if (showDatePicker) {
        PhotoCalendarDialog(
            confirmLabel = R.string.photo_ui_go_to_date,
            onDismiss = { showDatePicker = false },
            onConfirm = { millis ->
                showDatePicker = false
                if (millis != null && grouped.sections.isNotEmpty()) {
                    val date = Instant.ofEpochMilli(millis).atZone(java.time.ZoneOffset.UTC).toLocalDate()
                    val monthMode = prefs.gridCols >= 6
                    val key = if (monthMode) YearMonth.from(date).toString() else date.toString()
                    val section = grouped.sections.indexOfFirst { it.key <= key }.let { if (it < 0) grouped.sections.lastIndex else it }
                    val leading = (if (memories.isNotEmpty() && searchResults == null && !favFilter) 1 else 0) +
                        (if (searchResults == null && (libImages + libVideos) > 0) 1 else 0)
                    scope.launch { gridState.scrollToItem(grouped.headerPositions[section].first + leading) }
                }
            }
        )
    }

    // contextual bottom action bar — floats above the pill nav
        AnimatedVisibility(sel.inSelection, modifier = Modifier.align(Alignment.BottomCenter),
            enter = slideInVertically { it }, exit = slideOutVertically { it }) {
            FhCard(Modifier.padding(start = 18.dp, end = 18.dp, bottom = 96.dp)) {
                Row(Modifier.fillMaxWidth().padding(vertical = 8.dp), horizontalArrangement = Arrangement.SpaceEvenly) {
                    BarAction(Icons.Outlined.Download, appString(R.string.photo_ui_download), Modifier.weight(1f)) {
                        val ids = sel.ids.toSet()
                        sel.clear()
                        downloadPhotos(ids)
                    }
                    BarAction(Icons.Outlined.PhotoAlbum, appString(R.string.photo_ui_to_album), Modifier.weight(1f)) { val ids=sel.ids.toSet(); sel.clear(); scope.launch {pickAlbumFor=selectedPhotos(ids)} }
                    if (storagePolicy?.optBoolean("canWrite", true) != false) {
                        BarAction(Icons.Outlined.DriveFileMove, appString(R.string.file_ui_move), Modifier.weight(1f)) {
                            if (!movingPhotos) preparePhotoAction(sel.ids.toSet(), move = true)
                        }
                        BarAction(Icons.Outlined.Delete, appString(R.string.photo_ui_delete), Modifier.weight(1f), tint = MaterialTheme.colorScheme.error) {
                            preparePhotoAction(sel.ids.toSet(), move = false)
                        }
                    }
                }
            }
        }
    }

    if (pendingMove.isNotEmpty()) FolderPickerDialog(onDismiss = { pendingMove = emptyList() }, onPick = { source, folder ->
        val items = pendingMove
        pendingMove = emptyList()
        movingPhotos = true
        sel.clear()
        scope.launch {
            try {
                val moved = withContext(Dispatchers.IO) { items.filter { Api.move(it.source, it.path, source, folder) } }
                photosChanged(moved)
                android.widget.Toast.makeText(ctx, resources.getString(R.string.file_ui_moved_count, moved.size, items.size),
                    android.widget.Toast.LENGTH_LONG).show()
            } finally {
                movingPhotos = false
            }
        }
    })

    if (pendingDelete.isNotEmpty()) AlertDialog(onDismissRequest = { pendingDelete = emptyList() },
        title = { Text(appString(R.string.photo_ui_delete_count, pendingDelete.size)) }, text = { Text(appString(R.string.photo_ui_move_to_trash_message)) },
        confirmButton = { TextButton(onClick = {
            val items = pendingDelete
            pendingDelete = emptyList()
            sel.clear()
            deletePhotos(items)
        }) { Text(appString(R.string.photo_ui_delete)) } },
        dismissButton = { TextButton(onClick = { pendingDelete = emptyList() }) { Text(appString(R.string.action_cancel)) } })

    if (showSync) ModalBottomSheet(onDismissRequest = { showSync = false }, containerColor = Fh.Card) {
        Text(appString(R.string.photo_ui_sync_title), Modifier.padding(start = 20.dp, top = 4.dp, bottom = 4.dp), style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.SemiBold)
        SyncCard(prefs, mediaTotal, backedUpCount, auto,
            onToggle = { on -> if (on) perm.launch(mediaPerms()) else { auto = false; prefs.auto = false; Backup.setAuto(ctx, false) } },
            onFreeSpace = { showSync = false; showCleanup = true })
        Spacer(Modifier.navigationBarsPadding().height(16.dp))
    }

    if (showCleanup) CleanupBackupDialog(onClose = { showCleanup = false }, onDeleted = {
        android.widget.Toast.makeText(ctx, resources.getString(R.string.cleanup_finished), android.widget.Toast.LENGTH_LONG).show()
        local.refresh(force = true)
    })

    if (showMap) MapScreen(onOpenPhotos = { list, idx -> viewerPhotos = list; viewerIndex = idx }, onClose = { showMap = false })

    if (viewerIndex >= 0) {
        val vp = if (viewerPhotos.isNotEmpty()) viewerPhotos else photos
        PhotoViewer(vp, viewerIndex, isFav = { pid(it) in favSet }, onToggleFav = { toggleFav(it) }, onInfo = { infoPhoto = it },
            canTrash = ::canModify, onTrash = { photo -> viewerIndex = -1; viewerPhotos = emptyList(); pendingDelete = listOf(photo) },
            onRotate = { rotatePhoto(it) }, onCrop = { cropPhoto = it; viewerIndex = -1; viewerPhotos=emptyList() }) { viewerIndex = -1; viewerPhotos=emptyList() }
    }
    infoPhoto?.let { p -> InfoDialog(p.source,p.path,onChanged={viewerIndex=-1;reload()}) {infoPhoto=null} }
    cropPhoto?.let { p ->
        CropScreen(p.source, p.path, onCropped = { cropPhoto = null; reload() }) { cropPhoto = null }
    }

    if (storiesIndex >= 0 && memories.isNotEmpty())
        StoriesViewer(memories, storiesIndex, onOpenFull = { p -> storiesIndex = -1; viewerPhotos = memories; viewerIndex = memories.indexOf(p) }) { storiesIndex = -1 }


    if (showAlbums) AlbumsScreen(
        onOpenPhotos = { list, idx -> viewerPhotos = list; viewerIndex = idx },
        onClose = { showAlbums = false })
    pickAlbumFor?.let { items -> AlbumPicker(items) { pickAlbumFor = null } }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun PhotoCalendarDialog(
    @StringRes confirmLabel: Int,
    onDismiss: () -> Unit,
    onConfirm: (Long?) -> Unit,
    initialDateMillis: Long? = null,
) {
    val context = LocalContext.current
    val locale = LocalAppResources.current.configuration.locales[0]
    val configuration = LocalConfiguration.current
    val calendarConfiguration = remember(configuration, locale) {
        Configuration(configuration).apply { setLocale(locale) }
    }
    val calendarContext = remember(context, calendarConfiguration) {
        context.createConfigurationContext(calendarConfiguration)
    }
    // Material renders the calendar dates and its internal labels from these locals.
    CompositionLocalProvider(LocalConfiguration provides calendarConfiguration) {
        val state = rememberDatePickerState(initialSelectedDateMillis = initialDateMillis)
        DatePickerDialog(
            onDismissRequest = onDismiss,
            confirmButton = { TextButton(onClick = { onConfirm(state.selectedDateMillis) }) { Text(appString(confirmLabel)) } },
            dismissButton = { TextButton(onClick = onDismiss) { Text(appString(R.string.action_cancel)) } },
        ) {
            CompositionLocalProvider(LocalContext provides calendarContext) { DatePicker(state = state) }
        }
    }
}

// Instagram-stories memories viewer: auto-advancing full-screen cards with segmented
// progress bars on top, tap-left/right to move, long-press to pause, swipe down to close.
@OptIn(ExperimentalFoundationApi::class)
@androidx.annotation.OptIn(androidx.media3.common.util.UnstableApi::class)
@Composable
private fun StoriesViewer(photos: List<Photo>, start: Int, onOpenFull: (Photo) -> Unit, onClose: () -> Unit) {
    val ctx = LocalContext.current
    var idx by remember { mutableStateOf(start.coerceIn(0, photos.lastIndex)) }
    var paused by remember { mutableStateOf(false) }
    var videoFailed by remember(idx) { mutableStateOf(false) }
    var previewFailed by remember(idx) { mutableStateOf(false) }
    var retryPreview by remember(idx) { mutableIntStateOf(0) }
    val progress = remember(idx) { Animatable(0f) }
    var dragY by remember { mutableStateOf(0f) }
    val p = photos[idx]
    val account = remember(ctx) { Prefs(ctx).accountScope() }
    var shown by remember(account) { mutableStateOf<Pair<Photo, Painter>?>(null) }
    val photoReady = shown?.first == p
    val photoRequest = remember(account, p) { storyPreviewRequest(ctx, account, p) }
    key(account, p, retryPreview) {
        rememberAsyncImagePainter(
            model = photoRequest,
            onSuccess = { shown = p to it.painter },
            onError = { previewFailed = true },
        )
    }
    val preloadAdjacent = photoReady || p.isVideo
    LaunchedEffect(idx, preloadAdjacent, account) {
        if (preloadAdjacent) {
            // Keep at most one adjacent request active outside the current frame.
            val adjacent = photos.getOrNull(idx + 1) ?: photos.getOrNull(idx - 1)
            adjacent?.let { ctx.imageLoader.execute(storyPreviewRequest(ctx, account, it)) }
        }
    }
    val lifecycleOwner = LocalLifecycleOwner.current
    val storyPlayer = remember(idx) {
        if (!p.isVideo) null else ExoPlayer.Builder(ctx)
            .setMediaSourceFactory(DefaultMediaSourceFactory(OkHttpDataSource.Factory(Net.client)))
            .build().apply {
                setMediaItem(MediaItem.fromUri(Api.downloadUrl(p.source, p.path, inline = true)))
                addListener(object : Player.Listener {
                    override fun onPlayerError(error: PlaybackException) { videoFailed = true }
                })
                prepare(); playWhenReady = true
            }
    }
    DisposableEffect(storyPlayer) { onDispose { storyPlayer?.release() } }
    DisposableEffect(storyPlayer, lifecycleOwner, paused) {
        val observer = LifecycleEventObserver { _, event ->
            if (event == Lifecycle.Event.ON_STOP) storyPlayer?.pause()
            if (event == Lifecycle.Event.ON_START && !paused) storyPlayer?.play()
        }
        lifecycleOwner.lifecycle.addObserver(observer)
        onDispose { lifecycleOwner.lifecycle.removeObserver(observer) }
    }

    LaunchedEffect(idx, paused, storyPlayer, photoReady) {
        if (p.isVideo) {
            val player = storyPlayer ?: return@LaunchedEffect
            player.playWhenReady = !paused
            while (true) {
                val duration = player.duration
                if (duration > 0) progress.snapTo((player.currentPosition.toFloat() / duration).coerceIn(0f, 1f))
                if (player.playbackState == Player.STATE_ENDED) {
                    if (idx < photos.lastIndex) idx++ else onClose()
                    break
                }
                delay(80)
            }
        } else {
            if (paused || !photoReady) return@LaunchedEffect
            if (progress.value >= 1f) progress.snapTo(0f)
            val remaining = ((1f - progress.value) * 5000).toInt().coerceAtLeast(1)
            progress.animateTo(1f, tween(remaining, easing = LinearEasing))
            if (idx < photos.lastIndex) idx++ else onClose()
        }
    }

    val locale = LocalAppResources.current.configuration.locales[0]
    val dateStr = remember(p.modTime, locale) {
        Instant.ofEpochSecond(p.modTime).atZone(ZoneId.systemDefault()).toLocalDate()
            .format(DateTimeFormatter.ofLocalizedDate(FormatStyle.LONG).withLocale(locale))
    }

    Dialog(onDismissRequest = onClose, properties = DialogProperties(usePlatformDefaultWidth = false)) {
        Box(Modifier.fillMaxSize().background(Color.Black)
            .graphicsLayer { translationY = dragY }
            .pointerInput(Unit) {
                detectVerticalDragGestures(
                    onDragEnd = { if (dragY > size.height * 0.15f) onClose() else dragY = 0f },
                    onDragCancel = { dragY = 0f }
                ) { _, a -> if (a > 0 || dragY > 0) dragY = (dragY + a).coerceAtLeast(0f) }
            }) {
            shown?.let { (photo, painter) ->
                Image(painter, contentDescription = photo.name, contentScale = ContentScale.Fit, modifier = Modifier.fillMaxSize())
            }
            if (storyPlayer != null) AndroidView(
                factory = { c ->
                    PlayerView(c).apply {
                        player = storyPlayer; useController = false
                        setShutterBackgroundColor(android.graphics.Color.TRANSPARENT)
                        setBackgroundColor(android.graphics.Color.TRANSPARENT)
                    }
                },
                update = { it.player = storyPlayer },
                modifier = Modifier.fillMaxSize()
            )
            // tap zones: left third = back, right two-thirds = forward; long-press = pause
            Row(Modifier.fillMaxSize()) {
                Box(Modifier.weight(1f).fillMaxHeight().pointerInput(idx) {
                    detectTapGestures(onTap = { if (idx > 0) { idx--; } else onClose() }, onLongPress = { paused = true }, onPress = { tryAwaitRelease(); paused = false })
                })
                Box(Modifier.weight(2f).fillMaxHeight().pointerInput(idx) {
                    detectTapGestures(
                        onTap = { if (idx < photos.lastIndex) { idx++ } else onClose() },
                        onDoubleTap = { onOpenFull(photos[idx]) },
                        onLongPress = { paused = true }, onPress = { tryAwaitRelease(); paused = false })
                })
            }
            // segmented progress bars
            Row(Modifier.align(Alignment.TopCenter).statusBarsPadding().fillMaxWidth().padding(horizontal = 8.dp, vertical = 8.dp),
                horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                photos.forEachIndexed { i, _ ->
                    val f = when { i < idx -> 1f; i == idx -> progress.value; else -> 0f }
                    Box(Modifier.weight(1f).height(3.dp).clip(RoundedCornerShape(2.dp)).background(Color.White.copy(alpha = 0.35f))) {
                        Box(Modifier.fillMaxHeight().fillMaxWidth(f).clip(RoundedCornerShape(2.dp)).background(Color.White))
                    }
                }
            }
            Column(Modifier.align(Alignment.TopStart).statusBarsPadding().padding(start = 12.dp, top = 22.dp)) {
                Text(yearsAgoLabel(p.modTime), color = Color.White, fontSize = 17.sp, fontWeight = FontWeight.Bold)
                Text(dateStr, color = Color.White.copy(alpha = 0.85f), fontSize = 12.sp)
            }
            IconButton(onClick = onClose, modifier = Modifier.align(Alignment.TopEnd).statusBarsPadding().padding(top = 14.dp, end = 8.dp)
                .background(Color(0x1AFFFFFF), CircleShape)) {
                Icon(Icons.Rounded.Close, appString(R.string.action_close), Modifier.size(18.dp), tint = Color.White)
            }
            if (p.isVideo && paused) Icon(Icons.Rounded.PlayCircle, null, Modifier.align(Alignment.Center).size(64.dp), tint = Color.White.copy(alpha = 0.85f))
            if (videoFailed) Button(onClick = { onOpenFull(p) }, modifier = Modifier.align(Alignment.Center)) {
                Text(appString(R.string.photo_ui_open_video))
            }
            if (previewFailed && !p.isVideo) {
                Button(
                    onClick = { previewFailed = false; retryPreview++ },
                    modifier = Modifier.align(Alignment.Center),
                ) { Text(appString(R.string.photo_ui_story_retry)) }
            } else if (shown == null && !p.isVideo) {
                CircularProgressIndicator(Modifier.align(Alignment.Center).size(32.dp), color = Color.White)
            }
        }
    }
}

private fun storyPreviewRequest(ctx: android.content.Context, account: String, photo: Photo): ImageRequest {
    val key = "$account|${pid(photo)}|${photo.revision}@story"
    return ImageRequest.Builder(ctx).data(Api.photoThumbUrl(photo, 1280)).size(1280)
        .memoryCacheKey(key).diskCacheKey(key).crossfade(false).build()
}

@Composable
private fun BarAction(icon: androidx.compose.ui.graphics.vector.ImageVector, label: String, modifier: Modifier = Modifier, tint: Color = Fh.Text2, onClick: () -> Unit) {
    Column(modifier.clip(RoundedCornerShape(12.dp)).combinedClickableSimple(onClick).padding(horizontal = 3.dp, vertical = 6.dp), horizontalAlignment = Alignment.CenterHorizontally) {
        Icon(icon, label, Modifier.size(21.dp), tint = tint)
        Spacer(Modifier.height(4.dp))
        Text(label, fontSize = 11.5.sp, fontWeight = FontWeight.Medium, color = tint, maxLines = 1, overflow = TextOverflow.Ellipsis)
    }
}

// simple clickable without ripple fuss
@OptIn(ExperimentalFoundationApi::class)
private fun Modifier.combinedClickableSimple(onClick: () -> Unit) = this.combinedClickable(onClick = onClick)

@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun PhotoCell(
    p: Photo,
    selected: Boolean,
    inSelection: Boolean,
    thumbW: Int,
    onClick: () -> Unit,
    onLongClick: () -> Unit,
    menu: @Composable () -> Unit,
) {
    val ctx = LocalContext.current
    // static (non-animated) selection inset — per-cell animateDpAsState created two
    // animation holders per visible cell and cost frames on a big grid.
    val pad = if (selected) 8.dp else 0.dp
    val radius = if (selected) 12.dp else 0.dp
    val key = Prefs(ctx).accountScope() + "|" + pid(p)+"|"+p.revision
    // Built once per (key,thumbW) — stable across scroll/selection recompositions.
    val request = remember(key, thumbW) {
        ImageRequest.Builder(ctx).data(Api.photoThumbUrl(p, thumbW)).size(thumbW)
            .memoryCacheKey("$key@${thumbW}v2").diskCacheKey("$key@${thumbW}v2").crossfade(false).build()
    }
    // indication = null: the default ripple allocates an interaction node per cell and is
    // a known LazyGrid scroll-jank source; a photo tile doesn't need a ripple.
    val interaction = remember { MutableInteractionSource() }
    Box(Modifier.aspectRatio(1f).clip(Fh.TileShape).background(Fh.BgAlt)
        .combinedClickable(interactionSource = interaction, indication = null, onClick = onClick, onLongClick = onLongClick)) {
        AsyncImage(
            request,
            contentDescription = p.name, contentScale = ContentScale.Crop,
            modifier = if (selected) Modifier.matchParentSize().padding(pad).clip(RoundedCornerShape(radius))
                       else Modifier.matchParentSize()  // no clip/padding node when unselected (the common case)
        )
        // video badge: dark rounded-6 pill bottom-right (prototype's duration chip, icon-only)
        if (p.isVideo) Box(
            Modifier.align(Alignment.BottomEnd).padding(5.dp)
                .background(Color(0x990A0E18), RoundedCornerShape(6.dp)).padding(horizontal = 5.dp, vertical = 3.dp)
        ) { Icon(Icons.Rounded.PlayCircle, null, Modifier.size(13.dp), tint = Color.White) }
        if (inSelection) Icon(
            if (selected) Icons.Filled.CheckCircle else Icons.Outlined.RadioButtonUnchecked, null,
            tint = if (selected) Fh.Accent else Color.White,
            modifier = Modifier.align(Alignment.TopStart).padding(5.dp).size(22.dp)
                .background(if (selected) Color.White else Color(0x38000000), CircleShape)
        )
        menu()
    }
}

@Composable
private fun PhotoContextMenu(
    onDismiss: () -> Unit,
    onDownload: () -> Unit,
    onAlbum: () -> Unit,
    onSelect: () -> Unit,
    canModify: Boolean,
    onMove: () -> Unit,
    onDelete: () -> Unit,
) {
    DropdownMenu(expanded = true, onDismissRequest = onDismiss, modifier = Modifier.widthIn(max = 224.dp)) {
        DropdownMenuItem(
            text = { Text(appString(R.string.photo_ui_download)) },
            leadingIcon = { Icon(Icons.Outlined.Download, null) },
            onClick = { onDismiss(); onDownload() },
        )
        DropdownMenuItem(
            text = { Text(appString(R.string.photo_ui_to_album)) },
            leadingIcon = { Icon(Icons.Outlined.PhotoAlbum, null) },
            onClick = { onDismiss(); onAlbum() },
        )
        DropdownMenuItem(
            text = { Text(appString(R.string.photo_ui_select_photos)) },
            leadingIcon = { Icon(Icons.Outlined.SelectAll, null) },
            onClick = { onDismiss(); onSelect() },
        )
        if (canModify) {
            DropdownMenuItem(text = { Text(appString(R.string.file_ui_move)) },
                leadingIcon = { Icon(Icons.Outlined.DriveFileMove, null) },
                onClick = { onDismiss(); onMove() })
            DropdownMenuItem(text = { Text(appString(R.string.photo_ui_delete), color = MaterialTheme.colorScheme.error) },
                leadingIcon = { Icon(Icons.Outlined.Delete, null, tint = MaterialTheme.colorScheme.error) },
                onClick = { onDismiss(); onDelete() })
        }
    }
}

// Immich-style fast-scroll handle on the right edge: drag to jump through the
// timeline, shows the date you're at. Auto-hides when not scrolling.
@Composable
private fun BoxScope.DateScrubber(
    gridState: LazyGridState,
    timeline: PhotoTimeline,
    leadingItems: Int,
    sectionLabel: (PhotoTimeline.Section) -> String,
) {
    val scope = rememberCoroutineScope()
    val total = timeline.itemCount + leadingItems
    if (total < 80) return // not worth it for short lists
    var dragging by remember { mutableStateOf(false) }
    var trackH by remember { mutableStateOf(1f) }
    val handleH = 56.dp

    // Format only the active group, and only while dragging.
    val label by remember(timeline, leadingItems, sectionLabel) { derivedStateOf {
        if (!dragging) "" else timeline.sectionAt(gridState.firstVisibleItemIndex - leadingItems)?.let(sectionLabel).orEmpty()
    } }
    val show = dragging || gridState.isScrollInProgress
    val alpha by animateFloatAsState(if (show) 1f else 0f, tween(if (show) 120 else 500), label = "scrub")

    Box(Modifier.align(Alignment.CenterEnd).fillMaxHeight().width(40.dp)
        .onSizeChanged { trackH = it.height.toFloat().coerceAtLeast(1f) }
        .pointerInput(total) {
            detectVerticalDragGestures(
                onDragStart = { dragging = true }, onDragEnd = { dragging = false }, onDragCancel = { dragging = false }
            ) { change, _ ->
                val f = (change.position.y / trackH).coerceIn(0f, 1f)
                scope.launch { gridState.scrollToItem((f * total).toInt().coerceIn(0, total - 1)) }
            }
        }) {
        // frac read INSIDE the layout lambdas → scrolling repositions the handle without
        // recomposing the scrubber every frame.
        Box(Modifier.align(Alignment.TopEnd)
            .offset {
                val frac = (gridState.firstVisibleItemIndex.toFloat() / total).coerceIn(0f, 1f)
                IntOffset(0, (frac * (trackH - handleH.toPx())).toInt())
            }
            .graphicsLayer { this.alpha = alpha }
            .padding(end = 3.dp).size(width = 10.dp, height = handleH)
            .clip(RoundedCornerShape(5.dp)).background(MaterialTheme.colorScheme.primary))
    }
    // The label is a sibling of the 40dp touch rail. Offset only changes placement,
    // not measurement; keeping it inside that rail wrapped Russian month names.
    if (dragging && label.isNotEmpty()) {
        Surface(shape = RoundedCornerShape(99.dp), color = Fh.Toast,
            modifier = Modifier.align(Alignment.TopEnd)
                .offset {
                    val frac = (gridState.firstVisibleItemIndex.toFloat() / total).coerceIn(0f, 1f)
                    IntOffset(-(48.dp.toPx()).toInt(), (frac * (trackH - handleH.toPx())).toInt() + 14)
                }
                .widthIn(min = 120.dp, max = 190.dp)) {
            Text(label, Modifier.padding(horizontal = 12.dp, vertical = 8.dp),
                color = MaterialTheme.colorScheme.inverseOnSurface, fontSize = 13.sp, fontWeight = FontWeight.Medium,
                maxLines = 1, softWrap = false, overflow = TextOverflow.Ellipsis)
        }
    }
}

@Composable
private fun SyncCard(prefs: Prefs, mediaTotal: Int, backedUpCount: Int, auto: Boolean, onToggle: (Boolean) -> Unit, onFreeSpace: () -> Unit) {
    val ctx = LocalContext.current
    val infos by Backup.workInfos(ctx).collectAsStateWithLifecycle(emptyList())
    val info = activeBackup(infos)
    val running = info?.state == WorkInfo.State.RUNNING
    val waiting = info?.state == WorkInfo.State.ENQUEUED
    val countsReady = mediaTotal >= 0 && backedUpCount >= 0
    val remaining = if (countsReady) (mediaTotal - backedUpCount).coerceAtLeast(0) else -1

    Card(Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 4.dp), shape = Fh.CardShape,
        colors = CardDefaults.cardColors(containerColor = Fh.Bg)) {
        Column(Modifier.padding(16.dp)) {
            val (icon, tint, label) = when {
                running -> Triple(Icons.Outlined.CloudSync, Fh.Accent, appString(R.string.backup_sync_active))
                waiting -> Triple(Icons.Outlined.CloudQueue, Fh.Accent, appString(R.string.backup_sync_waiting))
                auto && remaining == 0 -> Triple(Icons.Outlined.CloudDone, Fh.GreenDeep, appString(R.string.backup_sync_nothing_new))
                auto -> Triple(Icons.Outlined.CloudQueue, Fh.Accent, appString(R.string.backup_sync_enabled))
                else -> Triple(Icons.Outlined.CloudOff, Fh.Muted, appString(R.string.backup_sync_disabled))
            }
            Row(verticalAlignment = Alignment.CenterVertically) {
                Icon(icon, null, tint = tint); Spacer(Modifier.width(10.dp))
                Text(label, style = MaterialTheme.typography.titleMedium, modifier = Modifier.weight(1f))
                FhSwitch(checked = auto, onChecked = onToggle)
            }
            Spacer(Modifier.height(10.dp))
            Text(appString(R.string.backup_sync_help), color = Fh.Muted, style = MaterialTheme.typography.bodySmall)
            Spacer(Modifier.height(8.dp))
            if (countsReady) {
                StatRow(appString(R.string.backup_sync_eligible), mediaTotal)
                StatRow(appString(R.string.backup_sync_uploaded), backedUpCount)
                StatRow(appString(R.string.backup_sync_unverified), remaining)
            } else Text(appString(R.string.backup_sync_counts_unknown), color = Fh.Muted, style = MaterialTheme.typography.bodySmall)
            if (!running && prefs.status.isNotBlank()) Text(prefs.status, color = Fh.Text2, fontSize = 12.sp)
            if (info != null) {
                Spacer(Modifier.height(10.dp))
                SyncProgressDetails(info)
            }
            Spacer(Modifier.height(10.dp))
            if (running) OutlinedButton(onClick = { Backup.stop(ctx) }, modifier = Modifier.fillMaxWidth(), shape = RoundedCornerShape(14.dp), border = BorderStroke(1.dp, Fh.Border)) { Text(appString(R.string.backup_sync_stop), color = Fh.Text2) }
            else Button(onClick = { Backup.runNow(ctx) }, modifier = Modifier.fillMaxWidth(), shape = RoundedCornerShape(14.dp),
                contentPadding = PaddingValues(vertical = 14.dp)) {
                Text(appString(R.string.backup_sync_now), fontSize = 15.sp, fontWeight = FontWeight.Bold)
            }
            if (backedUpCount > 0) {
                Spacer(Modifier.height(8.dp))
                Button(onClick = onFreeSpace, modifier = Modifier.fillMaxWidth(), shape = RoundedCornerShape(14.dp),
                    colors = ButtonDefaults.buttonColors(containerColor = Fh.AccentSoft, contentColor = Fh.AccentDeep)) {
                    Icon(Icons.Outlined.Delete, null, Modifier.size(18.dp)); Spacer(Modifier.width(6.dp))
                    Text(appString(R.string.cleanup_free_space_limit), fontWeight = FontWeight.SemiBold)
                }
            }
        }
    }
}

@androidx.annotation.OptIn(androidx.media3.common.util.UnstableApi::class)
@Composable
private fun VideoPage(p: Photo, play: Boolean) {
    val ctx = LocalContext.current
    val locale = LocalAppResources.current.configuration.locales[0]
    // Thumbnail poster ALWAYS drawn behind → no black flash while ExoPlayer buffers,
    // and the player is only built once the swipe settles on this page (play=true),
    // so swiping through several videos never churns players mid-scroll.
    Box(Modifier.fillMaxSize().background(Color.Black), contentAlignment = Alignment.Center) {
        AsyncImage(
            ImageRequest.Builder(ctx).data(Api.photoThumbUrl(p,1280))
                .placeholderMemoryCacheKey("${pid(p)}@${thumbWidthFor(4)}v2").crossfade(false).build(),
            contentDescription = p.name, contentScale = ContentScale.Fit, modifier = Modifier.fillMaxSize()
        )
        if (play) {
            val player = remember(pid(p)) {
                ExoPlayer.Builder(ctx)
                    .setMediaSourceFactory(DefaultMediaSourceFactory(OkHttpDataSource.Factory(Net.client)))
                    .build().apply {
                        setMediaItem(MediaItem.fromUri(Api.downloadUrl(p.source, p.path, inline = true)))
                        prepare(); playWhenReady = true
                    }
            }
            DisposableEffect(player) { onDispose { player.release() } }
            AndroidView(
                factory = { c ->
                    val localizedContext = c.createConfigurationContext(Configuration().apply { setLocale(locale) })
                    PlayerView(localizedContext).apply {
                        this.player = player; useController = true
                        setShowBuffering(PlayerView.SHOW_BUFFERING_ALWAYS)
                        setBackgroundColor(android.graphics.Color.TRANSPARENT)
                    }
                },
                modifier = Modifier.fillMaxSize()
            )
        } else {
            Icon(Icons.Rounded.PlayCircle, null, Modifier.size(72.dp), tint = Color.White.copy(alpha = 0.9f))
        }
    }
}

@Composable
private fun StatRow(label: String, value: Int) {
    val locale = LocalAppResources.current.configuration.locales[0]
    val formatted = remember(value, locale) { NumberFormat.getIntegerInstance(locale).format(value) }
    Row(Modifier.fillMaxWidth().padding(vertical = 5.dp), horizontalArrangement = Arrangement.SpaceBetween, verticalAlignment = Alignment.CenterVertically) {
        Text(label, style = MaterialTheme.typography.bodyLarge, color = MaterialTheme.colorScheme.onSurfaceVariant)
        Text(formatted, style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
    }
}

@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun PhotoViewer(photos: List<Photo>, start: Int, isFav: (Photo) -> Boolean, onToggleFav: (Photo) -> Unit, onInfo: (Photo) -> Unit, onRotate: (Photo) -> Unit, onCrop: (Photo) -> Unit, canTrash: (Photo) -> Boolean, onTrash: (Photo) -> Unit, onClose: () -> Unit) {
    val ctx = LocalContext.current
    var chrome by remember { mutableStateOf(true) } // top bar; tap the photo to toggle
    var bgAlpha by remember { mutableStateOf(1f) }   // dims toward 0 as you drag to dismiss
    Dialog(onDismissRequest = onClose, properties = DialogProperties(usePlatformDefaultWidth = false)) {
        val pager = rememberPagerState(initialPage = start.coerceIn(0, (photos.size - 1).coerceAtLeast(0))) { photos.size }
        DisposableEffect(pager.currentPage,photos) {
            val requests=listOf(pager.currentPage-1,pager.currentPage+1,pager.currentPage+2).mapNotNull {idx->
                photos.getOrNull(idx)?.let {p->ctx.imageLoader.enqueue(ImageRequest.Builder(ctx).data(Api.photoThumbUrl(p,1280)).build())}
            }
            onDispose {requests.forEach {it.dispose()}}
        }
        Box(Modifier.fillMaxSize().background(Fh.Dark.copy(alpha = bgAlpha))) {
            HorizontalPager(state = pager, modifier = Modifier.fillMaxSize(), beyondBoundsPageCount = 1, pageSpacing = 16.dp,
                key = { photos[it].source + "|" + photos[it].path }) { i ->
                val p = photos[i]
                val settled = i == pager.currentPage && !pager.isScrollInProgress
                if (p.isVideo) {
                    VideoPage(p, play = settled)
                } else {
                    ImagePage(p, onTapToggle = { chrome = !chrome }, onDragFraction = { bgAlpha = 1f - it }, onDismiss = onClose)
                }
            }
            AnimatedVisibility(chrome, enter = androidx.compose.animation.fadeIn(), exit = androidx.compose.animation.fadeOut()) {
                Box(Modifier.fillMaxSize()) {
                    val cur = photos.getOrNull(pager.currentPage)
                    // top chrome: back · name + date · info
                    Row(Modifier.align(Alignment.TopCenter).fillMaxWidth().statusBarsPadding().padding(horizontal = 14.dp, vertical = 8.dp),
                        verticalAlignment = Alignment.CenterVertically) {
                        ViewerRound(Icons.AutoMirrored.Filled.ArrowBack, appString(R.string.action_close), onClose)
                        Spacer(Modifier.width(12.dp))
                        Column(Modifier.weight(1f)) {
                            Text(cur?.name ?: "", color = Color.White, fontSize = 15.sp, fontWeight = FontWeight.SemiBold,
                                maxLines = 1, overflow = TextOverflow.Ellipsis)
                            val locale = LocalAppResources.current.configuration.locales[0]
                            val sub = remember(cur, locale) {
                                cur?.let {
                                    Instant.ofEpochSecond(it.modTime).atZone(ZoneId.systemDefault())
                                        .format(DateTimeFormatter.ofLocalizedDateTime(FormatStyle.LONG, FormatStyle.SHORT).withLocale(locale))
                                } ?: ""
                            }
                            Text(sub, color = Color.White.copy(alpha = 0.55f), fontSize = 11.5.sp, maxLines = 1)
                        }
                        Spacer(Modifier.width(12.dp))
                        ViewerRound(Icons.Outlined.Info, appString(R.string.photo_ui_properties)) { cur?.let(onInfo) }
                        if (cur != null && canTrash(cur)) {
                            Spacer(Modifier.width(6.dp))
                            ViewerRound(Icons.Outlined.Delete, appString(R.string.file_ui_trash)) { onTrash(cur) }
                        }
                    }
                    // bottom chrome: icon+label actions (all pre-redesign actions live here)
                    if (cur != null) Row(
                        Modifier.align(Alignment.BottomCenter).fillMaxWidth().navigationBarsPadding().padding(horizontal = 20.dp, vertical = 14.dp),
                        horizontalArrangement = Arrangement.SpaceEvenly
                    ) {
                        ViewerAction(if (isFav(cur)) Icons.Filled.Favorite else Icons.Outlined.FavoriteBorder, appString(R.string.photo_ui_favorites),
                            tint = if (isFav(cur)) Fh.Heart else Color(0xCCFFFFFF)) { onToggleFav(cur) }
                        if (!cur.isVideo) ViewerAction(Icons.Outlined.RotateRight, appString(R.string.photo_ui_rotate)) { onRotate(cur) }
                        if (!cur.isVideo) ViewerAction(Icons.Outlined.Crop, appString(R.string.photo_ui_crop)) { onCrop(cur) }
                    }
                }
            }
        }
    }
}

/** 38dp translucent round icon button for the dark viewer chrome. */
@Composable
private fun ViewerRound(icon: androidx.compose.ui.graphics.vector.ImageVector, desc: String?, onClick: () -> Unit) {
    val interaction = remember { MutableInteractionSource() }
    Box(
        Modifier.size(48.dp).clip(CircleShape).background(Color(0x1AFFFFFF))
            .clickable(interactionSource = interaction, indication = null, onClick = onClick),
        contentAlignment = Alignment.Center
    ) { Icon(icon, desc, Modifier.size(18.dp), tint = Color.White) }
}

/** Icon over an 11.5sp label — viewer bottom-row action, 80% white. */
@Composable
private fun ViewerAction(icon: androidx.compose.ui.graphics.vector.ImageVector, label: String, tint: Color = Color(0xCCFFFFFF), onClick: () -> Unit) {
    Column(
        Modifier.clip(RoundedCornerShape(12.dp)).combinedClickableSimple(onClick).padding(horizontal = 14.dp, vertical = 6.dp),
        horizontalAlignment = Alignment.CenterHorizontally
    ) {
        Icon(icon, label, Modifier.size(21.dp), tint = tint)
        Spacer(Modifier.height(6.dp))
        Text(label, fontSize = 11.5.sp, fontWeight = FontWeight.Medium, color = tint)
    }
}

// One full-screen image page: pinch-zoom (zoomable), tap to toggle chrome, and
// swipe DOWN (when not zoomed) to dismiss — translates + fades like Google Photos.
@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun ImagePage(p: Photo, onTapToggle: () -> Unit, onDragFraction: (Float) -> Unit, onDismiss: () -> Unit) {
    val ctx = LocalContext.current
    val zoom = rememberZoomState()
    var dragY by remember { mutableStateOf(0f) }
    Box(
        Modifier.fillMaxSize()
            .pointerInput(Unit) { detectTapGestures(onTap = { onTapToggle() }) }
            .pointerInput(Unit) {
                val dismissPx = size.height * 0.20f
                detectVerticalDragGestures(
                    onDragEnd = { if (kotlin.math.abs(dragY) > dismissPx) onDismiss() else { dragY = 0f; onDragFraction(0f) } },
                    onDragCancel = { dragY = 0f; onDragFraction(0f) }
                ) { _, amount ->
                    if (zoom.scale <= 1.02f) { // only when not zoomed; zoomed → let zoomable pan
                        dragY += amount
                        onDragFraction((kotlin.math.abs(dragY) / (size.height * 0.6f)).coerceIn(0f, 1f))
                    }
                }
            }
            .graphicsLayer {
                translationY = dragY
                val s = (1f - kotlin.math.abs(dragY) / (size.height * 1.5f)).coerceIn(0.85f, 1f)
                scaleX = s; scaleY = s
            },
        contentAlignment = Alignment.Center
    ) {
        AsyncImage(
            ImageRequest.Builder(ctx).data(Api.photoThumbUrl(p,1280))
                .placeholderMemoryCacheKey("${pid(p)}@${thumbWidthFor(4)}v2").crossfade(true).build(),
            contentDescription = p.name, contentScale = ContentScale.Fit,
            modifier = Modifier.fillMaxSize().zoomable(zoom, scrollGesturePropagation = ScrollGesturePropagation.NotZoomed)
        )
    }
}
