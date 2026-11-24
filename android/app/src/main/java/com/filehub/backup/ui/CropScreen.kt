package com.filehub.backup.ui

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.gestures.detectDragGestures
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Close
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.IntSize
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import coil.compose.AsyncImage
import coil.imageLoader
import coil.request.ImageRequest
import com.filehub.backup.Api
import com.filehub.backup.R
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

private fun hitHandle(c: Rect, p: Offset, r: Float): Int {
    val corners = listOf(Offset(c.left, c.top), Offset(c.right, c.top), Offset(c.left, c.bottom), Offset(c.right, c.bottom))
    corners.forEachIndexed { i, o -> if ((p - o).getDistance() < r) return i }
    return if (c.contains(p)) 4 else -1
}

private fun adjust(c: Rect, handle: Int, d: Offset, bounds: Rect, minSz: Float): Rect {
    var (l, t, rr, b) = listOf(c.left, c.top, c.right, c.bottom)
    when (handle) {
        0 -> { l += d.x; t += d.y }
        1 -> { rr += d.x; t += d.y }
        2 -> { l += d.x; b += d.y }
        3 -> { rr += d.x; b += d.y }
        4 -> {
            var nl = l + d.x; var nt = t + d.y; var nr = rr + d.x; var nb = b + d.y
            if (nl < bounds.left) { nr += bounds.left - nl; nl = bounds.left }
            if (nr > bounds.right) { nl -= nr - bounds.right; nr = bounds.right }
            if (nt < bounds.top) { nb += bounds.top - nt; nt = bounds.top }
            if (nb > bounds.bottom) { nt -= nb - bounds.bottom; nb = bounds.bottom }
            return Rect(nl, nt, nr, nb)
        }
    }
    l = l.coerceIn(bounds.left, rr - minSz); t = t.coerceIn(bounds.top, b - minSz)
    rr = rr.coerceIn(l + minSz, bounds.right); b = b.coerceIn(t + minSz, bounds.bottom)
    return Rect(l, t, rr, b)
}

/** Full-screen crop: drag corners to resize, drag inside to move. Sends fractional
 *  crop to the server which crops the full-res original (no quality loss). */
