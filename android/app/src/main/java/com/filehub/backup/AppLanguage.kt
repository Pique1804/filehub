package com.filehub.backup

import android.content.Context
import android.content.res.Configuration
import android.content.res.Resources
import android.os.LocaleList
import java.util.Locale

internal enum class AppLanguage(val tag: String) {
    SYSTEM("system"), RUSSIAN("ru"), ENGLISH("en");

    companion object {
        fun from(tag: String?) = entries.firstOrNull { it.tag == tag } ?: SYSTEM
    }

    fun locale(system: LocaleList): Locale {
        if (this != SYSTEM) return Locale.forLanguageTag(tag)
        // Preserve the region when following a supported system language.
        for (index in 0 until system.size()) {
            val locale = system[index]
            if (locale.language == RUSSIAN.tag || locale.language == ENGLISH.tag) return locale
        }
        return Locale.forLanguageTag(RUSSIAN.tag)
    }

    fun resources(context: Context, system: LocaleList): Resources {
        val override = Configuration().apply { setLocale(locale(system)) }
        return context.createConfigurationContext(override).resources
    }
}

internal fun languagePreferences(context: Context) =
    context.getSharedPreferences("filehub_language", Context.MODE_PRIVATE)

internal const val LANGUAGE_KEY = "language"

/** Background work reads the latest choice without restarting or rescheduling work. */
internal fun Context.appResources(): Resources {
    val language = AppLanguage.from(languagePreferences(this).getString(LANGUAGE_KEY, null))
    return language.resources(this, resources.configuration.locales)
}
