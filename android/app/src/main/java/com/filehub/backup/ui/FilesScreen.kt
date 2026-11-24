package com.filehub.backup.ui

import android.content.Intent
import android.net.Uri
import android.provider.OpenableColumns
import androidx.activity.compose.BackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.annotation.StringRes
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.slideInVertically
import androidx.compose.animation.slideOutVertically
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.ArrowBack
import androidx.compose.material.icons.automirrored.filled.KeyboardArrowRight
import androidx.compose.material.icons.automirrored.outlined.Send
import androidx.compose.material.icons.filled.CheckCircle
import androidx.compose.material.icons.outlined.*
import androidx.compose.material.icons.rounded.Add
import androidx.compose.material.icons.rounded.Close
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import coil.compose.AsyncImage
import coil.request.ImageRequest
import com.filehub.backup.*
import com.filehub.backup.R
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.text.NumberFormat

private val IMG = setOf("jpg", "jpeg", "png", "gif", "webp", "bmp")
private val DOC = setOf("pdf", "doc", "docx", "odt", "rtf", "xls", "xlsx", "ods", "ppt", "pptx", "odp")
private val TEXT = setOf(
    "txt", "md", "markdown", "log", "csv", "tsv", "json", "xml", "yml", "yaml", "ini", "conf", "cfg",
    "toml", "env", "properties", "gradle", "sh", "bash", "zsh", "bat", "ps1", "py", "js", "mjs", "ts",
    "jsx", "tsx", "go", "rs", "c", "h", "cpp", "cc", "hpp", "cs", "java", "kt", "kts", "swift", "rb",
    "php", "pl", "lua", "sql", "html", "htm", "css", "scss", "less", "vue", "srt", "vtt", "tex", "r")
private fun ext(n: String) = n.substringAfterLast('.', "").lowercase()
private fun isImage(n: String) = ext(n) in IMG
private fun isDoc(n: String) = ext(n) in DOC
private fun isText(n: String): Boolean {
    if (ext(n) in TEXT) return true
    return n.lowercase() in setOf("makefile", "dockerfile", "readme", ".gitignore", ".env", "license")
}

@Composable
private fun PersonalQuotaRow(quota: Api.PersonalQuota) {
    val resources = LocalAppResources.current
    Column(Modifier.fillMaxWidth().padding(horizontal = 4.dp, vertical = 8.dp),
        verticalArrangement = Arrangement.spacedBy(3.dp)) {
        Text(appString(R.string.personal_quota_usage,
            localizedSize(resources, quota.used), localizedSize(resources, quota.limit), localizedSize(resources, quota.available)),
            color = Fh.Text, fontSize = 13.sp)
        if (quota.reserved > 0) {
            Text(appString(R.string.personal_quota_reserved, localizedSize(resources, quota.reserved)),
                color = Fh.Muted, fontSize = 12.sp)
        }
    }
}

@StringRes
private fun fileSearchTypeLabel(type: String) = when (type) {
    "image" -> R.string.file_search_images
    "video" -> R.string.file_search_videos
    "audio" -> R.string.file_search_audio
    "document" -> R.string.file_search_documents
    "archive" -> R.string.file_search_archives
    "folder" -> R.string.file_ui_folders
    else -> R.string.nav_files
}

@Composable
private fun fmtDate(t: Long): String {
    if (t <= 0) return ""
    val d = java.time.Instant.ofEpochSecond(t).atZone(java.time.ZoneId.systemDefault()).toLocalDate()
    val now = java.time.LocalDate.now()
    val locale = LocalAppResources.current.configuration.locales[0]
    return when (d) {
        now -> appString(R.string.file_ui_today)
        now.minusDays(1) -> appString(R.string.file_ui_yesterday)
        else -> {
            val skeleton = if (d.year == now.year) "MMMd" else "yMMMd"
            val pattern = android.text.format.DateFormat.getBestDateTimePattern(locale, skeleton)
            d.format(java.time.format.DateTimeFormatter.ofPattern(pattern, locale))
        }
    }
}

// Tinted extension-badge palette from the design handoff («ЭКРАН: ФАЙЛЫ»).
private fun badgeColors(e: String): Pair<Color, Color> = when (e) {
    "png" -> Color(0xFFD7F0FA) to Color(0xFF00688F)
    "mp4", "mov", "mkv", "avi", "webm", "m4v", "wmv", "flv", "mpg", "mpeg" -> Color(0xFFF3E5FB) to Color(0xFF8324A8)
    "zip", "rar", "7z", "gz", "tar", "xz", "bz2" -> Color(0xFFF6ECD4) to Color(0xFF8A6A00)
    "pdf" -> Color(0xFFFBE4DE) to Color(0xFFB3341F)
    "txt", "doc", "docx", "md", "rtf", "odt", "log", "csv" -> Color(0xFFD8F1EC) to Color(0xFF00745F)
    else -> Color(0xFFDCEDFF) to Color(0xFF005ECB) // jpg/jpeg + default: blue
}

/** 42dp rounded square: folder icon for dirs, uppercase extension badge for files. */
@Composable
private fun ExtBadge(name: String, isDir: Boolean = false) {
    if (isDir) {
        Box(Modifier.size(42.dp).clip(RoundedCornerShape(12.dp)).background(Fh.AccentSoft), contentAlignment = Alignment.Center) {
            Icon(Icons.Outlined.Folder, null, Modifier.size(20.dp), tint = Fh.AccentDeep)
        }
    } else {
        val e = ext(name)
        val (bg, fg) = badgeColors(e)
        Box(Modifier.size(42.dp).clip(RoundedCornerShape(12.dp)).background(bg), contentAlignment = Alignment.Center) {
            Text(
                if (e.isEmpty()) appString(R.string.file_ui_extension_missing) else e.uppercase().take(4),
                color = fg, fontSize = 10.5.sp, fontWeight = FontWeight.ExtraBold,
                fontFamily = Golos, letterSpacing = 0.4.sp, maxLines = 1)
        }
    }
}

// Rows inside one visual «card»: first row gets the top corners, the last row the
// bottom corners, middle rows are separated by a hairline divider.
private fun groupShape(first: Boolean, last: Boolean) = RoundedCornerShape(
    topStart = if (first) 16.dp else 0.dp, topEnd = if (first) 16.dp else 0.dp,
    bottomStart = if (last) 16.dp else 0.dp, bottomEnd = if (last) 16.dp else 0.dp)

