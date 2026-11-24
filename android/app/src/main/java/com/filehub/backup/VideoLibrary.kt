package com.filehub.backup

data class VideoFolder(val source: String, val path: String, val kind: String)
data class LibraryVideo(
    val source: String, val path: String, val name: String,
    val season: Int, val episode: Int, val position: Double, val watched: Long,
)
data class VideoTitle(
    val id: String, val kind: String, val name: String, val cover: String,
    val videos: List<LibraryVideo>,
    val description: String,
    val year: Int,
    val genres: List<String>,
    val poster: String,
    val metadataLink: String,
    val metadataSource: String = "",
    val tmdbMatchId: Int = 0,
    val tmdbQuery: String = "",
    val tmdbQueryYear: Int = 0,
)
data class VideoLibrary(
    val folders: List<VideoFolder>, val titles: List<VideoTitle>,
    val partial: Boolean, val canManage: Boolean,
    val tmdbEnabled: Boolean = false, val tmdbPending: Boolean = false,
)

data class TmdbMatch(
    val id: Int,
    val name: String,
    val original: String,
    val year: Int,
    val poster: String,
)
