package com.filehub.backup

import android.app.Application
import android.net.Uri
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(manifest = Config.NONE, application = Application::class, sdk = [28])
class CleanupPolicyTest {
    private val now = 1_700_000_000L
    private val week = 7 * 24 * 60 * 60L

    private fun item(id: Long, favorite: Boolean? = false, dateAdded: Long = now - 2 * week,
        modified: Long = now - 2 * week, album: String = "Camera"): MediaScanner.Item = MediaScanner.Item(
        id, "fixture-$id.jpg", Uri.parse("content://media/external/images/media/$id"), false,
        dateAdded, id * 1024, modified, album, favorite,
    )

    private fun candidate(item: MediaScanner.Item): FreeSpace.Candidate = FreeSpace.Candidate(
        item, BackupLedger.Receipt(item.backupKey, item.size, item.modified, item.name, "a".repeat(64), now),
    )

    @Test fun defaultProtectionKeepsRecentFavoritesAndUnknownFavoriteStatus() {
        val protection = FreeSpace.Protection()
        assertEquals(FreeSpace.Kept.Favorite, protection.keep(item(1, favorite = true), now))
        assertEquals(FreeSpace.Kept.FavoriteUnknown, protection.keep(item(2, favorite = null), now))
        assertEquals(FreeSpace.Kept.Recent, protection.keep(item(3, dateAdded = now - week), now))
        assertEquals(FreeSpace.Kept.Recent, protection.keep(item(4, modified = now - 1), now))
        assertNull(protection.keep(item(5, dateAdded = now - week - 1), now))
        assertNull(protection.copy(favorites = false, recent = false).keep(item(6, favorite = true, modified = now), now))
    }

    @Test fun exactSelectionOmitsKeptAlbumsAndRejectsStaleSelectedKeys() {
        val allowed = candidate(item(1))
        val album = candidate(item(2, album = "Keep"))
        val favorite = candidate(item(3, favorite = true))
        val unselected = candidate(item(4))
        val all = listOf(allowed, album, favorite, unselected)
        val protection = FreeSpace.Protection(albums = setOf("Keep"))
        val review = FreeSpace.Review("fixture-scope", 4, all, true)
        assertEquals(FreeSpace.Kept.Album, protection.keep(album.item, now))
        assertEquals(listOf(allowed, unselected), review.selectable(protection, now))
        assertEquals(listOf(allowed), review.exactSelection(setOf(allowed.receipt.key), protection, now))
        assertTrue(review.exactSelection(setOf(allowed.receipt.key, favorite.receipt.key), protection, now).isEmpty())
        assertTrue(review.exactSelection(setOf(allowed.receipt.key, "unknown"), protection, now).isEmpty())
        assertTrue(review.copy(available = false).exactSelection(setOf(allowed.receipt.key), protection, now).isEmpty())
    }
}