@Composable
private fun GroupedRow(first: Boolean, last: Boolean, content: @Composable () -> Unit) {
    Column(Modifier.fillMaxWidth().clip(groupShape(first, last)).background(Fh.Card)) {
        if (!first) HorizontalDivider(color = Fh.Divider, thickness = 1.dp)
        content()
    }
}

@OptIn(ExperimentalMaterial3Api::class, ExperimentalLayoutApi::class)
@Composable
fun FilesScreen(onOpenDownloads: () -> Unit) {
    val ctx = LocalContext.current
    val resources = LocalAppResources.current
    val latestResources by rememberUpdatedState(resources)
    val scope = rememberCoroutineScope()
    val snackbar = remember { SnackbarHostState() }

    var source by rememberSaveable { mutableStateOf<String?>(null) }
    var path by rememberSaveable { mutableStateOf("") }
    val model:FileListingModel=viewModel(key="files:${Prefs(ctx).accountScope()}")
    var sources by model.sources
    var entries by model.entries
    var loading by model.loading
    var quota by remember(model) { mutableStateOf<Api.PersonalQuota?>(null) }
    var quotaRefresh by remember { mutableIntStateOf(0) }

    var sheetItem by remember { mutableStateOf<Entry?>(null) }
    var viewer by remember { mutableStateOf<Entry?>(null) }
    var docViewer by remember { mutableStateOf<Entry?>(null) }
    var textViewer by remember { mutableStateOf<Entry?>(null) }
    // search across filenames + document text + image OCR
    var searching by rememberSaveable { mutableStateOf(false) }
    var searchQ by rememberSaveable { mutableStateOf("") }
    var searchResults by remember { mutableStateOf<Api.FileSearchResult?>(null) }
    var literalSearch by rememberSaveable { mutableStateOf(false) }
    var searchBusy by remember { mutableStateOf(false) }
    // viewers opened directly from a search hit (by absolute source+path)
    var hitImage by remember { mutableStateOf<FileHit?>(null) }
    var hitText by remember { mutableStateOf<FileHit?>(null) }
    var hitDoc by remember { mutableStateOf<FileHit?>(null) }
    var playVideo by rememberSaveable { mutableStateOf<ArrayList<String>?>(null) } // (source, fullPath, name)
    var showTrash by remember { mutableStateOf(false) }
    // Recent files open in place like a normal folder.
    var virtual by remember { mutableStateOf<String?>(null) } // null | "recents"
    var recentsList by remember { mutableStateOf<List<RecentFile>>(emptyList()) }
    var virtualLoading by remember { mutableStateOf(false) }
    var online by model.online
    var loadFailed by model.failed
    var infoItem by remember { mutableStateOf<Entry?>(null) }
    var newFolder by remember { mutableStateOf(false) }
    var renameItem by remember { mutableStateOf<Entry?>(null) }
    var addMenu by remember { mutableStateOf(false) }
    var confirmDel by remember { mutableStateOf(false) }
    val sel = rememberSelection()
    fun keyOf(e: Entry) = (if (e.isDir) "d:" else "f:") + e.name

    fun toast(s: String) = scope.launch { snackbar.showSnackbar(s) }
    fun toast(@StringRes id: Int, vararg args: Any) = toast(latestResources.getString(id, *args))
    fun reload() { model.load(source,path,force=true); quotaRefresh++ }
    LaunchedEffect(source,path){sel.clear();model.load(source,path,force=false)}
    LaunchedEffect(model,source,path,quotaRefresh) {
        quota = withContext(Dispatchers.IO) { Api.personalQuota() }
    }

    fun selectedEntries() = entries.filter { keyOf(it) in sel.ids }
    fun batchDownload() {
        val items = selectedEntries().filter { !it.isDir }; sel.clear()
        if (items.isEmpty()) return
        scope.launch {
            withContext(Dispatchers.IO) {
                for (e in items) Downloader.toDevice(ctx, source!!, joinPath(path, e.name), e.name)
            }
            toast(latestResources.getQuantityString(R.plurals.file_ui_download_started, items.size, items.size))
        }
    }
    fun batchDelete() {
        val items = selectedEntries(); sel.clear()
        scope.launch {
            val ok = withContext(Dispatchers.IO) { items.count { Api.delete(source!!, joinPath(path, it.name)) } }
            toast(R.string.file_ui_trashed_count, ok, items.size); reload()
        }
    }
    var moveItems by remember { mutableStateOf<List<Entry>>(emptyList()) }
    var moveCopy by remember { mutableStateOf(false) }
    var showMovePicker by remember { mutableStateOf(false) }
    fun startMove(copy: Boolean) { moveItems = selectedEntries(); moveCopy = copy; if (moveItems.isNotEmpty()) showMovePicker = true }
    fun doMove(ds: String, dp: String) {
        val items = moveItems; val copy = moveCopy
        showMovePicker = false; sel.clear()
        scope.launch {
            val ok = withContext(Dispatchers.IO) {
                items.count { e ->
                    val pth = joinPath(path, e.name)
                    if (copy) Api.copy(source!!, pth, ds, dp) else Api.move(source!!, pth, ds, dp)
                }
            }
            val result = if (copy) R.string.file_ui_copied_count else R.string.file_ui_moved_count
            toast(result, ok, items.size); reload()
        }
    }

    // Send a file out to another app (Telegram, mail, …) via the system share sheet.
    fun shareFile(src: String, full: String, name: String) {
        scope.launch {
            toast(R.string.file_ui_share_preparing)
            val uri = withContext(Dispatchers.IO) { Downloader.cacheFile(ctx, src, full, name) }
            if (uri == null) { toast(R.string.file_ui_share_prepare_failed); return@launch }
            val send = Intent(Intent.ACTION_SEND).apply {
                type = ctx.contentResolver.getType(uri) ?: "*/*"
                putExtra(Intent.EXTRA_STREAM, uri)
                addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
            }
            runCatching { ctx.startActivity(Intent.createChooser(send, latestResources.getString(R.string.file_ui_share_named, name))) }
        }
    }
    fun shareEntry(e: Entry) = shareFile(source!!, joinPath(path, e.name), e.name)
    // Open a file with whatever app the system has for its type (handles formats filehub can't preview).
    fun openFileExternally(src: String, full: String, name: String) {
        scope.launch {
            toast(R.string.file_ui_opening)
            val uri = withContext(Dispatchers.IO) { Downloader.cacheFile(ctx, src, full, name) }
            if (uri == null) { toast(R.string.file_ui_open_failed); return@launch }
            val view = Intent(Intent.ACTION_VIEW).apply {
                setDataAndType(uri, ctx.contentResolver.getType(uri) ?: "*/*")
                addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
            }
            runCatching { ctx.startActivity(view) }.onFailure { toast(R.string.file_ui_no_app) }
        }
    }
    fun openExternally(e: Entry) = openFileExternally(source!!, joinPath(path, e.name), e.name)

    fun closeSearch() { searching = false; searchQ = ""; literalSearch = false; searchResults = null }
    fun openHit(h: FileHit) {
        when {
            h.isDir -> { source = h.source; path = h.path; closeSearch() }
            isImage(h.name) -> hitImage = h
            isVideoName(h.name) -> playVideo = arrayListOf(h.source, h.path, h.name)
            isText(h.name) -> hitText = h
            isDoc(h.name) -> hitDoc = h
            else -> openFileExternally(h.source, h.path, h.name)
        }
    }
    LaunchedEffect(searchQ, literalSearch) {
        val q = searchQ.trim()
        val useLiteral = literalSearch
        searchResults = null
        if (q.length < 2) { searchBusy = false; return@LaunchedEffect }
        searchBusy = true
        kotlinx.coroutines.delay(300)
        searchResults = withContext(Dispatchers.IO) { Api.searchFiles(q, useLiteral) }
        searchBusy = false
    }

    LaunchedEffect(virtual) {
        if (virtual == "recents") {
            virtualLoading = true
            recentsList = withContext(Dispatchers.IO) { Api.recents() }
            virtualLoading = false
        }
    }
    fun openRecent(r: RecentFile) {
        val parent = r.path.substringBeforeLast('/', "")
        virtual = null; source = r.source; path = parent
    }
    BackHandler(enabled = virtual != null && !searching) { virtual = null }
    BackHandler(enabled = searching) { closeSearch() }
    BackHandler(enabled = source != null && virtual == null && !searching) {
        if (path.isNotEmpty()) path = path.substringBeforeLast('/', "") else source = null
    }
    BackHandler(enabled = sel.inSelection) { sel.clear() }

    val picker = rememberLauncherForActivityResult(ActivityResultContracts.GetMultipleContents()) { uris ->
        val src = source ?: return@rememberLauncherForActivityResult
        if (uris.isEmpty()) return@rememberLauncherForActivityResult
        val destination = path
        scope.launch {
            loading = true
            var ok = 0
            try {
                withContext(Dispatchers.IO) {
                    for (uri in uris) {
                        val name = displayName(ctx, uri)
                        val isVid = (ctx.contentResolver.getType(uri) ?: "").startsWith("video")
                        if (Api.upload(src, destination, name, ctx.contentResolver, uri, isVid)) ok++
                    }
                }
                toast(R.string.file_ui_uploaded_count, ok)
            } catch (error: UploadException) {
                toast(latestResources.transferError(error, R.string.transfer_ui_write_failed))
            } finally {
                loading = false
                reload()
            }
        }
    }

    Scaffold(
        containerColor = Fh.Bg,
        // toasts must clear the floating pill nav
        snackbarHost = { SnackbarHost(snackbar) },
        topBar = {
            if (searching) {
                Column {
                    SearchField(searchQ, onChange = {
                        searchQ = it
                        if (it.isBlank()) literalSearch = false
                    }, onClose = { closeSearch() })
                    if (searchQ.trim().length >= 2) {
                        FlowRow(Modifier.padding(horizontal = 16.dp), horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                            searchResults?.intent?.let { intent ->
                                if (intent.type.isNotEmpty()) {
                                    AssistChip(onClick = { searchQ = intent.withoutType },
                                        label = { Text(appString(fileSearchTypeLabel(intent.type))) },
                                        trailingIcon = { Icon(Icons.Rounded.Close, appString(R.string.file_search_remove_filter), Modifier.size(16.dp)) })
                                }
                                if (intent.year > 0) {
                                    AssistChip(onClick = { searchQ = intent.withoutYear },
                                        label = { Text(appString(R.string.file_search_modified_year, intent.year.toString())) },
                                        trailingIcon = { Icon(Icons.Rounded.Close, appString(R.string.file_search_remove_filter), Modifier.size(16.dp)) })
                                }
                            }
                            FilterChip(
                                selected = literalSearch, onClick = { literalSearch = !literalSearch },
                                label = { Text(appString(R.string.file_search_literal)) },
                            )
                        }
                    }
                    if (searchBusy) LinearProgressIndicator(Modifier.fillMaxWidth(), color = Fh.Accent, trackColor = Fh.AccentSoft)
                }
            } else if (sel.inSelection) {
                SelectionHeader(sel.count, onClose = { sel.clear() },
                    onSelectAll = { sel.selectAll(entries.map { keyOf(it) }) })
            } else if (virtual != null) {
                FolderHeader(
                    title = appString(R.string.file_ui_recent),
                    crumb = null, loading = virtualLoading, onBack = { virtual = null })
            } else if (source == null) {
                val total = sources.sumOf { it.total }
                val used = sources.sumOf { it.total - it.free }
                RootHeader(
                    subtitle = if (total > 0) appString(
                        R.string.file_ui_storage_summary, localizedSize(resources, total), localizedSize(resources, used)
                    ) else null,
                    loading = loading,
                    onRecents = { virtual = "recents" },
                    onOffline = onOpenDownloads,
                    onSearch = { searching = true },
                    onRefresh = { reload() })
            } else {
                val parent = path.substringBeforeLast('/', "")
                val root = appString(R.string.file_ui_my_drive)
                val crumb = if (path.isEmpty()) root
                else "$root / " + source!! + (if (parent.isEmpty()) "" else " / " + parent.replace("/", " / "))
                FolderHeader(
                    title = if (path.isEmpty()) source!! else path.substringAfterLast('/'),
                    crumb = crumb, loading = loading,
                    onBack = { if (path.isNotEmpty()) path = path.substringBeforeLast('/', "") else source = null }
                ) {
                    FhRoundButton(onClick = { searching = true }) { Icon(Icons.Outlined.Search, appString(R.string.file_ui_search), Modifier.size(17.dp), tint = Fh.Text2) }
                    FhRoundButton(onClick = { showTrash = true }) { Icon(Icons.Outlined.Delete, appString(R.string.file_ui_trash), Modifier.size(17.dp), tint = Fh.Text2) }
                    FhRoundButton(onClick = { reload() }) { Icon(Icons.Outlined.Refresh, appString(R.string.file_ui_refresh), Modifier.size(17.dp), tint = Fh.Text2) }
                }
            }
        },
        bottomBar = {
            AnimatedVisibility(sel.inSelection, enter = slideInVertically { it }, exit = slideOutVertically { it }) {
                // floats above the pill nav like the prototype's FAB
                FhCard(Modifier.padding(horizontal = 18.dp).padding(bottom = 8.dp).fillMaxWidth()) {
                    Row(Modifier.fillMaxWidth().padding(vertical = 6.dp), horizontalArrangement = Arrangement.SpaceEvenly) {
                        SelAction(Icons.Outlined.Download, appString(R.string.file_ui_download)) { batchDownload() }
                        SelAction(Icons.Outlined.DriveFileMove, appString(R.string.file_ui_move)) { startMove(false) }
                        SelAction(Icons.Outlined.ContentCopy, appString(R.string.file_ui_copy)) { startMove(true) }
                        SelAction(Icons.Outlined.Delete, appString(R.string.file_ui_delete), tint = MaterialTheme.colorScheme.error) { confirmDel = true }
                    }
                }
            }
        },
        floatingActionButton = {
            if (source != null && !sel.inSelection) Surface(
                onClick = { addMenu = true },
                modifier = Modifier
                    .padding(bottom = 8.dp)
                    .size(56.dp)
                    .shadow(3.dp, Fh.CardShape),
                shape = Fh.CardShape, color = Fh.Accent
            ) {
                Box(contentAlignment = Alignment.Center) {
                    Icon(Icons.Rounded.Add, appString(R.string.file_ui_add), Modifier.size(24.dp), tint = Fh.OnAccent)
                }
            }
        }
    ) { pad ->
        if (searching) {
            val res = searchResults?.files
            when {
                searchQ.trim().length < 2 -> Box(Modifier.fillMaxSize().padding(pad).padding(28.dp), contentAlignment = Alignment.Center) {
                    Text(appString(R.string.file_ui_search_help),
                        color = Fh.Muted, fontSize = 13.5.sp, lineHeight = 19.sp)
                }
                res == null -> {}
                res.isEmpty() -> Box(Modifier.fillMaxSize().padding(pad), contentAlignment = Alignment.Center) {
                    Text(appString(R.string.file_ui_no_results), color = Fh.Muted)
                }
                else -> LazyColumn(
                    Modifier.fillMaxSize().padding(pad),
                    contentPadding = PaddingValues(start = 18.dp, end = 18.dp, top = 8.dp, bottom = 24.dp),
                    verticalArrangement = Arrangement.spacedBy(8.dp)
                ) {
                    items(res, key = { it.source + "|" + it.path }) { h -> SearchHitRow(h) { openHit(h) } }
                }
            }
        } else if (virtual == "recents") {
            if (recentsList.isEmpty() && !virtualLoading) {
                Box(Modifier.fillMaxSize().padding(pad).padding(28.dp), contentAlignment = Alignment.Center) {
                    Text(appString(R.string.file_ui_recent_empty), color = Fh.Muted, fontSize = 13.5.sp)
                }
            } else LazyColumn(
                Modifier.fillMaxSize().padding(pad),
                contentPadding = PaddingValues(start = 18.dp, end = 18.dp, top = 8.dp, bottom = 24.dp)
            ) {
                itemsIndexed(recentsList, key = { _, r -> r.source + "|" + r.path }) { i, r ->
                    GroupedRow(first = i == 0, last = i == recentsList.lastIndex) {
                        Row(Modifier.fillMaxWidth().clickable { openRecent(r) }.padding(horizontal = 14.dp, vertical = 12.dp),
                            verticalAlignment = Alignment.CenterVertically) {
                            ExtBadge(r.name)
                            Spacer(Modifier.width(12.dp))
                            Column(Modifier.weight(1f)) {
                                Text(r.name, fontSize = 14.sp, fontWeight = FontWeight.SemiBold, color = Fh.Text,
                                    maxLines = 1, overflow = TextOverflow.Ellipsis)
                                FileOwnerLabel(r.owner)
                                Text("${r.source} · ${localizedSize(resources, r.size)}", Modifier.padding(top = 2.dp), fontSize = 12.sp, color = Fh.Muted)
                            }
                        }
                    }
                }
            }
        } else if (source == null) {
            LazyColumn(
                Modifier.fillMaxSize().padding(pad),
                contentPadding = PaddingValues(start = 18.dp, end = 18.dp, top = 12.dp, bottom = 24.dp),
                verticalArrangement = Arrangement.spacedBy(10.dp)
            ) {
                if (!loading && sources.isEmpty()) item {
                    FhEmptyState(Icons.Outlined.Storage, if (!online) appString(R.string.file_ui_server_unavailable) else if (loadFailed) appString(R.string.file_ui_storage_load_failed) else appString(R.string.file_ui_storage_empty),
                        if (!loadFailed) appString(R.string.file_ui_storage_empty_help) else appString(R.string.file_ui_retry_help),
                        actionLabel = appString(R.string.file_ui_refresh), onAction = { reload() })
                }
                quota?.let { usage -> item(key = "personal-quota") { PersonalQuotaRow(usage) } }
                items(sources, key = { it.name }) { s -> DiskCard(s) { source = s.name; path = "" } }
            }
        } else {
            val (dirs, files) = remember(entries) { entries.partition { it.isDir } }
            LazyColumn(
                Modifier.fillMaxSize().padding(pad),
                contentPadding = PaddingValues(start = 18.dp, end = 18.dp, top = 4.dp, bottom = 24.dp)
            ) {
                quota?.let { usage -> item(key = "personal-quota") { PersonalQuotaRow(usage) } }
                if (dirs.isNotEmpty()) {
                    item(key = "sec-dirs") { FhSectionLabel(appString(R.string.file_ui_folders), Modifier.padding(start = 4.dp, top = 6.dp, bottom = 8.dp)) }
                    itemsIndexed(dirs, key = { _, e -> keyOf(e) }) { i, e ->
                        val id = keyOf(e)
                        GroupedRow(first = i == 0, last = i == dirs.lastIndex) {
                            EntryRow(source!!, path, e, selected = id in sel.ids, inSelection = sel.inSelection,
                                onClick = {
                                    if (sel.inSelection) sel.toggle(id)
                                    else path = if (path.isEmpty()) e.name else "$path/${e.name}"
                                },
                                onLongClick = { sel.toggle(id) },
                                onShare = { shareEntry(e) },
                                onMenu = { sheetItem = e })
                        }
                    }
                }
                if (files.isNotEmpty()) {
                    item(key = "sec-files") { FhSectionLabel(appString(R.string.nav_files), Modifier.padding(start = 4.dp, top = 16.dp, bottom = 8.dp)) }
                    itemsIndexed(files, key = { _, e -> keyOf(e) }) { i, e ->
                        val id = keyOf(e)
                        GroupedRow(first = i == 0, last = i == files.lastIndex) {
                            EntryRow(source!!, path, e, selected = id in sel.ids, inSelection = sel.inSelection,
                                onClick = {
                                    if (sel.inSelection) sel.toggle(id)
                                    else if (isImage(e.name)) viewer = e
                                    else if (isVideoName(e.name)) playVideo = arrayListOf(source!!, joinPath(path, e.name), e.name)
                                    else if (isText(e.name)) textViewer = e
                                    else if (isDoc(e.name)) docViewer = e
                                    else openExternally(e)
                                },
                                onLongClick = { if (sel.inSelection) sel.toggle(id) else sheetItem = e },
                                onShare = { shareEntry(e) },
                                onMenu = { sheetItem = e })
                        }
                    }
                }
                if (!loading && entries.isEmpty()) item {
                    FhEmptyState(Icons.Outlined.Folder, if (!loadFailed) appString(R.string.file_ui_folder_empty) else appString(R.string.file_ui_file_load_failed),
                        if (!loadFailed) appString(R.string.file_ui_folder_empty_help) else appString(R.string.file_ui_retry_help),
                        actionLabel = if (!loadFailed) null else appString(R.string.file_ui_retry), onAction = if (!loadFailed) null else ({ reload() }))
                }
            }
        }
    }

    // add menu
    if (addMenu) ModalBottomSheet(onDismissRequest = { addMenu = false }, containerColor = Fh.Card) {
        SheetAction(appString(R.string.file_ui_new_folder), Icons.Outlined.CreateNewFolder) { addMenu = false; newFolder = true }
        SheetAction(appString(R.string.file_ui_upload_files), Icons.Outlined.Upload) { addMenu = false; picker.launch("*/*") }
        Spacer(Modifier.navigationBarsPadding().height(12.dp))
    }

    // per-item actions
    sheetItem?.let { e ->
        val storage = source ?: return@let
        val fullPath = joinPath(path, e.name)
        ModalBottomSheet(onDismissRequest = { sheetItem = null }, containerColor = Fh.Card) {
            Column(Modifier.verticalScroll(rememberScrollState())) {
                Row(Modifier.fillMaxWidth().padding(horizontal = 20.dp, vertical = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                    ExtBadge(e.name, e.isDir)
                    Spacer(Modifier.width(12.dp))
                    Column(Modifier.weight(1f)) {
                        Text(e.name, fontSize = 15.sp, fontWeight = FontWeight.Bold, color = Fh.Text,
                            maxLines = 1, overflow = TextOverflow.Ellipsis)
                        Text(if (e.isDir) appString(R.string.file_ui_folder) else localizedSize(resources, e.size), Modifier.padding(top = 2.dp), fontSize = 12.sp, color = Fh.Muted)
                    }
                }
                HorizontalDivider(Modifier.padding(vertical = 10.dp), color = Fh.Divider)
                if (!e.isDir) SheetAction(appString(R.string.file_ui_download_to_device), Icons.Outlined.Download) {
                    sheetItem = null
                    scope.launch { toast(withContext(Dispatchers.IO) { Downloader.toDevice(ctx, storage, fullPath, e.name) }) }
                }
                if (!e.isDir) {
                    val pinned = Offline.isPinned(ctx, storage, fullPath)
                    SheetAction(
                        appString(if (pinned) R.string.file_ui_offline_remove else R.string.file_ui_offline_save),
                        if (pinned) Icons.Outlined.CloudDone else Icons.Outlined.CloudDownload,
                    ) {
                        sheetItem = null
                        if (pinned) {
                            toast(if (Offline.unpin(ctx, storage, fullPath)) R.string.file_ui_offline_removed
                                else R.string.downloads_remove_failed)
                        } else scope.launch {
                            OfflineDownload.enqueue(ctx, storage, fullPath)
                            onOpenDownloads()
                        }
                    }
                }
                if (!e.isDir) SheetAction(appString(R.string.file_ui_share_file), Icons.AutoMirrored.Outlined.Send) {
                    val item = e; sheetItem = null; shareEntry(item)
                }
                if (!e.isDir && isVideoName(e.name)) SheetAction(appString(R.string.file_ui_stream_in_player), Icons.Outlined.OpenInNew) {
                    sheetItem = null
                    scope.launch {
                        val url = withContext(Dispatchers.IO) { Api.streamUrl(storage, fullPath) }
                        if (url == null) { toast(R.string.file_ui_link_create_failed); return@launch }
                        val mime = java.net.URLConnection.guessContentTypeFromName(e.name) ?: "video/*"
                        runCatching { ctx.startActivity(Intent.createChooser(Intent(Intent.ACTION_VIEW).apply { setDataAndType(android.net.Uri.parse(url), mime) }, latestResources.getString(R.string.file_ui_open_player))) }
                            .onFailure { toast(R.string.file_ui_no_player) }
                    }
                }
                if (!e.isDir && !isVideoName(e.name)) SheetAction(appString(R.string.file_ui_open_in_app), Icons.Outlined.OpenInNew) {
                    val item = e; sheetItem = null; openExternally(item)
                }
                SheetAction(appString(R.string.file_ui_create_link), Icons.Outlined.Link) {
                    sheetItem = null
                    scope.launch {
                        val url = withContext(Dispatchers.IO) { Api.shareCreate(storage, fullPath) }
                        if (url == null) { toast(R.string.file_ui_link_create_failed); return@launch }
                        val send = Intent(Intent.ACTION_SEND).apply { type = "text/plain"; putExtra(Intent.EXTRA_TEXT, url) }
                        runCatching { ctx.startActivity(Intent.createChooser(send, latestResources.getString(R.string.file_ui_link))) }
                    }
                }
                SheetAction(appString(R.string.file_ui_select_items), Icons.Outlined.SelectAll) {
                    sheetItem = null
                    sel.toggle(keyOf(e))
                }
                SheetAction(appString(R.string.file_ui_rename), Icons.Outlined.DriveFileRenameOutline) { renameItem = e; sheetItem = null }
                SheetAction(appString(R.string.file_ui_properties), Icons.Outlined.Info) { infoItem = e; sheetItem = null }
                SheetAction(appString(R.string.file_ui_delete), Icons.Outlined.Delete,
                    iconTint = MaterialTheme.colorScheme.error, textColor = MaterialTheme.colorScheme.error) {
                    sheetItem = null
                    scope.launch {
                        val ok = withContext(Dispatchers.IO) { Api.delete(storage, fullPath) }
                        toast(if (ok) R.string.file_ui_moved_to_trash else R.string.file_ui_operation_failed); reload()
                    }
                }
                Spacer(Modifier.navigationBarsPadding().height(12.dp))
            }
        }
    }

    if (newFolder) TextPrompt(appString(R.string.file_ui_new_folder_name), "") { name ->
        newFolder = false
        if (name != null) scope.launch {
            val ok = withContext(Dispatchers.IO) { Api.mkdir(source!!, path, name) }
            toast(if (ok) R.string.file_ui_folder_created else R.string.file_ui_operation_failed); reload()
        }
    }
    renameItem?.let { e ->
        TextPrompt(appString(R.string.file_ui_new_name), e.name) { name ->
            renameItem = null
            if (name != null && name != e.name) scope.launch {
                val ok = withContext(Dispatchers.IO) { Api.rename(source!!, joinPath(path, e.name), name) }
                toast(if (ok) R.string.file_ui_renamed else R.string.file_ui_operation_failed); reload()
            }
        }
    }
    viewer?.let { e -> ImageViewer(Api.downloadUrl(source!!, joinPath(path, e.name), inline = true), e.name) { viewer = null } }
    docViewer?.let { e ->
        DocViewer(source!!, joinPath(path, e.name), e.name,
            onDownload = { scope.launch { toast(withContext(Dispatchers.IO) { Downloader.toDevice(ctx, source!!, joinPath(path, e.name), e.name) }) } }
        ) { docViewer = null }
    }
    textViewer?.let { e ->
        TextViewer(source!!, joinPath(path, e.name), e.name,
            onDownload = { scope.launch { toast(withContext(Dispatchers.IO) { Downloader.toDevice(ctx, source!!, joinPath(path, e.name), e.name) }) } }
        ) { textViewer = null }
    }
    // viewers opened straight from a search hit (absolute source+path, no navigation)
    hitImage?.let { h -> ImageViewer(Api.downloadUrl(h.source, h.path, inline = true), h.name) { hitImage = null } }
    hitText?.let { h ->
        TextViewer(h.source, h.path, h.name,
            onDownload = { scope.launch { toast(withContext(Dispatchers.IO) { Downloader.toDevice(ctx, h.source, h.path, h.name) }) } }
        ) { hitText = null }
    }
    hitDoc?.let { h ->
        DocViewer(h.source, h.path, h.name,
            onDownload = { scope.launch { toast(withContext(Dispatchers.IO) { Downloader.toDevice(ctx, h.source, h.path, h.name) }) } }
        ) { hitDoc = null }
    }
    playVideo?.let { (s, p, n) ->
        VideoScreen(s, p, n,
            onDownloadOffline = {
                OfflineDownload.enqueue(ctx, s, p)
                onOpenDownloads()
            }
        ) { playVideo = null }
    }

    if (showTrash && source != null) TrashScreen(source!!, onClose = { showTrash = false }, onChanged = { reload() })

    if (showMovePicker) FolderPickerDialog(
        onPick = { s, p -> doMove(s, p) },
        onDismiss = { showMovePicker = false })


    infoItem?.let { e -> InfoDialog(source!!, joinPath(path, e.name)) { infoItem = null } }

    if (confirmDel) AlertDialog(onDismissRequest = { confirmDel = false },
        containerColor = Fh.Card, shape = Fh.CardShape,
        title = { Text(appQuantity(R.plurals.file_ui_trash_confirmation_count, sel.count)) },
        text = { Text(appString(R.string.file_ui_trash_confirmation_help)) },
        confirmButton = { TextButton(onClick = { confirmDel = false; batchDelete() }) { Text(appString(R.string.file_ui_delete)) } },
        dismissButton = { TextButton(onClick = { confirmDel = false }) { Text(appString(R.string.action_cancel)) } })
}

private fun joinPath(path: String, name: String) = if (path.isEmpty()) name else "$path/$name"

private fun displayName(ctx: android.content.Context, uri: Uri): String {
    var name = "upload_${System.currentTimeMillis()}"
    try {
        ctx.contentResolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)?.use { c ->
            if (c.moveToFirst()) { val i = c.getColumnIndex(OpenableColumns.DISPLAY_NAME); if (i >= 0) c.getString(i)?.let { name = it } }
        }
    } catch (_: Exception) {}
    return name
}

