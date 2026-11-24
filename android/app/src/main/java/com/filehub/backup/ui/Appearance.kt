package com.filehub.backup.ui

import android.app.Activity
import android.content.Context
import android.content.ContextWrapper
import android.content.SharedPreferences
import androidx.annotation.StringRes
import androidx.compose.runtime.*
import androidx.compose.ui.platform.LocalContext
import com.filehub.backup.R

enum class AppearanceMode(val value: String, @StringRes val label: Int) {
    SYSTEM("system", R.string.theme_system), LIGHT("light", R.string.theme_light), DARK("dark", R.string.theme_dark);
    companion object {
        fun from(value: String?) = entries.firstOrNull { it.value == value } ?: SYSTEM
    }
}

class AppearanceState(private val prefs: SharedPreferences) {
    var mode by mutableStateOf(AppearanceMode.from(prefs.getString("theme", null)))
        private set
    fun select(mode: AppearanceMode) {
        prefs.edit().putString("theme", mode.value).apply()
        this.mode = mode
    }
    fun refresh() { mode = AppearanceMode.from(prefs.getString("theme", null)) }
}

val LocalAppearance = staticCompositionLocalOf<AppearanceState> { error("AppTheme is required") }

@Composable
internal fun rememberAppearance(): AppearanceState {
    val ctx = LocalContext.current.applicationContext
    val prefs = remember(ctx) { ctx.getSharedPreferences("filehub_appearance", Context.MODE_PRIVATE) }
    val state = remember(prefs) { AppearanceState(prefs) }
    DisposableEffect(prefs) {
        val listener = SharedPreferences.OnSharedPreferenceChangeListener { _, key -> if (key == "theme") state.refresh() }
        prefs.registerOnSharedPreferenceChangeListener(listener)
        onDispose { prefs.unregisterOnSharedPreferenceChangeListener(listener) }
    }
    return state
}

internal tailrec fun Context.findActivity(): Activity? = when (this) {
    is Activity -> this
    is ContextWrapper -> baseContext.findActivity()
    else -> null
}
