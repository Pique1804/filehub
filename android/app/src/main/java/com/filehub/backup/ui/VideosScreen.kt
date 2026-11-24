package com.filehub.backup.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.GridItemSpan
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Download
import androidx.compose.material.icons.rounded.ExpandMore
import androidx.compose.material.icons.rounded.PlayCircle
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalUriHandler
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import coil.compose.AsyncImage
import com.filehub.backup.Api
import com.filehub.backup.LibraryVideo
import com.filehub.backup.OfflineDownload
import com.filehub.backup.R
import com.filehub.backup.TmdbMatch
import com.filehub.backup.VideoLibrary
import com.filehub.backup.VideoTitle
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

@Composable
fun VideosScreen(onOpenDownloads: () -> Unit) {
    val context = LocalContext.current
    val uriHandler = LocalUriHandler.current
    val resources = LocalAppResources.current
    val scope = rememberCoroutineScope()
    var catalog by remember { mutableStateOf<VideoLibrary?>(null) }
    var search by rememberSaveable { mutableStateOf("") }
    var kind by rememberSaveable { mutableStateOf("all") }
    var selected by rememberSaveable { mutableStateOf<String?>(null) }
    var activeSeason by rememberSaveable(selected) { mutableStateOf<Int?>(null) }
    var seasonPicker by remember { mutableStateOf(false) }
    var configuring by rememberSaveable { mutableStateOf(false) }
    var identify by remember { mutableStateOf<VideoTitle?>(null) }
    var folderKind by rememberSaveable { mutableStateOf("movies") }
    var picker by remember { mutableStateOf(false) }
    var playbackKey by rememberSaveable { mutableStateOf<ArrayList<String>?>(null) }
    var refresh by remember { mutableIntStateOf(0) }
    var loading by remember { mutableStateOf(true) }
    var savingFolder by remember { mutableStateOf(false) }
    var warning by remember { mutableIntStateOf(0) }
    val progress = remember { Channel<Pair<LibraryVideo, Double>?>(capacity = 16) }

    LaunchedEffect(progress) {
        for (event in progress) {
            if (event == null) {
                refresh++
            } else {
                val (video, position) = event
                withContext(Dispatchers.IO) { Api.saveVideoPosition(video.source, video.path, position) }
            }
        }
    }

    LaunchedEffect(refresh) {
        loading = true
        warning = 0
        val result = withContext(Dispatchers.IO) { Api.videoLibrary() }
        if (result != null) catalog = result else warning = R.string.video_library_load_failed
        loading = false
        for (interval in listOf(1500L, 3000L, 6000L)) {
            if (catalog?.tmdbPending != true) break
            delay(interval)
            if (playbackKey != null || identify != null) break
            withContext(Dispatchers.IO) { Api.videoLibrary() }?.let { catalog = it }
        }
    }

    fun changeFolder(source: String, path: String, type: String) {
        savingFolder = true
        scope.launch {
            val success = withContext(Dispatchers.IO) { Api.setVideoFolder(source, path, type) }
            savingFolder = false
            if (success) refresh++ else warning = R.string.video_library_folder_failed
        }
    }

    val titles = catalog?.titles.orEmpty()
    val title = titles.find { it.id == selected }
    val playingTitle = playbackKey?.let { key -> titles.find { item ->
        item.videos.any { it.source == key[0] && it.path == key[1] }
    } }
    val playing = playbackKey?.let { key -> playingTitle?.videos?.find { it.source == key[0] && it.path == key[1] } }
    fun openVideo(video: LibraryVideo) {
        playbackKey = arrayListOf(video.source, video.path)
    }
    fun updatePosition(video: LibraryVideo, position: Double) {
        progress.trySend(video to position)
        catalog = catalog?.let { data -> data.copy(titles = data.titles.map { entry ->
            if (entry.id != playingTitle?.id) entry else entry.copy(videos = entry.videos.map {
                if (it.source == video.source && it.path == video.path) {
                    it.copy(position = position, watched = System.currentTimeMillis() / 1000)
                } else it
            })
        }) }
    }
    val visible = titles.filter { item ->
        (kind == "all" || item.kind == kind) &&
            (item.name.contains(search.trim(), ignoreCase = true) ||
                item.description.contains(search.trim(), ignoreCase = true) ||
                item.genres.any { it.contains(search.trim(), ignoreCase = true) } || item.videos.any { it.name.contains(search.trim(), ignoreCase = true) })
    }
    val resumable = titles.flatMap { item -> item.videos.filter { it.position > 0 }.map { item to it } }
        .sortedByDescending { (_, video) -> video.watched }.take(8)
    val movies = appString(R.string.video_library_movies)
    val series = appString(R.string.video_library_series)

    LazyVerticalGrid(
        columns = GridCells.Adaptive(136.dp),
        contentPadding = PaddingValues(16.dp),
        verticalArrangement = Arrangement.spacedBy(10.dp),
        horizontalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        item(span = { GridItemSpan(maxLineSpan) }) {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text(appString(R.string.video_library_title), style = MaterialTheme.typography.headlineMedium)
                TextButton(onClick = onOpenDownloads, contentPadding = PaddingValues(horizontal = 8.dp)) {
                    Icon(Icons.Outlined.Download, null, Modifier.size(18.dp))
                    Spacer(Modifier.width(8.dp))
                    Text(appString(R.string.downloads_title))
                }
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    if (catalog?.canManage == true) {
                        OutlinedButton(onClick = { configuring = !configuring }) {
                            Text(appString(R.string.video_library_folders))
                        }
                    }
                    TextButton(onClick = { refresh++ }, enabled = !loading && !savingFolder) {
                        Text(appString(R.string.file_ui_refresh))
                    }
                }
                if (loading || savingFolder) LinearProgressIndicator(Modifier.fillMaxWidth())
                if (warning != 0) Text(appString(warning), color = MaterialTheme.colorScheme.error)
                if (catalog?.partial == true) Text(appString(R.string.video_library_partial), color = Fh.Muted)
            }
        }

        if (configuring && catalog?.canManage == true) {
            item(span = { GridItemSpan(maxLineSpan) }) {
                FhCard {
                    Column(Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                        catalog?.folders.orEmpty().forEach { folder ->
                            Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                                val label = if (folder.kind == "movies") movies else series
                                Text("$label · ${folder.source}:/${folder.path}", Modifier.weight(1f))
                                TextButton(
                                    onClick = { changeFolder(folder.source, folder.path, "remove") },
                                    enabled = !savingFolder,
                                ) { Text(appString(R.string.video_library_remove_folder)) }
                            }
                        }
                        FhSegmented(listOf("movies" to movies, "series" to series), folderKind, { folderKind = it })
                        Button(onClick = { picker = true }, enabled = !savingFolder) {
                            Text(appString(R.string.video_library_add_folder))
                        }
                    }
                }
            }
        }

        if (title != null) {
            val resume = title.videos.filter { it.position > 0 }.maxByOrNull { it.watched }
            val bySeason = title.videos.groupBy { it.season }
            val season = activeSeason?.takeIf(bySeason::containsKey) ?: bySeason.keys.firstOrNull()
            item(span = { GridItemSpan(maxLineSpan) }) {
                Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                    TextButton(onClick = { selected = null }) { Text(appString(R.string.photo_ui_back)) }
                    Row(horizontalArrangement = Arrangement.spacedBy(14.dp)) {
                        CatalogPoster(title, Modifier.width(90.dp).aspectRatio(2f / 3f))
                        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(5.dp)) {
                            Text(title.name, style = MaterialTheme.typography.headlineSmall)
                            val meta = listOfNotNull(title.year.takeIf { it > 0 }?.toString()) + title.genres
                            if (meta.isNotEmpty()) Text(meta.joinToString(" · "), color = Fh.Muted)
                            Button(onClick = { openVideo(resume ?: title.videos.first()) }) {
                                Icon(Icons.Rounded.PlayCircle, null)
                                Spacer(Modifier.width(8.dp))
                                Text(appString(if (resume != null) R.string.video_library_continue else R.string.video_play))
                            }
                        }
                    }
                    if (title.description.isNotBlank()) Text(title.description, style = MaterialTheme.typography.bodyMedium)
                    if (catalog?.canManage == true && catalog?.tmdbEnabled == true) {
                        OutlinedButton(onClick = { identify = title }) { Text(appString(R.string.tmdb_identify)) }
                    }
                    if (title.metadataLink.isNotBlank()) {
                        TextButton(onClick = { uriHandler.openUri(title.metadataLink) }) {
                            Text(if (title.metadataSource == "tmdb") "TMDB" else "TVmaze · CC BY-SA")
                        }
                        if (title.metadataSource == "tmdb") {
                            Text("This product uses the TMDB API but is not endorsed or certified by TMDB.",
                                color = Fh.Muted, style = MaterialTheme.typography.labelSmall)
                        }
                    }
                }
            }
            if (title.kind == "series" && season != null) {
                item(span = { GridItemSpan(maxLineSpan) }) {
                    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically,
                        horizontalArrangement = Arrangement.SpaceBetween) {
                        Text(seasonLabel(season), style = MaterialTheme.typography.titleMedium)
                        if (bySeason.size > 1) Box {
                            OutlinedButton(onClick = { seasonPicker = true }) {
                                Text(appString(R.string.video_season_picker), Modifier.weight(1f, fill = false))
                                Spacer(Modifier.width(8.dp))
                                Icon(Icons.Rounded.ExpandMore, null, Modifier.size(18.dp))
                            }
                            DropdownMenu(expanded = seasonPicker, onDismissRequest = { seasonPicker = false }) {
                                bySeason.keys.forEach { choice ->
                                    DropdownMenuItem(text = { Text(seasonLabel(choice)) }, onClick = {
                                        activeSeason = choice
                                        seasonPicker = false
                                    })
                                }
                            }
                        }
                    }
                }
                items(bySeason[season].orEmpty(), span = { GridItemSpan(maxLineSpan) }) { video ->
                    OutlinedCard(onClick = { openVideo(video) }, modifier = Modifier.fillMaxWidth()) {
                        Row(Modifier.fillMaxWidth().padding(8.dp), verticalAlignment = Alignment.CenterVertically,
                            horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                            VideoPreview(Api.videoFrameUrl(video.source, video.path), Modifier.width(92.dp).aspectRatio(16f / 9f),
                                posterSources(title))
                            Column {
                                val number = if (video.episode > 0) appString(R.string.video_library_episode, video.episode) + " · " else ""
                                Text(number + video.name, maxLines = 2, overflow = TextOverflow.Ellipsis)
                                if (video.position > 0) Text(appString(R.string.video_library_continue),
                                    color = Fh.Muted, style = MaterialTheme.typography.labelSmall)
                            }
                        }
                    }
                }
            }
        } else if (catalog != null) {
            if (resumable.isNotEmpty()) {
                item(span = { GridItemSpan(maxLineSpan) }) {
                    Text(appString(R.string.video_library_continue), style = MaterialTheme.typography.titleLarge)
                }
                items(resumable, span = { GridItemSpan(maxLineSpan) }) { (item, video) ->
                    OutlinedCard(onClick = { openVideo(video) }, modifier = Modifier.fillMaxWidth()) {
                        Row(Modifier.padding(8.dp), horizontalArrangement = Arrangement.spacedBy(12.dp),
                            verticalAlignment = Alignment.CenterVertically) {
                            VideoPreview(Api.videoFrameUrl(video.source, video.path), Modifier.width(92.dp).aspectRatio(16f / 9f),
                                posterSources(item))
                            Column(Modifier.weight(1f)) {
                                Text(item.name, maxLines = 2, overflow = TextOverflow.Ellipsis)
                                if (item.kind == "series") Text(video.name, color = Fh.Muted,
                                    style = MaterialTheme.typography.labelSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
                            }
                        }
                    }
                }
            }
            if (catalog?.folders.orEmpty().isNotEmpty()) {
                item(span = { GridItemSpan(maxLineSpan) }) {
                    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                        FhSegmented(listOf("all" to appString(R.string.video_library_all), "movies" to movies, "series" to series),
                            kind, { kind = it })
                        OutlinedTextField(search, { search = it }, Modifier.fillMaxWidth(), singleLine = true,
                            label = { Text(appString(R.string.video_library_search)) })
                    }
                }
                items(visible, key = { it.id }) { item ->
                    OutlinedCard(onClick = { selected = item.id }) {
                        CatalogPoster(item, Modifier.fillMaxWidth().aspectRatio(2f / 3f))
                        Text(item.name, Modifier.padding(10.dp), style = MaterialTheme.typography.titleSmall,
                            maxLines = 2, overflow = TextOverflow.Ellipsis)
                    }
                }
                if (visible.isEmpty() && !loading) item(span = { GridItemSpan(maxLineSpan) }) {
                    Text(appString(R.string.video_library_empty), color = Fh.Muted)
                }
            } else if (!loading) item(span = { GridItemSpan(maxLineSpan) }) {
                Text(appString(R.string.video_library_no_folders), color = Fh.Muted)
            }
        }
    }

    if (picker) FolderPickerDialog(
        onPick = { source, path -> picker = false; changeFolder(source, path, folderKind) },
        onDismiss = { picker = false },
    )

    playing?.let { video ->
        VideoScreen(
            source = video.source, path = video.path, name = video.name,
            position = video.position,
            onPosition = { position -> updatePosition(video, position) },
            series = playingTitle?.takeIf { it.kind == "series" }?.name,
            episodes = playingTitle?.takeIf { it.kind == "series" }?.videos.orEmpty(),
            onEpisode = { next -> openVideo(next) },
            onDownloadOffline = {
                OfflineDownload.enqueue(context, video.source, video.path)
                onOpenDownloads()
            },
            onClose = { playbackKey = null; progress.trySend(null) },
        )
    }
    identify?.let { item ->
        TmdbMatchDialog(item, onDismiss = { identify = null }, onSaved = { identify = null; refresh++ })
    }
}