// ─── Headers ───

/** Root header: big «Файлы» title, storage subtitle, round action buttons. */
@Composable
private fun RootHeader(
    subtitle: String?, loading: Boolean,
    onRecents: () -> Unit, onOffline: () -> Unit, onSearch: () -> Unit, onRefresh: () -> Unit,
) {
    Column(Modifier.fillMaxWidth().background(Fh.Bg)) {
        Row(
            Modifier.padding(start = 18.dp, end = 18.dp, top = 16.dp, bottom = 8.dp).fillMaxWidth(),
            verticalAlignment = Alignment.Bottom
        ) {
            Column(Modifier.weight(1f)) {
                Text(appString(R.string.nav_files), fontSize = 26.sp, fontWeight = FontWeight.Bold, fontFamily = Golos,
                    letterSpacing = (-0.5).sp, color = Fh.Text)
                if (subtitle != null) Text(subtitle, Modifier.padding(top = 3.dp), fontSize = 12.5.sp, color = Fh.Muted)
            }
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                FhRoundButton(onClick = onSearch) { Icon(Icons.Outlined.Search, appString(R.string.file_ui_search), Modifier.size(17.dp), tint = Fh.Text2) }
                FhRoundButton(onClick = onRefresh) { Icon(Icons.Outlined.Refresh, appString(R.string.file_ui_refresh), Modifier.size(17.dp), tint = Fh.Text2) }
            }
        }
        Row(Modifier.padding(horizontal = 10.dp), horizontalArrangement = Arrangement.spacedBy(4.dp)) {
            TextButton(onClick = onRecents, modifier = Modifier.heightIn(min = 48.dp)) {
                Icon(Icons.Outlined.History, null, Modifier.size(18.dp)); Spacer(Modifier.width(8.dp)); Text(appString(R.string.file_ui_recent))
            }
            TextButton(onClick = onOffline, modifier = Modifier.heightIn(min = 48.dp)) {
                Icon(Icons.Outlined.Download, null, Modifier.size(18.dp)); Spacer(Modifier.width(8.dp)); Text(appString(R.string.downloads_title))
            }
        }
        if (loading) LinearProgressIndicator(Modifier.fillMaxWidth(), color = Fh.Accent, trackColor = Fh.AccentSoft)
    }
}

