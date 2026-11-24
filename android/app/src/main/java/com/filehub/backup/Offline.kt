package com.filehub.backup

import android.content.Context
import java.io.File
import java.io.IOException
import java.security.MessageDigest

/** Private copies of server files, kept apart by account and original path. */
object Offline {
    private const val Separator = '\u001f'
    private val indexLock = Any()

    internal fun digest(value: String): String = MessageDigest.getInstance("SHA-256")
        .digest(value.toByteArray()).joinToString("") { "%02x".format(it.toInt() and 0xff) }

    private fun key(scope: String, source: String, path: String) = "$scope$Separator$source$Separator$path"

    internal fun fileFor(ctx: Context, scope: String, source: String, path: String): File =
        File(ctx.filesDir, "offline").resolve(digest(key(scope, source, path)))

    fun isPinned(ctx: Context, source: String, path: String) = localFile(ctx, source, path) != null

    fun localFile(ctx: Context, source: String, path: String): File? {
        val prefs = Prefs(ctx)
        val scope = prefs.accountScope()
        return fileFor(ctx, scope, source, path).takeIf { key(scope, source, path) in prefs.pinned && it.isFile }
    }

    internal fun publish(ctx: Context, scope: String, source: String, path: String, temporary: File) =
        synchronized(indexLock) {
            val prefs = Prefs(ctx)
            if (prefs.accountScope() != scope || !prefs.isConfigured()) throw IOException("Account changed")
            if (!temporary.renameTo(fileFor(ctx, scope, source, path))) throw IOException("Cannot save offline file")
            prefs.pinned = prefs.pinned + key(scope, source, path)
        }

    fun unpin(ctx: Context, source: String, path: String): Boolean = synchronized(indexLock) {
        val prefs = Prefs(ctx)
        val scope = prefs.accountScope()
        val file = fileFor(ctx, scope, source, path)
        if (file.exists() && !file.delete()) return false
        prefs.pinned = prefs.pinned - key(scope, source, path)
        true
    }

    fun list(ctx: Context): List<Pair<String, String>> {
        val prefs = Prefs(ctx)
        val scope = prefs.accountScope()
        val prefix = "$scope$Separator"
        return prefs.pinned.mapNotNull { key ->
            if (!key.startsWith(prefix)) return@mapNotNull null
            val body = key.removePrefix(prefix)
            val i = body.indexOf(Separator)
            if (i < 0) return@mapNotNull null
            val source = body.substring(0, i)
            val path = body.substring(i + 1)
            if (!fileFor(ctx, scope, source, path).isFile) return@mapNotNull null
            source to path
        }
    }
}
