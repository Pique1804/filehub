package com.filehub.backup

import android.content.Context
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull

/** Thin SharedPreferences wrapper for connection settings + backup bookkeeping. */
class Prefs(private val ctx: Context) {
    private val sp = ctx.getSharedPreferences("filehub", Context.MODE_PRIVATE)
    private val passwordVault=PasswordVault(sp)
    private val savedServer: String get() = sp.getString("server", "") ?: ""

    var server: String
        get() = savedServerAddress(savedServer)
        set(v) {
            val endpoint = Net.normalizedServer(v) ?: return
            if (endpoint != Net.normalizedServer(server)) sp.edit().putString("server", endpoint).apply()
        }
    var user: String
        get() = sp.getString("user", "") ?: ""
        set(v) { sp.edit().putString("user", v.trim()).apply() }
    var pass: String
        get() = try { passwordVault.read() } catch (_: Exception) {
            status=ctx.appResources().getString(R.string.transfer_ui_password_unavailable)
            ""
        }
        set(v) { passwordVault.write(v) }
    var source: String
        get() = sp.getString("source", "HDD2") ?: "HDD2"
        set(v) { sp.edit().putString("source", v.trim()).apply() }
    var folder: String
        get() = sp.getString("folder", "Photos/PhoneBackup") ?: "Photos/PhoneBackup"
        set(v) { sp.edit().putString("folder", v.trim().trim('/')).apply() }
    var auto: Boolean
        get() = sp.getBoolean("auto", false)
        set(v) { sp.edit().putBoolean("auto", v).apply() }
    var status: String
        get() = sp.getString("status", "") ?: ""
        set(v) { sp.edit().putString("status", v).apply() }
    var lastRun: Long
        get() = sp.getLong("lastRun", 0)
        set(v) { sp.edit().putLong("lastRun", v).apply() }
    var intervalMin: Long // sync frequency in minutes (WorkManager floor 15)
        get() = sp.getLong("intervalMin", 60)
        set(v) { sp.edit().putLong("intervalMin", v).apply() }
    var wifiOnly: Boolean
        get() = sp.getBoolean("wifiOnly", false)
        set(v) { sp.edit().putBoolean("wifiOnly", v).apply() }
    var gridCols: Int
        get() = sp.getInt("gridCols", 4)
        set(v) { sp.edit().putInt("gridCols", v.coerceIn(2, 6)).apply() }
    var appLock: Boolean
        get() = sp.getBoolean("appLock", false)
        set(v) { sp.edit().putBoolean("appLock", v).apply() }
    var pinned: Set<String> // "source|path" of files kept available offline
        get() = sp.getStringSet("pinned", emptySet()) ?: emptySet()
        set(v) { sp.edit().putStringSet("pinned", HashSet(v)).apply() }

    var selectedAlbums: Set<String> // empty means all accessible phone albums
        get() = sp.getStringSet("selectedAlbums", emptySet()) ?: emptySet()
        set(v) { sp.edit().putStringSet("selectedAlbums", HashSet(v)).apply() }

    fun accountScope(): String = accountScope(savedServer, user)
    fun setBackupDestination(source: String, path: String) {
        sp.edit().putString("source", source).putString("folder", path.trim('/')).apply()
    }

    fun backupScope(): String = listOf(accountScope(), source, folder).joinToString("\u001f")

    // live progress (read by the UI while a backup runs)
    var syncRunning: Boolean
        get() = sp.getBoolean("syncRunning", false)
        set(v) { sp.edit().putBoolean("syncRunning", v).apply() }
    var syncTotal: Int
        get() = sp.getInt("syncTotal", 0)
        set(v) { sp.edit().putInt("syncTotal", v).apply() }
    var syncDone: Int
        get() = sp.getInt("syncDone", 0)
        set(v) { sp.edit().putInt("syncDone", v).apply() }
    var syncStartMs: Long
        get() = sp.getLong("syncStartMs", 0)
        set(v) { sp.edit().putLong("syncStartMs", v).apply() }

    /** keys ("i:<id>" / "v:<id>") of media already uploaded, so we never re-send. */
    var uploaded: Set<String>
        get() = sp.getStringSet("uploaded", emptySet()) ?: emptySet()
        set(v) { sp.edit().putStringSet("uploaded", HashSet(v)).apply() }

    fun isConfigured() = server.isNotBlank() && user.isNotBlank()

    /** Clears credentials + auto-backup flag on logout (keeps server URL for convenience). */
    fun logout() {
        passwordVault.clear()
        sp.edit().remove("user").remove("pass").putBoolean("auto", false).apply()
    }

    companion object {
        internal fun savedServerAddress(value: String): String {
            val address = value.trim()
            // Previously saved addresses without a scheme always used HTTP.
            // Preserve that choice until the user signs in with a new address.
            return if (address.isNotEmpty() && "://" !in address) "http://$address" else address
        }

        internal fun accountScope(saved: String, user: String): String {
            val endpoint = Net.normalizedServer(savedServerAddress(saved)).orEmpty()
            val root = endpoint.toHttpUrlOrNull()?.encodedPath == "/"
            // Root installs can keep existing backup and offline keys. Old
            // path-based keys lost original case and cannot be safely claimed.
            val key = if (root) saved.trim().trimEnd('/').lowercase() else "\u001e$endpoint"
            return "$key\u001f$user"
        }
    }
}
