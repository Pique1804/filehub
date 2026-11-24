package com.filehub.backup

import com.filehub.backup.ui.groupPlaceMarkers
import com.filehub.backup.ui.markerAt
import com.filehub.backup.ui.mapLatitude
import com.filehub.backup.ui.preparePlacesMap
import java.time.LocalDate
import java.time.ZoneId
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class PlacesMarkersTest {
    @Test
    fun groupsNearbyPhotosAndKeepsDistantPhotoSelectable() {
        val first = place("first")
        val second = place("second")
        val separate = place("separate")
        val outside = place("outside")
        val markers = groupPlaceMarkers(
            listOf(first, second, separate, outside),
            doubleArrayOf(49.0, 80.0, 51.0, 84.0, 250.0, 260.0, -60.0, 100.0),
            width = 320, height = 400, radius = 50f,
        )

        assertEquals(2, markers.size)
        assertEquals(listOf(first, second), markerAt(markers, 50f, 82f, 25f)?.photos)
        assertEquals(listOf(separate), markerAt(markers, 250f, 260f, 25f)?.photos)
        assertNull(markerAt(markers, 150f, 160f, 25f))
    }

    @Test
    fun dateFilterIncludesWholeLocalDayAndUpdatesGroups() {
        val zone = ZoneId.of("America/New_York")
        val day = LocalDate.of(2025, 3, 9)
        val start = day.atStartOfDay(zone).toEpochSecond()
        val end = day.plusDays(1).atStartOfDay(zone).toEpochSecond()
        val morning = place("morning", start)
        val evening = place("evening", end - 1)
        val previous = place("previous", start - 1)
        val tomorrow = place("tomorrow", end)

        val matching = preparePlacesMap(listOf(morning, evening, previous, tomorrow), day, day, zone)
        assertEquals(listOf(evening, morning), matching.places)
        val markers = groupPlaceMarkers(
            matching.places, doubleArrayOf(50.0, 100.0, 50.0, 100.0),
            width = 320, height = 400, radius = 52f,
        )
        assertEquals(listOf(evening, morning), markers.single().photos)
        assertTrue(preparePlacesMap(matching.places, day.plusDays(1), null, zone).places.isEmpty())
        assertTrue(groupPlaceMarkers(emptyList(), doubleArrayOf(), 320, 400, 52f).isEmpty())
    }

    @Test
    fun projectionUsesLatitudeThenLongitudeAndBoundsPolarPhotos() {
        val polar = place("polar").copy(lat = 90.0, lon = -23.0)
        val invalid = place("invalid").copy(lat = Double.NaN)
        val matching = preparePlacesMap(listOf(polar, polar, invalid), null, null, ZoneId.of("UTC"))
        assertEquals(listOf(polar), matching.places)
        assertEquals(listOf(mapLatitude(90.0), -23.0), matching.coordinates.toList())
        assertEquals(-85.051129, mapLatitude(-90.0), 0.0)
    }

    private fun place(name: String, time: Long = 0L) =
        Place("Fixture", "$name.jpg", 60.0, 30.0, time, name, false)
}