@Composable
private fun TmdbMatchDialog(title: VideoTitle, onDismiss: () -> Unit, onSaved: () -> Unit) {
    val scope = rememberCoroutineScope()
    var query by remember(title.id) { mutableStateOf(title.tmdbQuery.ifBlank { title.name }) }
    var year by remember(title.id) { mutableStateOf(title.tmdbQueryYear.takeIf { it > 0 }?.toString().orEmpty()) }
    var matches by remember { mutableStateOf<List<TmdbMatch>?>(null) }
    var busy by remember { mutableStateOf(true) }
    var error by remember { mutableIntStateOf(0) }
    suspend fun search(name: String, releaseYear: String) {
        busy = true
        error = 0
        matches = withContext(Dispatchers.IO) { Api.findTmdbMatches(title.id, name, releaseYear) }
        if (matches == null) error = R.string.tmdb_search_failed
        busy = false
    }
    LaunchedEffect(title.id) { search(query, year) }
    AlertDialog(
        onDismissRequest = { if (!busy) onDismiss() },
        title = { Text(appString(R.string.tmdb_identify)) },
        text = {
            Column(Modifier.heightIn(max = 420.dp).verticalScroll(rememberScrollState()),
                verticalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedTextField(query, { query = it.take(160) }, Modifier.fillMaxWidth(),
                    label = { Text(appString(R.string.video_library_search)) }, singleLine = true, enabled = !busy)
                OutlinedTextField(year, { year = it.filter(Char::isDigit).take(4) }, Modifier.fillMaxWidth(),
                    label = { Text(appString(R.string.tmdb_year)) }, singleLine = true, enabled = !busy,
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number))
                if (busy) LinearProgressIndicator(Modifier.fillMaxWidth())
                if (error != 0) Text(appString(error), color = MaterialTheme.colorScheme.error)
                if (!busy && error == 0 && matches?.isEmpty() == true) Text(appString(R.string.tmdb_no_matches))
                for (match in matches.orEmpty()) {
                    OutlinedCard(onClick = {
                        scope.launch {
                            busy = true
                            error = 0
                            val saved = withContext(Dispatchers.IO) { Api.saveTmdbMatch(title.id, match.id) }
                            busy = false
                            if (saved) onSaved() else error = R.string.tmdb_save_failed
                        }
                    }, modifier = Modifier.fillMaxWidth(), enabled = !busy) {
                        Row(Modifier.padding(8.dp), verticalAlignment = Alignment.CenterVertically,
                            horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                            VideoPreview(match.poster.takeIf { it.startsWith("https://image.tmdb.org/t/p/") },
                                Modifier.width(34.dp).aspectRatio(2f / 3f))
                            Column(Modifier.weight(1f)) {
                                Text(match.name.ifBlank { match.original }, maxLines = 2, overflow = TextOverflow.Ellipsis)
                                val details = listOfNotNull(match.year.takeIf { it > 0 }?.toString(),
                                    match.original.takeIf { it.isNotBlank() && it != match.name })
                                if (details.isNotEmpty()) Text(details.joinToString(" · "), color = Fh.Muted,
                                    style = MaterialTheme.typography.labelSmall)
                                if (match.id == title.tmdbMatchId) Text(appString(R.string.tmdb_current_match),
                                    color = Fh.Accent, style = MaterialTheme.typography.labelSmall)
                            }
                        }
                    }
                }
            }
        },
        confirmButton = {
            TextButton(onClick = { scope.launch { search(query.trim(), year) } }, enabled = !busy && query.isNotBlank()) {
                Text(appString(R.string.tmdb_find))
            }
        },
        dismissButton = { TextButton(onClick = onDismiss, enabled = !busy) { Text(appString(R.string.action_cancel)) } },
    )
}