@Composable
fun CropScreen(source: String, path: String, onCropped: () -> Unit, onClose: () -> Unit) {
    val ctx = LocalContext.current
    val resources = LocalAppResources.current
    val scope = rememberCoroutineScope()
    var container by remember { mutableStateOf(IntSize.Zero) }
    var imgDim by remember { mutableStateOf(IntSize.Zero) }
    var crop by remember { mutableStateOf<Rect?>(null) }
    var saving by remember { mutableStateOf(false) }
    val handleR = with(LocalDensity.current) { 30.dp.toPx() }
    val minSz = with(LocalDensity.current) { 56.dp.toPx() }

    val imageRect: Rect? = remember(container, imgDim) {
        if (container.width == 0 || imgDim.width == 0) null
        else {
            val cw = container.width.toFloat(); val ch = container.height.toFloat()
            val s = minOf(cw / imgDim.width, ch / imgDim.height)
            val dw = imgDim.width * s; val dh = imgDim.height * s
            Rect((cw - dw) / 2, (ch - dh) / 2, (cw - dw) / 2 + dw, (ch - dh) / 2 + dh)
        }
    }
    LaunchedEffect(imageRect) { if (imageRect != null && crop == null) crop = imageRect }

    fun save() {
        val ir = imageRect ?: return; val cr = crop ?: return
        saving = true
        val fx = ((cr.left - ir.left) / ir.width).coerceIn(0f, 1f)
        val fy = ((cr.top - ir.top) / ir.height).coerceIn(0f, 1f)
        val fw = (cr.width / ir.width).coerceIn(0.02f, 1f)
        val fh = (cr.height / ir.height).coerceIn(0.02f, 1f)
        scope.launch {
            val ok = withContext(Dispatchers.IO) { Api.crop(source, path, fx, fy, fw, fh) }
            if (ok) {
                withContext(Dispatchers.IO) {
                    val il = ctx.imageLoader
                    il.memoryCache?.clear()
                    il.diskCache?.remove("$source|$path@192v2")
                    il.diskCache?.remove(Api.thumbUrl(source, path, 1280))
                }
                android.widget.Toast.makeText(ctx, resources.getString(R.string.photo_ui_cropped), android.widget.Toast.LENGTH_SHORT).show()
                onCropped()
            } else { saving = false; android.widget.Toast.makeText(ctx, resources.getString(R.string.photo_ui_crop_failed), android.widget.Toast.LENGTH_SHORT).show() }
        }
    }

    Dialog(onDismissRequest = onClose, properties = DialogProperties(usePlatformDefaultWidth = false)) {
        Box(Modifier.fillMaxSize().background(Fh.Dark)) {
            Box(Modifier.fillMaxSize().padding(top = 56.dp, bottom = 16.dp).onSizeChanged { container = it }) {
                AsyncImage(
                    ImageRequest.Builder(ctx).data(Api.thumbUrl(source, path, 1280)).build(),
                    contentDescription = null, contentScale = ContentScale.Fit, modifier = Modifier.fillMaxSize(),
                    onSuccess = { imgDim = IntSize(it.result.drawable.intrinsicWidth, it.result.drawable.intrinsicHeight) }
                )
                val ir = imageRect; val cr = crop
                if (ir != null && cr != null) {
                    Box(Modifier.fillMaxSize().pointerInput(ir) {
                        var handle = -1
                        detectDragGestures(
                            onDragStart = { p -> handle = hitHandle(cr, p, handleR) },
                            onDragEnd = { handle = -1 }, onDragCancel = { handle = -1 }
                        ) { change, d -> if (handle >= 0) { change.consume(); crop = crop?.let { adjust(it, handle, d, ir, minSz) } } }
                    })
                    Canvas(Modifier.fillMaxSize()) {
                        val dim = Color.Black.copy(alpha = 0.5f)
                        drawRect(dim, Offset(0f, 0f), Size(size.width, cr.top))
                        drawRect(dim, Offset(0f, cr.bottom), Size(size.width, size.height - cr.bottom))
                        drawRect(dim, Offset(0f, cr.top), Size(cr.left, cr.height))
                        drawRect(dim, Offset(cr.right, cr.top), Size(size.width - cr.right, cr.height))
                        drawRect(Color.White, Offset(cr.left, cr.top), Size(cr.width, cr.height), style = Stroke(width = 3f))
                        listOf(Offset(cr.left, cr.top), Offset(cr.right, cr.top), Offset(cr.left, cr.bottom), Offset(cr.right, cr.bottom))
                            .forEach { drawCircle(Color.White, 13f, it) }
                    }
                }
            }
            Row(Modifier.align(Alignment.TopStart).statusBarsPadding().fillMaxWidth().padding(horizontal = 12.dp, vertical = 6.dp),
                verticalAlignment = Alignment.CenterVertically) {
                Surface(onClick = onClose, shape = CircleShape, color = Color.White.copy(alpha = 0.10f), modifier = Modifier.size(48.dp)) {
                    Box(contentAlignment = Alignment.Center) {
                        Icon(Icons.Rounded.Close, appString(R.string.action_cancel), Modifier.size(18.dp), tint = Color.White)
                    }
                }
                Text(appString(R.string.photo_ui_cropping), color = Color.White, fontFamily = Golos, fontSize = 15.sp, fontWeight = FontWeight.SemiBold,
                    modifier = Modifier.weight(1f).padding(horizontal = 12.dp))
                TextButton(onClick = { save() }, enabled = !saving) {
                    Text(if (saving) "…" else appString(R.string.action_done), fontFamily = Golos, fontSize = 15.sp, fontWeight = FontWeight.Bold,
                        color = if (saving) Color.White.copy(alpha = 0.55f) else Color(0xFF66AFFF))
                }
            }
        }
    }
}
