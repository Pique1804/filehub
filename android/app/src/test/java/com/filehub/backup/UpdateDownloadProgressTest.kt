package com.filehub.backup

import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertThrows
import org.junit.Test
import java.io.ByteArrayOutputStream
import java.io.IOException
import java.security.MessageDigest

class UpdateDownloadProgressTest {
    @Test
    fun progressReportsBytesWrittenWhilePreservingTheApkAndItsDigest() {
        val apk = ByteArray(256 * 1024 + 11) { it.toByte() }
        val output = ByteArrayOutputStream()
        val digest = MessageDigest.getInstance("SHA-256")
        val updates = mutableListOf<Updater.Progress.Downloading>()

        val downloaded = Updater.copyApk(apk.inputStream(), output, digest) { bytes ->
            assertEquals(output.size().toLong(), bytes)
            updates += Updater.Progress.Downloading(bytes, apk.size.toLong())
        }

        assertEquals(listOf(0L, 131072L, 262144L, apk.size.toLong()), updates.map { it.downloaded })
        assertEquals(0.0, updates.first().fraction!!, 0.0)
        assertEquals(1.0, updates.last().fraction!!, 0.0)
        assertEquals(apk.size.toLong(), downloaded)
        assertArrayEquals(apk, output.toByteArray())
        assertArrayEquals(MessageDigest.getInstance("SHA-256").digest(apk), digest.digest())
    }

    @Test
    fun aResponseWithNoSizeHasAByteCountWithoutAnEstimatedFraction() {
        val download = Updater.Progress.Downloading(downloaded = 134000L, total = null)
        assertEquals(134000L, download.downloaded)
        assertNull(download.fraction)
    }

    @Test
    fun copyStopsBeforeWritingBeyondTheLimit() {
        val output = ByteArrayOutputStream()
        val input = ByteArray(200 * 1024).inputStream()
        assertThrows(IOException::class.java) {
            Updater.copyApk(input, output, MessageDigest.getInstance("SHA-256"), limit = 150L * 1024) {}
        }
        assertEquals(128 * 1024, output.size())
    }
}