@Composable
private fun seasonLabel(season: Int): String =
    if (season > 0) appString(R.string.video_library_season, season) else appString(R.string.video_library_episodes)

@Composable
private fun CatalogPoster(title: VideoTitle, modifier: Modifier) {
    val first = title.videos.firstOrNull()
    val sources = posterSources(title) + listOfNotNull(first?.let { Api.videoFrameUrl(it.source, it.path, 360) })
    VideoPreview(sources.firstOrNull(), modifier, sources.drop(1))
}

private fun posterSources(title: VideoTitle): List<String> = listOfNotNull(
    title.videos.firstOrNull()?.let { first ->
        title.cover.takeIf(String::isNotBlank)?.let { Api.thumbUrl(first.source, it, 360) }
    },
    title.poster.takeIf { it.startsWith("https://static.tvmaze.com/") || it.startsWith("https://image.tmdb.org/t/p/") },
)

@Composable
internal fun VideoPreview(url: String?, modifier: Modifier, fallbacks: List<String> = emptyList()) {
    val sources = listOfNotNull(url) + fallbacks
    var index by remember(sources) { mutableIntStateOf(0) }
    val current = sources.getOrNull(index)
    Box(modifier.clip(RoundedCornerShape(8.dp)).background(Fh.Card), contentAlignment = Alignment.Center) {
        Icon(Icons.Rounded.PlayCircle, null, Modifier.size(32.dp), tint = Fh.Muted)
        AsyncImage(current, null, Modifier.matchParentSize(), contentScale = ContentScale.Crop,
            onError = { if (current != null && current == sources.getOrNull(index)) index++ })
    }
}
