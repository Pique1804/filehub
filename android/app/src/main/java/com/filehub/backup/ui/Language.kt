package com.filehub.backup.ui

import android.content.SharedPreferences
import android.content.res.Resources
import androidx.annotation.PluralsRes
import androidx.annotation.StringRes
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.compositionLocalOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import com.filehub.backup.AppLanguage
import com.filehub.backup.LANGUAGE_KEY
import com.filehub.backup.R
import com.filehub.backup.languagePreferences

private class LanguageSelection(private val storage: SharedPreferences) {
    var language by mutableStateOf(AppLanguage.from(storage.getString(LANGUAGE_KEY, null)))
        private set

    fun select(choice: AppLanguage) {
        if (choice == language) return
        storage.edit().putString(LANGUAGE_KEY, choice.tag).apply()
        language = choice
    }
}

private val LocalLanguage = compositionLocalOf<LanguageSelection> { error("AppTheme is required") }
internal val LocalAppResources = compositionLocalOf<Resources> { error("AppTheme is required") }

@Composable
internal fun ProvideAppLanguage(content: @Composable () -> Unit) {
    val context = LocalContext.current
    val system = LocalConfiguration.current.locales
    val selection = remember(context) { LanguageSelection(languagePreferences(context)) }
    val language = selection.language
    val resources = remember(context, system, language) { language.resources(context, system) }
    // Keep the real Activity context for biometrics, permission launchers and Android intents.
    CompositionLocalProvider(LocalLanguage provides selection, LocalAppResources provides resources, content = content)
}

@Composable
internal fun appString(@StringRes id: Int, vararg args: Any): String =
    if (args.isEmpty()) LocalAppResources.current.getString(id) else LocalAppResources.current.getString(id, *args)

@Composable
internal fun appQuantity(@PluralsRes id: Int, count: Int): String =
    LocalAppResources.current.getQuantityString(id, count, count)

@Composable
internal fun LanguagePicker(modifier: Modifier = Modifier) {
    val selection = LocalLanguage.current
    val options = listOf(
        AppLanguage.SYSTEM.tag to appString(R.string.language_system),
        AppLanguage.RUSSIAN.tag to appString(R.string.language_russian),
        AppLanguage.ENGLISH.tag to appString(R.string.language_english),
    )
    Column(modifier, verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Text(appString(R.string.language_title), style = MaterialTheme.typography.titleMedium, color = Fh.Text)
        FhSegmented(options, selection.language.tag, { selection.select(AppLanguage.from(it)) }, fitLabels = true)
    }
}
