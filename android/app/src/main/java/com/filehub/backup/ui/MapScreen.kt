package com.filehub.backup.ui

import android.graphics.Paint
import android.graphics.Typeface
import android.view.View
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.itemsIndexed
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Close
import androidx.compose.material.icons.rounded.PlayCircle
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.nativeCanvas
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import coil.compose.AsyncImage
import coil.request.ImageRequest
import com.filehub.backup.*
import com.filehub.backup.R
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.withContext
import org.maplibre.android.MapLibre
import org.maplibre.android.camera.CameraUpdateFactory
import org.maplibre.android.geometry.LatLng
import org.maplibre.android.geometry.LatLngBounds
import org.maplibre.android.maps.MapView
import org.maplibre.android.maps.MapLibreMap
import org.maplibre.android.maps.MapLibreMapOptions
import org.maplibre.android.maps.Style
import org.json.JSONArray
import java.time.Instant
import java.time.LocalDate
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.time.format.FormatStyle

private fun placeKey(p: Place) = JSONArray(listOf(p.source,p.path)).toString()
/** Native map lifecycle follows the host, and is released when this dialog leaves composition. */
@Composable
private fun rememberMapView(): MapView {
    val context = LocalContext.current
    val owner = LocalLifecycleOwner.current
    val view = remember {
        MapLibre.getInstance(context)
        // TextureView participates in the same view composition as the photo
        // markers drawn above the tile map.
        MapView(context, MapLibreMapOptions.createFromAttributes(context).textureMode(true)).apply { onCreate(null) }
    }
    DisposableEffect(view,owner) {
        var started=false;var resumed=false
        fun sync() {
            val state=owner.lifecycle.currentState
            if(state.isAtLeast(Lifecycle.State.STARTED) && !started){view.onStart();started=true}
            if(state.isAtLeast(Lifecycle.State.RESUMED) && !resumed){view.onResume();resumed=true}
            if(!state.isAtLeast(Lifecycle.State.RESUMED) && resumed){view.onPause();resumed=false}
            if(!state.isAtLeast(Lifecycle.State.STARTED) && started){view.onStop();started=false}
        }
        val observer=LifecycleEventObserver { _,_->sync() }
        owner.lifecycle.addObserver(observer);sync()
        onDispose {owner.lifecycle.removeObserver(observer);if(resumed)view.onPause();if(started)view.onStop();view.onDestroy()}
    }
    return view
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun MapScreen(onOpenPhotos: (List<Photo>,Int)->Unit, onClose: ()->Unit) {
    val resources = LocalAppResources.current
    val locale = resources.configuration.locales[0]
    val dateFormat = remember(locale) { DateTimeFormatter.ofLocalizedDate(FormatStyle.MEDIUM).withLocale(locale) }
    var places by remember { mutableStateOf<List<Place>>(emptyList()) }
    var styleJSON by remember { mutableStateOf<String?>(null) }
    var loading by remember { mutableStateOf(true) }
    var error by remember { mutableStateOf<Exception?>(null) }
    var retry by remember { mutableIntStateOf(0) }
    var from by rememberSaveable { mutableStateOf<String?>(null) }
    var to by rememberSaveable { mutableStateOf<String?>(null) }
    var picker by remember { mutableStateOf<String?>(null) }
    var selection by remember { mutableStateOf<List<Place>?>(null) }
    var map by remember { mutableStateOf<MapLibreMap?>(null) }
    var ready by remember { mutableStateOf(false) }
    var prepared by remember { mutableStateOf<PlacesOnMap?>(null) }
    val zone = remember { ZoneId.systemDefault() }
    LaunchedEffect(places, from, to) {
        prepared = withContext(Dispatchers.Default) {
            preparePlacesMap(places, from?.let(LocalDate::parse), to?.let(LocalDate::parse), zone)
        }
    }
    val shown = prepared?.places.orEmpty()
    LaunchedEffect(places, from, to) { selection = null }
    LaunchedEffect(retry) {
        loading=true;error=null;styleJSON=null;ready=false;selection=null
        try {
            val data=withContext(Dispatchers.IO) {Api.places() to Api.mapStyle()}
            places=data.first;styleJSON=data.second
        } catch(e:Exception) {
            if(e is CancellationException)throw e
            error=e
        } finally {loading=false}
    }
    Dialog(onDismissRequest=onClose,properties=DialogProperties(usePlatformDefaultWidth=false)) {
        Surface(Modifier.fillMaxSize(),color=Fh.Bg) {
            Column(Modifier.fillMaxSize().navigationBarsPadding()) {
                Row(Modifier.statusBarsPadding().fillMaxWidth().padding(18.dp,12.dp),verticalAlignment=Alignment.CenterVertically) {
                    FhRoundButton(onClick=onClose) {Icon(Icons.Rounded.Close,appString(R.string.action_close),Modifier.size(18.dp),tint=Fh.Text2)}
                    Column(Modifier.weight(1f).padding(horizontal=12.dp)) {
                        Text(appString(R.string.photo_ui_places),fontFamily=Golos,fontSize=22.sp,color=Fh.Text)
                        Text(appQuantity(R.plurals.photo_ui_geotagged_count, shown.size),fontFamily=Golos,fontSize=12.sp,color=Fh.Muted)
                    }
                }
                Row(Modifier.fillMaxWidth().padding(horizontal=18.dp),horizontalArrangement=Arrangement.spacedBy(8.dp)) {
                    FilterChip(selected=from!=null,onClick={picker="from"},label={
                        Text(from?.let { LocalDate.parse(it).format(dateFormat) } ?: appString(R.string.photo_ui_from_date))
                    })
                    FilterChip(selected=to!=null,onClick={picker="to"},label={
                        Text(to?.let { LocalDate.parse(it).format(dateFormat) } ?: appString(R.string.photo_ui_to_date))
                    })
                    if(from!=null || to!=null) TextButton(onClick={from=null;to=null}) {Text(appString(R.string.photo_ui_reset))}
                }
                if(loading) LinearProgressIndicator(Modifier.fillMaxWidth())
                error?.let { failure ->
                    Row(Modifier.padding(18.dp),verticalAlignment=Alignment.CenterVertically) {
                        Text(resources.requestError(failure, R.string.photo_ui_map_failed),
                            Modifier.weight(1f),color=MaterialTheme.colorScheme.error,fontFamily=Golos,fontSize=13.sp)
                        TextButton(onClick={retry++}) {Text(appString(R.string.photo_ui_retry))}
                    }
                }
                val visiblePlaces = prepared
                if (!loading && error == null && visiblePlaces != null && shown.isEmpty()) {
                    val message = if (places.isEmpty()) R.string.photo_ui_empty_map else R.string.photo_ui_empty_date_map
                    Text(appString(message), Modifier.padding(18.dp), color = Fh.Muted, fontFamily = Golos)
                }
                styleJSON?.let { json ->
                    val view = rememberMapView()
                    var markers by remember(view) { mutableStateOf<List<PlacesMarker>>(emptyList()) }
                    val density = LocalDensity.current
                    val clusterRadius = with(density) { 52.dp.toPx() }
                    val hitRadius = with(density) { 25.dp.toPx() }
                    DisposableEffect(view, json) {
                        var active = true
                        view.getMapAsync { instance ->
                            if (active) {
                                instance.setMaxZoomPreference(19.0)
                                instance.setStyle(Style.Builder().fromJson(json)) {
                                    if (active) {
                                        map = instance
                                        ready = true
                                    }
                                }
                            }
                        }
                        onDispose {
                            active = false
                            map = null
                            ready = false
                        }
                    }
                    DisposableEffect(view, map, visiblePlaces, clusterRadius, hitRadius) {
                        val instance = map
                        if (instance == null || visiblePlaces == null) {
                            onDispose {}
                        } else {
                            val projected = DoubleArray(visiblePlaces.coordinates.size)
                            var queued = false
                            var fitted = false
                            val render = Runnable {
                                queued = false
                                if (view.width > 0 && view.height > 0) {
                                    if (!fitted) {
                                        fitted = true
                                        fitPlaces(instance, visiblePlaces.places)
                                    }
                                    if (projected.isNotEmpty()) {
                                        instance.projection.toScreenLocations(visiblePlaces.coordinates, projected)
                                    }
                                    markers = groupPlaceMarkers(visiblePlaces.places, projected, view.width, view.height, clusterRadius)
                                }
                            }
                            fun requestRender() {
                                if (!queued) {
                                    queued = true
                                    view.postOnAnimation(render)
                                }
                            }
                            val move = MapLibreMap.OnCameraMoveListener { requestRender() }
                            val idle = MapLibreMap.OnCameraIdleListener { requestRender() }
                            val layout = View.OnLayoutChangeListener { _, _, _, _, _, _, _, _, _ -> requestRender() }
                            val click = MapLibreMap.OnMapClickListener { location ->
                                val point = instance.projection.toScreenLocation(location)
                                val hit = markerAt(markers, point.x, point.y, hitRadius)
                                if (hit == null) false else {
                                    selection = hit.photos
                                    if (hit.photos.size > 1) {
                                        instance.animateCamera(CameraUpdateFactory.newLatLngZoom(
                                            location, (instance.cameraPosition.zoom + 2.0).coerceAtMost(19.0)))
                                    }
                                    true
                                }
                            }
                            view.addOnLayoutChangeListener(layout)
                            instance.addOnCameraMoveListener(move)
                            instance.addOnCameraIdleListener(idle)
                            instance.addOnMapClickListener(click)
                            requestRender()
                            onDispose {
                                view.removeCallbacks(render)
                                view.removeOnLayoutChangeListener(layout)
                                instance.removeOnCameraMoveListener(move)
                                instance.removeOnCameraIdleListener(idle)
                                instance.removeOnMapClickListener(click)
                                markers = emptyList()
                            }
                        }
                    }
                    TextButton(
                        onClick = { selection = markers.flatMap { it.photos }.sortedByDescending { it.modTime } },
                        enabled = ready && shown.isNotEmpty(),
                        modifier = Modifier.align(Alignment.End),
                    ) { Text(appString(R.string.photo_ui_photos_in_area)) }
                    Box(Modifier.fillMaxWidth().weight(1f)) {
                        AndroidView(factory = { view }, modifier = Modifier.matchParentSize())
                        PlacesMarkerOverlay(markers, Modifier.matchParentSize())
                    }
                }
            }
        }
    }
    picker?.let { which ->
        val initial=(if(which=="from")from else to)?.let {LocalDate.parse(it).atStartOfDay(java.time.ZoneOffset.UTC).toInstant().toEpochMilli()}
        PhotoCalendarDialog(
            confirmLabel = R.string.photo_ui_choose_date,
            initialDateMillis = initial,
            onDismiss = { picker = null },
            onConfirm = { millis ->
                val date=millis?.let {Instant.ofEpochMilli(it).atZone(java.time.ZoneOffset.UTC).toLocalDate().toString()}
                if(which=="from"){from=date;if(date!=null && to!=null && date>to!!)to=null}else{to=date;if(date!=null && from!=null && date<from!!)from=null}
                picker = null
            },
        )
    }
    selection?.let { photos -> ClusterGrid(photos, onOpenPhotos, onClose = { selection = null }) }
}

private fun fitPlaces(map: MapLibreMap, places: List<Place>) {
    val first = places.firstOrNull() ?: return
    val position = LatLng(mapLatitude(first.lat), first.lon)
    if (places.all { mapLatitude(it.lat) == position.latitude && it.lon == position.longitude }) {
        map.moveCamera(CameraUpdateFactory.newLatLngZoom(position, 14.0))
    } else {
        val bounds = LatLngBounds.Builder().includes(places.map { LatLng(mapLatitude(it.lat), it.lon) }).build()
        map.moveCamera(CameraUpdateFactory.newLatLngBounds(bounds, 64))
    }
}

@Composable
private fun PlacesMarkerOverlay(markers: List<PlacesMarker>, modifier: Modifier = Modifier) {
    val labels = remember {
        Paint(Paint.ANTI_ALIAS_FLAG).apply {
            color = android.graphics.Color.WHITE
            typeface = Typeface.DEFAULT_BOLD
            textAlign = Paint.Align.CENTER
        }
    }
    Canvas(modifier) {
        labels.textSize = 13.sp.toPx()
        val textOffset = (labels.ascent() + labels.descent()) / 2f
        for (marker in markers) {
            val center = Offset(marker.x, marker.y)
            val radius = if (marker.photos.size > 1) 22.dp.toPx() else 9.dp.toPx()
            drawCircle(Color.White, radius, center)
            drawCircle(Color(0xFF006DE2), radius - 2.dp.toPx(), center)
            if (marker.photos.size > 1) {
                drawContext.canvas.nativeCanvas.drawText(marker.photos.size.toString(), marker.x, marker.y - textOffset, labels)
            }
        }
    }
}

@Composable
private fun ClusterGrid(places: List<Place>, onOpenPhotos: (List<Photo>, Int) -> Unit, onClose: () -> Unit) {
    val context=LocalContext.current
    var limit by remember(places) { mutableIntStateOf(240) }
    val visible = remember(places, limit) { places.take(limit) }
    var photos by remember(visible) { mutableStateOf<List<Photo>>(emptyList()) }
    LaunchedEffect(visible) {
        photos = withContext(Dispatchers.Default) { visible.map(Place::photo) }
    }
    Dialog(onDismissRequest=onClose,properties=DialogProperties(usePlatformDefaultWidth=false)) {
        Surface(Modifier.fillMaxSize(),color=Fh.Bg) {
            Column(Modifier.fillMaxSize().statusBarsPadding().navigationBarsPadding()) {
                Row(Modifier.padding(18.dp,12.dp),verticalAlignment=Alignment.CenterVertically) {
                    FhRoundButton(onClick=onClose) {Icon(Icons.Rounded.Close,appString(R.string.photo_ui_back),Modifier.size(18.dp),tint=Fh.Text2)}
                    Text(appString(R.string.photo_ui_area_progress, visible.size, places.size),Modifier.padding(start=12.dp),fontFamily=Golos,color=Fh.Text,fontSize=18.sp)
                }
                if(photos.isEmpty())Text(appString(R.string.photo_ui_empty_area),Modifier.padding(18.dp),color=Fh.Muted)
                if (visible.size < places.size) TextButton(onClick = { limit += 240 }) { Text(appString(R.string.photo_ui_load_more)) }
                LazyVerticalGrid(columns=GridCells.Fixed(3),horizontalArrangement=Arrangement.spacedBy(2.dp),verticalArrangement=Arrangement.spacedBy(2.dp)) {
                    itemsIndexed(visible,key={_,p->placeKey(p)}) {index,p->
                        Box(Modifier.aspectRatio(1f).background(Fh.Card).clickable(enabled=photos.size==visible.size) {onOpenPhotos(photos,index)}) {
                            AsyncImage(ImageRequest.Builder(context).data(Api.thumbUrl(p.source,p.path,320,p.revision)).size(320).crossfade(false).build(),contentDescription=p.name,contentScale=ContentScale.Crop,modifier=Modifier.matchParentSize())
                            if(p.isVideo)Icon(Icons.Rounded.PlayCircle,null,Modifier.align(Alignment.BottomEnd).padding(4.dp).size(20.dp),tint=Color.White)
                        }
                    }
                }
            }
        }
    }
}
