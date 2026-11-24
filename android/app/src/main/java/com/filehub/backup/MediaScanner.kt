package com.filehub.backup

import android.content.ContentUris
import android.content.Context
import android.net.Uri
import android.os.Build
import android.provider.MediaStore

/** Enumerates the device's photos + videos from MediaStore (oldest first). */
object MediaScanner {
    data class Item(
        val id: Long, val name: String, val uri: Uri, val isVideo: Boolean,
        val dateAdded: Long, val size: Long, val modified: Long, val album: String,
        val favorite: Boolean? = null,
    )

    fun scan(ctx: Context, selectedAlbums: Set<String> = emptySet(), chronological:Boolean=true): List<Item> {
        val out = ArrayList<Item>()
        scanUri(ctx, MediaStore.Images.Media.EXTERNAL_CONTENT_URI, false, out)
        scanUri(ctx, MediaStore.Video.Media.EXTERNAL_CONTENT_URI, true, out)
        if(chronological)out.sortBy { it.dateAdded } // chronological backup
        return if (selectedAlbums.isEmpty()) out else out.filter { it.album in selectedAlbums }
    }

    fun unchanged(ctx: Context, item: Item): Boolean = try {
        if(item.uri.scheme=="file") {
            java.io.File(item.uri.path ?: "").let {it.length()==item.size&&it.lastModified()/1000==item.modified}
        } else {
            ctx.contentResolver.query(item.uri,arrayOf(MediaStore.MediaColumns.SIZE,MediaStore.MediaColumns.DATE_MODIFIED),null,null,null)?.use { c ->
                c.moveToFirst() && c.getLong(0) == item.size && c.getLong(1) == item.modified
            } ?: false
        }
    } catch (e: Exception) { false }

    /** Read the selected media row again before showing or submitting a deletion. */
    fun cleanupItem(ctx: Context, item: Item): Item? {
        val album = if (Build.VERSION.SDK_INT >= 29) MediaStore.MediaColumns.RELATIVE_PATH else "bucket_display_name"
        val columns = arrayOf(MediaStore.MediaColumns.DATE_ADDED, MediaStore.MediaColumns.SIZE,
            MediaStore.MediaColumns.DATE_MODIFIED, album)
        fun read(includeFavorite: Boolean): Item? {
            val projection = if (includeFavorite) columns + MediaStore.MediaColumns.IS_FAVORITE else columns
            return ctx.contentResolver.query(item.uri, projection, null, null, null)?.use { row ->
                if (!row.moveToFirst()) return@use null
                val favorite = if (includeFavorite) row.getColumnIndex(MediaStore.MediaColumns.IS_FAVORITE) else -1
                item.copy(
                    dateAdded = row.getLong(row.getColumnIndexOrThrow(MediaStore.MediaColumns.DATE_ADDED)),
                    size = row.getLong(row.getColumnIndexOrThrow(MediaStore.MediaColumns.SIZE)),
                    modified = row.getLong(row.getColumnIndexOrThrow(MediaStore.MediaColumns.DATE_MODIFIED)),
                    album = albumName(row.getString(row.getColumnIndexOrThrow(album))),
                    favorite = if (favorite >= 0 && !row.isNull(favorite)) row.getInt(favorite) != 0 else null,
                )
            }
        }
        return try {
            if (Build.VERSION.SDK_INT < 30) read(false)
            else try { read(true) } catch (_: IllegalArgumentException) { read(false) }
        } catch (_: Exception) { null }
    }

    private fun albumName(path: String?) = path?.trim('/')?.ifBlank { "Другое" } ?: "Другое"

    private fun scanUri(ctx: Context, base: Uri, isVideo: Boolean, out: ArrayList<Item>) {
        val proj = mutableListOf(
            MediaStore.MediaColumns._ID,
            MediaStore.MediaColumns.DISPLAY_NAME,
            MediaStore.MediaColumns.DATE_ADDED,
            MediaStore.MediaColumns.DATE_MODIFIED,
            MediaStore.MediaColumns.SIZE,
        )
        if (Build.VERSION.SDK_INT >= 29) proj.add(MediaStore.MediaColumns.RELATIVE_PATH)
        else proj.add("bucket_display_name")
        ctx.contentResolver.query(base, proj.toTypedArray(), if (Build.VERSION.SDK_INT >= 29) "is_pending=0" else null, null, "${MediaStore.MediaColumns.DATE_ADDED} ASC")?.use { c ->
            val idCol = c.getColumnIndexOrThrow(MediaStore.MediaColumns._ID)
            val nameCol = c.getColumnIndexOrThrow(MediaStore.MediaColumns.DISPLAY_NAME)
            val dateCol = c.getColumnIndexOrThrow(MediaStore.MediaColumns.DATE_ADDED)
            val modifiedCol = c.getColumnIndexOrThrow(MediaStore.MediaColumns.DATE_MODIFIED)
            val sizeCol = c.getColumnIndexOrThrow(MediaStore.MediaColumns.SIZE)
            val albumCol = c.getColumnIndexOrThrow(if (Build.VERSION.SDK_INT >= 29) MediaStore.MediaColumns.RELATIVE_PATH else "bucket_display_name")
            while (c.moveToNext()) {
                val id = c.getLong(idCol)
                val name = c.getString(nameCol) ?: "$id"
                val date = c.getLong(dateCol)
                val album = albumName(c.getString(albumCol))
                if (c.getLong(sizeCol) <= 0) continue
                out.add(Item(id, name, ContentUris.withAppendedId(base, id), isVideo,
                    date, c.getLong(sizeCol), c.getLong(modifiedCol), album))
            }
        }
    }
}
