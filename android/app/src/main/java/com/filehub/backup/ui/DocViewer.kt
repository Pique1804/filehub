package com.filehub.backup.ui

import android.graphics.Bitmap
import android.graphics.Color as AColor
import android.graphics.pdf.PdfRenderer
import android.os.ParcelFileDescriptor
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Download
import androidx.compose.material.icons.rounded.Close
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import com.filehub.backup.Api
import com.filehub.backup.Net
import com.filehub.backup.R
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.cancel
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import okhttp3.Request
import java.io.File
import java.io.IOException

/** Holds an open PdfRenderer; renders pages one at a time (PdfRenderer is single-page). */
private class PdfDoc(file: File) {
    private val pfd = ParcelFileDescriptor.open(file, ParcelFileDescriptor.MODE_READ_ONLY)
    private val renderer = PdfRenderer(pfd)
    val pageCount: Int = renderer.pageCount
    private val mutex = Mutex()

    suspend fun render(index: Int, widthPx: Int): Bitmap? = mutex.withLock {
        withContext(Dispatchers.IO) {
            try {
                val page = renderer.openPage(index)
                try {
                    val h = (widthPx.toFloat() * page.height / page.width).toInt().coerceIn(1, 10000)
                    val bmp = Bitmap.createBitmap(widthPx, h, Bitmap.Config.ARGB_8888)
                    bmp.eraseColor(AColor.WHITE)
                    page.render(bmp, null, null, PdfRenderer.Page.RENDER_MODE_FOR_DISPLAY)
                    bmp
                } finally { page.close() }
            } catch (e: Exception) { null }
        }
    }

    fun close() {
        try { renderer.close() } catch (_: Exception) {}
        try { pfd.close() } catch (_: Exception) {}
    }
}

private class PreviewHttpException(val status: Int) : IOException()

/** Full-screen document preview. Fetches /api/preview (PDF, or Office→PDF on the
 *  server) and renders the pages natively. Scroll vertically through pages. */
@Composable
fun DocViewer(source: String, path: String, name: String, onDownload: () -> Unit, onClose: () -> Unit) {
    val ctx = LocalContext.current
    var doc by remember { mutableStateOf<PdfDoc?>(null) }
    var error by remember { mutableStateOf<Exception?>(null) }
    var loading by remember { mutableStateOf(true) }
    val widthPx = with(LocalDensity.current) { LocalConfiguration.current.screenWidthDp.dp.roundToPx() }

    DisposableEffect(source, path) {
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
        scope.launch {
            try {
                val file = File(ctx.cacheDir, "preview_${(source + "|" + path).hashCode()}.pdf")
                Net.client.newCall(Request.Builder().url(Api.previewUrl(source, path)).build()).execute().use { resp ->
                    if (!resp.isSuccessful) throw PreviewHttpException(resp.code)
                    file.outputStream().use { out -> resp.body!!.byteStream().use { it.copyTo(out) } }
                }
                val d = PdfDoc(file)
                withContext(Dispatchers.Main) { doc = d; loading = false }
            } catch (e: Exception) {
                withContext(Dispatchers.Main) { error = e; loading = false }
            }
        }
        onDispose { scope.cancel(); doc?.close() }
    }

    Dialog(onDismissRequest = onClose, properties = DialogProperties(usePlatformDefaultWidth = false)) {
        Surface(Modifier.fillMaxSize(), color = Fh.Dark) {
            Column(Modifier.fillMaxSize()) {
                Row(
                    Modifier.fillMaxWidth().statusBarsPadding().padding(horizontal = 14.dp, vertical = 10.dp),
                    verticalAlignment = Alignment.CenterVertically
                ) {
                    DarkRoundButton(Icons.Rounded.Close, appString(R.string.action_close), onClose)
                    Spacer(Modifier.width(10.dp))
                    Text(name, color = Color.White, maxLines = 1, overflow = TextOverflow.Ellipsis,
                        fontSize = 15.sp, fontWeight = FontWeight.SemiBold, fontFamily = Golos,
                        modifier = Modifier.weight(1f))
                    Spacer(Modifier.width(10.dp))
                    DarkRoundButton(Icons.Outlined.Download, appString(R.string.photo_ui_download), onDownload)
                }
                Box(Modifier.weight(1f).fillMaxWidth(), contentAlignment = Alignment.Center) {
                    val d = doc
                    val failure = error
                    when {
                        loading -> Column(horizontalAlignment = Alignment.CenterHorizontally) {
                            CircularProgressIndicator(color = Color.White)
                            Spacer(Modifier.height(12.dp))
                            Text(appString(R.string.photo_ui_preparing_document), color = Color(0x8CFFFFFF), fontSize = 11.5.sp, fontFamily = Golos)
                        }
                        failure != null -> {
                            val detail = if (failure is PreviewHttpException) {
                                appString(R.string.photo_ui_document_http_status, failure.status)
                            } else {
                                failure.message?.takeIf { it.isNotBlank() }
                            }
                            Text(
                                if (detail == null) appString(R.string.photo_ui_document_failed)
                                else appString(R.string.photo_ui_document_failed_detail, detail),
                                color = Color(0x8CFFFFFF), fontFamily = Golos, modifier = Modifier.padding(24.dp)
                            )
                        }
                        d != null && d.pageCount > 0 -> LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                            items((0 until d.pageCount).toList()) { i -> PdfPageItem(d, i, widthPx) }
                        }
                        else -> Text(appString(R.string.photo_ui_empty_document), color = Color(0x8CFFFFFF), fontFamily = Golos)
                    }
                }
            }
        }
    }
}

/** 38dp translucent white-10% round icon button used on the dark viewer background. */
@Composable
private fun DarkRoundButton(icon: androidx.compose.ui.graphics.vector.ImageVector, label: String, onClick: () -> Unit) {
    Surface(onClick = onClick, shape = CircleShape, color = Color(0x1AFFFFFF), modifier = Modifier.size(48.dp)) {
        Box(contentAlignment = Alignment.Center) {
            Icon(icon, label, tint = Color.White, modifier = Modifier.size(18.dp))
        }
    }
}

@Composable
private fun PdfPageItem(doc: PdfDoc, index: Int, widthPx: Int) {
    var bmp by remember(index) { mutableStateOf<Bitmap?>(null) }
    var ratio by remember(index) { mutableStateOf(1.41f) } // A4 portrait placeholder
    LaunchedEffect(index) {
        val b = doc.render(index, widthPx)
        if (b != null) { ratio = b.height.toFloat() / b.width; bmp = b }
    }
    val b = bmp
    if (b != null) {
        Image(b.asImageBitmap(), contentDescription = null, modifier = Modifier.fillMaxWidth(), contentScale = ContentScale.FillWidth)
    } else {
        Box(Modifier.fillMaxWidth().aspectRatio(1f / ratio).background(Color.White), contentAlignment = Alignment.Center) {
            CircularProgressIndicator(strokeWidth = 2.dp, modifier = Modifier.size(26.dp))
        }
    }
}
