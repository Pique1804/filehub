package com.filehub.backup

private val IMG_EXT = setOf("jpg", "jpeg", "png", "gif", "webp", "bmp", "heic")
private val VID_EXT = setOf("mp4", "webm", "mkv", "mov", "avi", "m4v", "ogv", "wmv", "flv", "mpg", "mpeg")
fun extOf(n: String) = n.substringAfterLast('.', "").lowercase()
fun isVideoName(n: String) = extOf(n) in VID_EXT
fun isMediaName(n: String) = extOf(n).let { it in IMG_EXT || it in VID_EXT }

data class Source(val name: String, val total: Long, val free: Long)
data class Entry(val name: String, val isDir: Boolean, val size: Long, val modTime: Long, val owner: String = "")
data class Photo(val name: String, val path: String, val source: String, val modTime: Long, val isVideo: Boolean, val revision: String = "", val size: Long = 0)
data class PhotoPage(val photos: List<Photo>, val total: Int, val images: Int, val videos: Int)
data class TrashItem(val id: String, val name: String, val origPath: String, val deletedAt: Long, val isDir: Boolean, val size: Long)
data class RecentFile(val name: String, val path: String, val source: String, val modTime: Long, val size: Long, val owner: String = "")
data class Place(val source: String, val path: String, val lat: Double, val lon: Double, val modTime: Long,
    val name: String = path.substringAfterLast('/'), val isVideo: Boolean = isVideoName(path), val revision: String = "", val size: Long = 0) {
    fun photo() = Photo(name, path, source, modTime, isVideo, revision, size)
}
data class ContentHit(val source: String, val path: String, val name: String, val modTime: Long, val excerpt: String, val owner: String = "")
data class Album(val id: Long, val name: String, val count: Int, val coverSource: String, val coverPath: String, val shareActive: Boolean = false)
/** A unified Files-search row: a filename match and/or a text/OCR match (excerpt non-null). */
data class FileHit(val name: String, val path: String, val source: String, val isDir: Boolean, val size: Long, val modTime: Long, val excerpt: String? = null, val owner: String = "")

fun fmtSize(n: Long): String {
    if (n < 1024) return "$n B"
    var v = n.toDouble(); val u = listOf("KB", "MB", "GB", "TB"); var i = -1
    do { v /= 1024; i++ } while (v >= 1024 && i < u.size - 1)
    return String.format("%.1f %s", v, u[i])
}
