package com.filehub.backup.ui

import android.content.Intent
import androidx.compose.foundation.clickable
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.ArrowBack
import androidx.compose.material.icons.outlined.Add
import androidx.compose.material.icons.outlined.DeleteOutline
import androidx.compose.material.icons.outlined.LinkOff
import androidx.compose.material.icons.outlined.Share
import androidx.compose.material.icons.rounded.Close
import androidx.compose.material.icons.rounded.PlayCircle
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import coil.compose.AsyncImage
import coil.request.ImageRequest
import com.filehub.backup.Album
import com.filehub.backup.Api
import com.filehub.backup.Photo
import com.filehub.backup.R
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/** Picker shown when adding selected photos to an album (choose existing or create new). */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun AlbumPicker(photos: List<Photo>, onDone: () -> Unit) {
    val ctx = LocalContext.current
    val resources = LocalAppResources.current
    val scope = rememberCoroutineScope()
    var albums by remember { mutableStateOf<List<Album>>(emptyList()) }
    var creating by remember { mutableStateOf(false) }
    LaunchedEffect(Unit) { albums = withContext(Dispatchers.IO) { Api.albums() } }
    fun addAll(id: Long) {
        scope.launch {
            withContext(Dispatchers.IO) { photos.forEach { Api.albumAdd(id, it.source, it.path) } }
            android.widget.Toast.makeText(ctx, resources.getString(R.string.photo_ui_added_to_album_count, photos.size), android.widget.Toast.LENGTH_SHORT).show()
            onDone()
        }
    }
    Dialog(onDismissRequest = onDone) {
        Surface(shape = Fh.CardShape, color = Fh.Card, border = androidx.compose.foundation.BorderStroke(1.dp, Fh.Border)) {
            Column(Modifier.padding(vertical = 14.dp).widthIn(min = 280.dp)) {
                Text(appString(R.string.photo_ui_add_to_album_count, photos.size), Modifier.padding(horizontal = 20.dp, vertical = 6.dp),
                    fontFamily = Golos, fontSize = 16.5.sp, fontWeight = FontWeight.Bold, color = Fh.Text)
                LazyColumn(Modifier.heightIn(max = 320.dp)) {
                    items(albums, key = { it.id }) { a ->
                        Column(Modifier.fillMaxWidth().clickable { addAll(a.id) }) {
                            HorizontalDivider(color = Fh.Divider)
                            Column(Modifier.padding(horizontal = 20.dp, vertical = 12.dp)) {
                                Text(a.name, maxLines = 1, overflow = TextOverflow.Ellipsis,
                                    fontFamily = Golos, fontSize = 14.5.sp, fontWeight = FontWeight.SemiBold, color = Fh.Text)
                                Text(appQuantity(R.plurals.photo_ui_image_count, a.count), fontFamily = Golos, fontSize = 12.sp, color = Fh.Muted)
                            }
                        }
                    }
                }
                HorizontalDivider(color = Fh.Divider)
                Surface(
                    onClick = { creating = true },
                    modifier = Modifier.padding(horizontal = 16.dp).padding(top = 12.dp).fillMaxWidth(),
                    shape = RoundedCornerShape(14.dp), color = Fh.AccentSoft,
                ) {
                    Row(Modifier.padding(vertical = 12.dp), verticalAlignment = Alignment.CenterVertically,
                        horizontalArrangement = Arrangement.Center) {
                        Icon(Icons.Outlined.Add, null, Modifier.size(18.dp), tint = Fh.AccentDeep)
                        Spacer(Modifier.width(6.dp))
                        Text(appString(R.string.photo_ui_new_album), fontFamily = Golos, fontSize = 14.sp, fontWeight = FontWeight.Bold, color = Fh.AccentDeep)
                    }
                }
            }
        }
    }
    if (creating) TextPrompt(appString(R.string.photo_ui_album_name), "") { name ->
        creating = false
        if (!name.isNullOrBlank()) scope.launch {
            val id = withContext(Dispatchers.IO) { Api.albumCreate(name) }
            if (id != null) addAll(id) else onDone()
        }
    }
}