/** Folder / virtual-list header: back button, title + breadcrumb, optional round actions. */
@Composable
private fun FolderHeader(
    title: String, crumb: String?, loading: Boolean, onBack: () -> Unit,
    actions: @Composable () -> Unit = {},
) {
    Column(Modifier.fillMaxWidth().background(Fh.Bg.copy(alpha = 0.92f))) {
        Row(
            Modifier.padding(start = 14.dp, end = 14.dp, top = 12.dp, bottom = 10.dp).fillMaxWidth(),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(9.dp)
        ) {
            FhRoundButton(onClick = onBack) { Icon(Icons.AutoMirrored.Outlined.ArrowBack, appString(R.string.file_ui_back), Modifier.size(18.dp), tint = Fh.Text2) }
            Column(Modifier.weight(1f)) {
                Text(title, fontSize = 17.sp, fontWeight = FontWeight.Bold, fontFamily = Golos, color = Fh.Text,
                    maxLines = 1, overflow = TextOverflow.Ellipsis)
                if (crumb != null) Text(crumb, fontSize = 11.5.sp, color = Fh.Muted, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
            actions()
        }
        if (loading) LinearProgressIndicator(Modifier.fillMaxWidth(), color = Fh.Accent, trackColor = Fh.AccentSoft)
    }
}

/** Multi-select header: close, count, select-all. */
@Composable
private fun SelectionHeader(count: Int, onClose: () -> Unit, onSelectAll: () -> Unit) {
    Row(
        Modifier.fillMaxWidth().background(Fh.Bg)
            .padding(start = 14.dp, end = 14.dp, top = 12.dp, bottom = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(10.dp)
    ) {
        FhRoundButton(onClick = onClose) { Icon(Icons.Rounded.Close, appString(R.string.action_cancel), Modifier.size(18.dp), tint = Fh.Text2) }
        Text(NumberFormat.getIntegerInstance(LocalAppResources.current.configuration.locales[0]).format(count), Modifier.weight(1f), fontSize = 17.sp, fontWeight = FontWeight.Bold, fontFamily = Golos, color = Fh.Text)
        FhRoundButton(onClick = onSelectAll) { Icon(Icons.Outlined.SelectAll, appString(R.string.file_ui_select_all), Modifier.size(18.dp), tint = Fh.Text2) }
    }
}

// ─── Pieces ───

@Composable
private fun SelAction(icon: ImageVector, label: String, tint: Color = Fh.Text2, onClick: () -> Unit) {
    Column(Modifier.clip(RoundedCornerShape(12.dp)).clickable { onClick() }.padding(horizontal = 8.dp, vertical = 6.dp),
        horizontalAlignment = Alignment.CenterHorizontally) {
        Icon(icon, label, Modifier.size(21.dp), tint = tint)
        Text(label, Modifier.padding(top = 3.dp), fontSize = 11.5.sp, fontWeight = FontWeight.Medium, color = tint)
    }
}

/** Bottom-sheet action row (icon + label), replaces the old ListItem styling. */
@Composable
private fun SheetAction(
    text: String, icon: ImageVector,
    iconTint: Color = Fh.AccentDeep, textColor: Color = Fh.Text,
    onClick: () -> Unit,
) {
    Row(Modifier.fillMaxWidth().clickable(onClick = onClick).heightIn(min = 48.dp).padding(horizontal = 22.dp, vertical = 13.dp),
        verticalAlignment = Alignment.CenterVertically) {
        Icon(icon, null, Modifier.size(21.dp), tint = iconTint)
        Spacer(Modifier.width(16.dp))
        Text(text, fontSize = 14.5.sp, fontWeight = FontWeight.Medium, color = textColor)
    }
}

@Composable
private fun SearchField(value: String, onChange: (String) -> Unit, onClose: () -> Unit) {
    val focus = remember { FocusRequester() }
    LaunchedEffect(Unit) { runCatching { focus.requestFocus() } }
    Row(
        Modifier.fillMaxWidth().background(Fh.Bg)
            .padding(start = 14.dp, end = 14.dp, top = 12.dp, bottom = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(10.dp)
    ) {
        FhRoundButton(onClick = onClose) { Icon(Icons.AutoMirrored.Outlined.ArrowBack, appString(R.string.file_ui_close_search), Modifier.size(18.dp), tint = Fh.Text2) }
        FhCard(Modifier.weight(1f), shape = RoundedCornerShape(16.dp)) {
            TextField(
                value = value, onValueChange = onChange,
                modifier = Modifier.fillMaxWidth().focusRequester(focus),
                placeholder = { Text(appString(R.string.file_ui_search_placeholder), fontSize = 13.5.sp, color = Fh.Muted, maxLines = 1, overflow = TextOverflow.Ellipsis) },
                singleLine = true,
                textStyle = LocalTextStyle.current.copy(fontSize = 14.sp, fontFamily = Golos),
                leadingIcon = { Icon(Icons.Outlined.Search, null, Modifier.size(18.dp), tint = Fh.Muted) },
                trailingIcon = { if (value.isNotEmpty()) IconButton(onClick = { onChange("") }) { Icon(Icons.Rounded.Close, appString(R.string.file_ui_search_clear), tint = Fh.Muted) } },
                colors = TextFieldDefaults.colors(
                    focusedContainerColor = Color.Transparent, unfocusedContainerColor = Color.Transparent,
                    focusedIndicatorColor = Color.Transparent, unfocusedIndicatorColor = Color.Transparent,
                    cursorColor = Fh.Accent)
            )
        }
    }
}

@Composable
private fun FileOwnerLabel(owner: String) {
    if (owner.isBlank()) return
    Text(appString(R.string.file_owner_label, owner), Modifier.padding(top = 2.dp),
        fontSize = 12.sp, color = Fh.Muted, maxLines = 1, overflow = TextOverflow.Ellipsis)
}

@Composable
private fun SearchHitRow(h: FileHit, onClick: () -> Unit) {
    FhCard(shape = RoundedCornerShape(16.dp)) {
        Row(Modifier.fillMaxWidth().clickable(onClick = onClick).padding(12.dp), verticalAlignment = Alignment.CenterVertically) {
            ExtBadge(h.name, h.isDir)
            Spacer(Modifier.width(12.dp))
            Column(Modifier.weight(1f)) {
                Text(h.name, fontSize = 14.sp, fontWeight = FontWeight.SemiBold, color = Fh.Text,
                    maxLines = 1, overflow = TextOverflow.Ellipsis)
                FileOwnerLabel(h.owner)
                Text("${h.source} · /${h.path}", Modifier.padding(top = 2.dp), fontSize = 11.5.sp, color = Fh.Muted,
                    maxLines = 1, overflow = TextOverflow.Ellipsis)
                if (!h.excerpt.isNullOrBlank()) Text(
                    h.excerpt.replace('\n', ' ').trim(), Modifier.padding(top = 3.dp),
                    fontSize = 12.sp, maxLines = 2, overflow = TextOverflow.Ellipsis, color = Fh.Text2)
            }
            if (!h.excerpt.isNullOrBlank()) {
                Spacer(Modifier.width(8.dp))
                Icon(Icons.Outlined.Subject, appString(R.string.file_ui_text_match), Modifier.size(18.dp), tint = Fh.Accent)
            }
        }
    }
}

@Composable
private fun DiskCard(s: Source, onClick: () -> Unit) {
    val resources = LocalAppResources.current
    val used = if (s.total > 0) (1f - s.free.toFloat() / s.total).coerceIn(0f, 1f) else 0f
    val usedPercent = NumberFormat.getPercentInstance(resources.configuration.locales[0])
        .format((used * 100).toInt() / 100.0)
    FhCard {
        Row(Modifier.fillMaxWidth().clickable(onClick = onClick).padding(16.dp), verticalAlignment = Alignment.CenterVertically) {
            Box(Modifier.size(46.dp).clip(RoundedCornerShape(14.dp)).background(Fh.AccentSoft), contentAlignment = Alignment.Center) {
                Icon(Icons.Outlined.Storage, null, Modifier.size(22.dp), tint = Fh.AccentDeep)
            }
            Spacer(Modifier.width(14.dp))
            Column(Modifier.weight(1f)) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(s.name, Modifier.weight(1f), fontSize = 15.5.sp, fontWeight = FontWeight.Bold, fontFamily = Golos,
                        color = Fh.Text, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    Text(appString(R.string.file_ui_storage_percent, usedPercent), fontSize = 12.sp, fontWeight = FontWeight.SemiBold, color = Fh.Muted)
                }
                Spacer(Modifier.height(7.dp))
                Box(Modifier.fillMaxWidth().height(6.dp).clip(RoundedCornerShape(99.dp)).background(Fh.BgAlt)) {
                    Box(Modifier.fillMaxWidth(used).fillMaxHeight().clip(RoundedCornerShape(99.dp)).background(Fh.Accent))
                }
                Spacer(Modifier.height(7.dp))
                Text(
                    appString(
                        R.string.file_ui_storage_breakdown,
                        localizedSize(resources, s.total - s.free),
                        localizedSize(resources, s.total),
                        localizedSize(resources, s.free)
                    ),
                    fontSize = 12.5.sp, color = Fh.Muted
                )
            }
            Spacer(Modifier.width(10.dp))
            Icon(Icons.AutoMirrored.Filled.KeyboardArrowRight, null, Modifier.size(18.dp), tint = Fh.Chevron)
        }
    }
}

@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun EntryRow(source: String, path: String, e: Entry, selected: Boolean, inSelection: Boolean, onClick: () -> Unit, onLongClick: () -> Unit, onShare: () -> Unit, onMenu: () -> Unit) {
    val ctx = LocalContext.current
    val resources = LocalAppResources.current
    Row(
        Modifier.fillMaxWidth()
            .combinedClickable(onClick = onClick, onLongClick = onLongClick)
            .background(if (selected) Fh.AccentSoft else Color.Transparent)
            .padding(start = 14.dp, end = 4.dp, top = 9.dp, bottom = 9.dp),
        verticalAlignment = Alignment.CenterVertically
    ) {
        when {
            inSelection -> Box(Modifier.size(42.dp), contentAlignment = Alignment.Center) {
                Icon(if (selected) Icons.Filled.CheckCircle else Icons.Outlined.RadioButtonUnchecked, null,
                    Modifier.size(26.dp), tint = if (selected) Fh.Accent else Fh.Muted)
            }
            !e.isDir && isImage(e.name) -> AsyncImage(
                ImageRequest.Builder(ctx).data(Api.thumbUrl(source, joinPath(path, e.name),revision="${e.modTime}-${e.size}"))
                    .memoryCacheKey(com.filehub.backup.Prefs(ctx).accountScope() + "|" + source + "|" + joinPath(path, e.name)+"|${e.modTime}-${e.size}").crossfade(true).build(),
                contentDescription = null, modifier = Modifier.size(42.dp).clip(RoundedCornerShape(12.dp)),
                contentScale = androidx.compose.ui.layout.ContentScale.Crop)
            else -> ExtBadge(e.name, e.isDir)
        }
        Spacer(Modifier.width(12.dp))
        Column(Modifier.weight(1f)) {
            Text(e.name, fontSize = 14.sp, fontWeight = FontWeight.SemiBold, color = Fh.Text,
                maxLines = 1, overflow = TextOverflow.Ellipsis)
            FileOwnerLabel(e.owner)
            val sub = if (e.isDir) fmtDate(e.modTime)
            else listOf(fmtDate(e.modTime), localizedSize(resources, e.size)).filter { it.isNotBlank() }.joinToString(" · ")
            if (sub.isNotBlank()) Text(sub, Modifier.padding(top = 2.dp), fontSize = 12.sp, color = Fh.Muted)
        }
        if (!inSelection) Row(verticalAlignment = Alignment.CenterVertically) {
            if (!e.isDir) IconButton(onClick = onShare) {
                Icon(Icons.AutoMirrored.Outlined.Send, appString(R.string.file_ui_send), Modifier.size(20.dp), tint = Fh.AccentDeep)
            }
            IconButton(onClick = onMenu) { Icon(Icons.Outlined.MoreVert, appString(R.string.file_ui_actions_menu), tint = Fh.Muted) }
            if (e.isDir) Icon(Icons.AutoMirrored.Filled.KeyboardArrowRight, null,
                Modifier.padding(end = 8.dp).size(18.dp), tint = Fh.Chevron)
        }
    }
}
