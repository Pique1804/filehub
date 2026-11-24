package com.filehub.backup

import org.json.JSONObject
import androidx.annotation.StringRes

enum class BackupCopyState {
    Confirmed, SameVolume, Unconfirmed, Unavailable, Unknown;

    companion object {
        fun from(value: String) = when (value) {
            "confirmed" -> Confirmed
            "same_volume" -> SameVolume
            "unconfirmed" -> Unconfirmed
            "unavailable" -> Unavailable
            else -> Unknown
        }
    }
}

data class BackupHealth(
    val state: String = "unknown",
    val stale: Boolean = false,
    val lastSuccessfulCheck: Long = 0,
    val fileChecks: Boolean = false,
    val previousSettings: Boolean = false,
    val processingPending: Int = 0,
) {
    @get:StringRes
    val statusMessage: Int get() = when {
        state == "error" -> R.string.backup_health_error
        state in setOf("running", "copying", "checking") -> R.string.backup_health_running
        previousSettings -> R.string.backup_health_previous_settings
        stale && lastSuccessfulCheck > 0 -> R.string.backup_health_stale
        state == "ok" && lastSuccessfulCheck > 0 -> R.string.backup_health_ok
        else -> R.string.backup_health_unknown
    }

    companion object {
        fun from(response: JSONObject): BackupHealth {
            val managed = response.optJSONObject("managedCopy")
            val copy = managed ?: response
            return BackupHealth(
                state = copy.optString("state"),
                stale = copy.optBoolean("stale"),
                lastSuccessfulCheck = if (managed != null) copy.optLong("completed")
                    else copy.optLong("lastSuccessfulCompleted").takeIf { it > 0 } ?: copy.optLong("completed"),
                fileChecks = managed?.optBoolean("fileChecks") == true,
                previousSettings = managed?.optBoolean("previousSettings") == true,
                processingPending = response.optInt("processingPending"),
            )
        }
    }
}
