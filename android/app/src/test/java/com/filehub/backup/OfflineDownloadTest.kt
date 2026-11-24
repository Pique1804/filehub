package com.filehub.backup

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.runBlocking
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.SocketPolicy
import okio.Buffer
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertThrows
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.ByteArrayOutputStream
import java.io.IOException
import java.io.OutputStream
import javax.net.ssl.SSLHandshakeException

class OfflineDownloadTest {
    @Test
    fun reportsProgressWhileCopying() = runBlocking {
        val data = ByteArray(400_000) { (it % 256).toByte() }
        val copied = ByteArrayOutputStream()
        val progress = mutableListOf<Long>()
        copyOfflineDownload(data.inputStream(), copied, data.size.toLong()) { received ->
            progress.add(received)
            assertEquals(received, copied.size().toLong())
        }
        assertEquals(listOf(131_072L, 262_144L, 393_216L, 400_000L), progress)
        assertArrayEquals(data, copied.toByteArray())
    }

    @Test
    fun rejectsShortResponse() {
        assertThrows(IOException::class.java) {
            runBlocking { copyOfflineDownload(ByteArray(10).inputStream(), ByteArrayOutputStream(), 100) {} }
        }
    }

    @Test
    fun handlesUnknownSize() = runBlocking {
        val data = ByteArray(150_000) { 11 }
        val copied = ByteArrayOutputStream()
        copyOfflineDownload(data.inputStream(), copied, -1) {}
        assertArrayEquals(data, copied.toByteArray())
    }

    @Test
    fun cancellationStopsReading() {
        val copied = ByteArrayOutputStream()
        assertThrows(CancellationException::class.java) {
            runBlocking {
                copyOfflineDownload(ByteArray(500_000).inputStream(), copied, -1) { throw CancellationException() }
            }
        }
        assertEquals(131_072, copied.size())
    }

    @Test
    fun retriesInterruptedResponseAndTheNextTransferMatchesTheOriginal() {
        MockWebServer().use { server ->
            val data = ByteArray(400_000) { (it % 256).toByte() }
            server.enqueue(MockResponse().setBody(Buffer().write(data))
                .setSocketPolicy(SocketPolicy.DISCONNECT_DURING_RESPONSE_BODY))
            server.enqueue(MockResponse().setBody(Buffer().write(data)))
            val client = OkHttpClient()
            val request = Request.Builder().url(server.url("/download")).build()
            fun transfer(output: OutputStream) = runBlocking {
                client.newCall(request).execute().use { response ->
                    val body = checkNotNull(response.body)
                    copyOfflineDownload(body.byteStream(), output, body.contentLength()) {}
                }
            }
            val partial = ByteArrayOutputStream()
            val failure = assertThrows(OfflineReadFailure::class.java) { transfer(partial) }
            assertTrue(partial.size() in 1 until data.size)
            assertTrue(retryableOfflineTransport(failure.networkError))
            assertTrue(hasOfflineRetryRemaining(0))
            val retry = ByteArrayOutputStream()
            transfer(retry)
            assertArrayEquals(data, retry.toByteArray())
        }
    }

    @Test
    fun accessRejectionLocalWriteFailureAndCertificateFailureArePermanent() {
        for (status in listOf(400, 401, 403, 404, 410)) assertFalse(retryableOfflineStatus(status))
        for (status in listOf(408, 429, 500, 502, 503, 504)) assertTrue(retryableOfflineStatus(status))
        assertFalse(retryableOfflineTransport(SSLHandshakeException("untrusted certificate")))
        assertTrue(hasOfflineRetryRemaining(2))
        assertFalse(hasOfflineRetryRemaining(3))
        val fullDisk = object : OutputStream() {
            override fun write(b: Int) { throw IOException("No space left") }
        }
        val failure = assertThrows(IOException::class.java) {
            runBlocking { copyOfflineDownload(byteArrayOf(1).inputStream(), fullDisk, 1) {} }
        }
        assertFalse(failure is OfflineReadFailure)
    }
}