/** Albums: a grid of user collections; tap one to see its photos. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun AlbumsScreen(onOpenPhotos: (List<Photo>, Int) -> Unit, onClose: () -> Unit) {
    val ctx = LocalContext.current
    val scope = rememberCoroutineScope()
    var albums by remember { mutableStateOf<List<Album>>(emptyList()) }
    var loading by remember { mutableStateOf(true) }
    var open by remember { mutableStateOf<Album?>(null) }
    var creating by remember { mutableStateOf(false) }

    fun reload() { scope.launch { loading = true; albums = withContext(Dispatchers.IO) { Api.albums() }; loading = false } }
    LaunchedEffect(Unit) { reload() }

    Dialog(onDismissRequest = onClose, properties = DialogProperties(usePlatformDefaultWidth = false)) {
        Surface(Modifier.fillMaxSize(), color = Fh.Bg) {
            val cur = open
            if (cur == null) {
                Column(Modifier.fillMaxSize()) {
                    Row(
                        Modifier.statusBarsPadding().fillMaxWidth().padding(horizontal = 18.dp, vertical = 12.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        FhRoundButton(onClick = onClose) { Icon(Icons.Rounded.Close, appString(R.string.action_close), Modifier.size(18.dp), tint = Fh.Text2) }
                        Text(appString(R.string.photo_ui_albums), Modifier.weight(1f).padding(horizontal = 12.dp),
                            fontFamily = Golos, fontSize = 22.sp, fontWeight = FontWeight.ExtraBold,
                            letterSpacing = (-0.5).sp, color = Fh.Text)
                        FhRoundButton(onClick = { creating = true }) { Icon(Icons.Outlined.Add, appString(R.string.photo_ui_new_album), Modifier.size(18.dp), tint = Fh.Text2) }
                    }
                    if (loading) LinearProgressIndicator(Modifier.fillMaxWidth(), color = Fh.Accent, trackColor = Fh.AccentSoft)
                    if (!loading && albums.isEmpty()) Box(Modifier.fillMaxSize().padding(28.dp), contentAlignment = Alignment.Center) {
                        Text(appString(R.string.photo_ui_empty_albums),
                            fontFamily = Golos, fontSize = 13.5.sp, color = Fh.Muted)
                    } else LazyVerticalGrid(columns = GridCells.Fixed(2), modifier = Modifier.fillMaxSize(),
                        contentPadding = PaddingValues(start = 18.dp, end = 18.dp, top = 4.dp, bottom = 24.dp),
                        horizontalArrangement = Arrangement.spacedBy(12.dp), verticalArrangement = Arrangement.spacedBy(14.dp)) {
                        items(albums, key = { it.id }) { a ->
                            Column(Modifier.clickable { open = a }) {
                                Box(Modifier.fillMaxWidth().aspectRatio(1f).clip(RoundedCornerShape(16.dp)).background(Fh.BgAlt)) {
                                    if (a.coverPath.isNotEmpty()) AsyncImage(
                                        ImageRequest.Builder(ctx).data(Api.thumbUrl(a.coverSource, a.coverPath, 320)).crossfade(false).build(),
                                        contentDescription = null, contentScale = ContentScale.Crop, modifier = Modifier.matchParentSize())
                                }
                                Text(a.name, Modifier.padding(start = 4.dp, top = 7.dp), fontFamily = Golos, fontSize = 14.sp,
                                    fontWeight = FontWeight.SemiBold, color = Fh.Text, maxLines = 1, overflow = TextOverflow.Ellipsis)
                                Text(appQuantity(R.plurals.photo_ui_image_count, a.count), Modifier.padding(start = 4.dp), fontFamily = Golos, fontSize = 12.sp, color = Fh.Muted)
                            }
                        }
                    }
                }
            } else {
                AlbumView(cur, onOpenPhotos = onOpenPhotos, onDeleted = { open = null; reload() }, onBack = { open = null; reload() })
            }
        }
    }

    if (creating) TextPrompt(appString(R.string.photo_ui_album_name), "") { name ->
        creating = false
        if (!name.isNullOrBlank()) scope.launch { withContext(Dispatchers.IO) { Api.albumCreate(name) }; reload() }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun AlbumView(album: Album, onOpenPhotos: (List<Photo>, Int) -> Unit, onDeleted: () -> Unit, onBack: () -> Unit) {
    val ctx = LocalContext.current
    val resources = LocalAppResources.current
    val scope = rememberCoroutineScope()
    var photos by remember { mutableStateOf<List<Photo>>(emptyList()) }
    var loading by remember { mutableStateOf(true) }
    var confirmDel by remember { mutableStateOf(false) }
    var shareActive by remember(album.id) { mutableStateOf(album.shareActive) }
    LaunchedEffect(album.id) { photos = withContext(Dispatchers.IO) { Api.albumPhotos(album.id) }; loading = false }
    fun shareAlbum() {
        scope.launch {
            val url = withContext(Dispatchers.IO) { Api.albumShare(album.id) }
            if (url == null) { android.widget.Toast.makeText(ctx, resources.getString(R.string.photo_ui_link_failed), android.widget.Toast.LENGTH_SHORT).show(); return@launch }
            shareActive = true
            val send = Intent(Intent.ACTION_SEND).apply { type = "text/plain"; putExtra(Intent.EXTRA_TEXT, url) }
            runCatching { ctx.startActivity(Intent.createChooser(send, resources.getString(R.string.photo_ui_album_link_title, album.name))) }
        }
    }

    Column(Modifier.fillMaxSize()) {
        Row(
            Modifier.statusBarsPadding().fillMaxWidth().padding(horizontal = 18.dp, vertical = 12.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            FhRoundButton(onClick = onBack) { Icon(Icons.AutoMirrored.Rounded.ArrowBack, appString(R.string.photo_ui_back), Modifier.size(18.dp), tint = Fh.Text2) }
            Column(Modifier.weight(1f).padding(horizontal = 12.dp)) {
                Text(album.name, fontFamily = Golos, fontSize = 18.sp, fontWeight = FontWeight.Bold,
                    color = Fh.Text, maxLines = 1, overflow = TextOverflow.Ellipsis)
                Text(appQuantity(R.plurals.photo_ui_image_count, album.count), fontFamily = Golos, fontSize = 12.sp, color = Fh.Muted)
            }
            FhRoundButton(onClick = { shareAlbum() }) { Icon(Icons.Outlined.Share, appString(R.string.photo_ui_share_album), Modifier.size(17.dp), tint = Fh.Text2) }
            if (shareActive) {
                Spacer(Modifier.width(8.dp))
                FhRoundButton(onClick = {
                    scope.launch {
                        val revoked = withContext(Dispatchers.IO) { Api.albumRevokeLinks(album.id) }
                        if (revoked) shareActive = false
                        val message = if (revoked) R.string.photo_ui_album_links_revoked else R.string.photo_ui_album_revoke_failed
                        android.widget.Toast.makeText(ctx, resources.getString(message), android.widget.Toast.LENGTH_SHORT).show()
                    }
                }) { Icon(Icons.Outlined.LinkOff, appString(R.string.photo_ui_album_revoke), Modifier.size(17.dp), tint = Fh.Text2) }
            }
            Spacer(Modifier.width(8.dp))
            FhRoundButton(onClick = { confirmDel = true }) { Icon(Icons.Outlined.DeleteOutline, appString(R.string.photo_ui_delete_album), Modifier.size(18.dp), tint = Fh.Text2) }
        }
        if (loading) LinearProgressIndicator(Modifier.fillMaxWidth(), color = Fh.Accent, trackColor = Fh.AccentSoft)
        if (!loading && photos.isEmpty()) Box(Modifier.fillMaxSize().padding(28.dp), contentAlignment = Alignment.Center) {
            Text(appString(R.string.photo_ui_empty_album), fontFamily = Golos, fontSize = 13.5.sp, color = Fh.Muted)
        } else LazyVerticalGrid(columns = GridCells.Fixed(4), modifier = Modifier.fillMaxSize(),
            horizontalArrangement = Arrangement.spacedBy(2.dp), verticalArrangement = Arrangement.spacedBy(2.dp)) {
            items(photos, key = { it.source + "|" + it.path }) { p ->
                Box(Modifier.aspectRatio(1f).background(Fh.BgAlt).clickable { onOpenPhotos(photos, photos.indexOf(p)) }) {
                    AsyncImage(
                        ImageRequest.Builder(ctx).data(Api.photoThumbUrl(p,192)).size(192).crossfade(false).build(),
                        contentDescription = null, contentScale = ContentScale.Crop, modifier = Modifier.matchParentSize())
                    if (p.isVideo) Icon(Icons.Rounded.PlayCircle, null, Modifier.align(Alignment.BottomEnd).padding(4.dp).size(20.dp), tint = Color.White)
                }
            }
        }
    }
    if (confirmDel) AlertDialog(onDismissRequest = { confirmDel = false },
        shape = RoundedCornerShape(22.dp), containerColor = Fh.Card, titleContentColor = Fh.Text, textContentColor = Fh.Text2,
        title = { Text(appString(R.string.photo_ui_delete_named_album, album.name), fontFamily = Golos, fontWeight = FontWeight.Bold) },
        text = { Text(appString(R.string.photo_ui_delete_album_hint), fontFamily = Golos) },
        confirmButton = { TextButton(onClick = { confirmDel = false; scope.launch { withContext(Dispatchers.IO) { Api.albumDelete(album.id) }; onDeleted() } }) {
            Text(appString(R.string.photo_ui_delete), fontFamily = Golos, fontWeight = FontWeight.Bold, color = MaterialTheme.colorScheme.error) } },
        dismissButton = { TextButton(onClick = { confirmDel = false }) { Text(appString(R.string.action_cancel), fontFamily = Golos, color = Fh.Muted) } })
}
