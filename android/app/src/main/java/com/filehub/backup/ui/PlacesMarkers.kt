package com.filehub.backup.ui

import com.filehub.backup.Place
import java.time.LocalDate
import java.time.ZoneId
import kotlin.math.floor

internal fun mapLatitude(latitude: Double): Double = latitude.coerceIn(-85.051129, 85.051129)

internal data class PlacesOnMap(val places: List<Place>, val coordinates: DoubleArray)

internal fun preparePlacesMap(places: List<Place>, from: LocalDate?, to: LocalDate?, zone: ZoneId): PlacesOnMap {
    val start = from?.atStartOfDay(zone)?.toEpochSecond() ?: Long.MIN_VALUE
    val end = to?.plusDays(1)?.atStartOfDay(zone)?.toEpochSecond() ?: Long.MAX_VALUE
    val shown = places.asSequence()
        .filter { it.modTime >= start && it.modTime < end }
        .filter { it.lat in -90.0..90.0 && it.lon in -180.0..180.0 }
        .distinctBy { it.source to it.path }
        .sortedByDescending { it.modTime }
        .toList()
    val coordinates = DoubleArray(shown.size * 2)
    shown.forEachIndexed { index, place ->
        coordinates[index * 2] = mapLatitude(place.lat)
        coordinates[index * 2 + 1] = place.lon
    }
    return PlacesOnMap(shown, coordinates)
}

internal data class PlacesMarker(val x: Float, val y: Float, val photos: List<Place>)

private class MarkerGroup(val anchorX: Double, val anchorY: Double) {
    private val photos = ArrayList<Place>()
    private var totalX = 0.0
    private var totalY = 0.0

    fun add(place: Place, x: Double, y: Double) {
        photos += place
        totalX += x
        totalY += y
    }

    fun marker() = PlacesMarker((totalX / photos.size).toFloat(), (totalY / photos.size).toFloat(), photos)
}

private fun distanceSquared(x: Double, y: Double): Double = x * x + y * y

private fun cellKey(x: Int, y: Int): Long = (x.toLong() shl 32) or (y.toLong() and 0xffffffffL)

/** Group projected points in screen pixels. Searching adjacent cells keeps
 * nearby photos together even when they straddle a grid boundary. */
internal fun groupPlaceMarkers(
    places: List<Place>,
    projected: DoubleArray,
    width: Int,
    height: Int,
    radius: Float,
): List<PlacesMarker> {
    if (places.isEmpty() || width <= 0 || height <= 0 || radius <= 0) return emptyList()
    val cells = HashMap<Long, MutableList<MarkerGroup>>()
    val groups = ArrayList<MarkerGroup>()
    val radiusSquared = radius.toDouble() * radius
    for ((index, place) in places.withIndex()) {
        val x = projected[index * 2]
        val y = projected[index * 2 + 1]
        if (!x.isFinite() || !y.isFinite() || x < 0 || y < 0 || x > width || y > height) continue
        val col = floor(x / radius).toInt()
        val row = floor(y / radius).toInt()
        var nearest: MarkerGroup? = null
        var bestDistance = radiusSquared
        for (dy in -1..1) {
            for (dx in -1..1) {
                val nearby = cells[cellKey(col + dx, row + dy)] ?: continue
                for (group in nearby) {
                    val distance = distanceSquared(x - group.anchorX, y - group.anchorY)
                    if (distance < bestDistance) {
                        nearest = group
                        bestDistance = distance
                    }
                }
            }
        }
        val group = nearest ?: MarkerGroup(x, y).also {
            groups += it
            cells.getOrPut(cellKey(col, row), ::ArrayList) += it
        }
        group.add(place, x, y)
    }
    return groups.map(MarkerGroup::marker)
}

internal fun markerAt(markers: List<PlacesMarker>, x: Float, y: Float, hitRadius: Float): PlacesMarker? {
    var best = hitRadius.toDouble() * hitRadius
    var hit: PlacesMarker? = null
    for (marker in markers) {
        val distance = distanceSquared((marker.x - x).toDouble(), (marker.y - y).toDouble())
        if (distance <= best) {
            best = distance
            hit = marker
        }
    }
    return hit
}
