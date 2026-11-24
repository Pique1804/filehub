package com.filehub.backup.ui

import com.filehub.backup.R
import android.content.Context
import androidx.biometric.BiometricManager
import androidx.biometric.BiometricManager.Authenticators.BIOMETRIC_WEAK
import androidx.biometric.BiometricPrompt
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Lock
import androidx.compose.material3.*
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.core.content.ContextCompat
import androidx.fragment.app.FragmentActivity

fun biometricAvailable(ctx: Context): Boolean =
    BiometricManager.from(ctx).canAuthenticate(BIOMETRIC_WEAK) == BiometricManager.BIOMETRIC_SUCCESS

/** Returns a function that shows the biometric prompt and calls onSuccess on auth. */
@Composable
fun rememberAppUnlock(): (() -> Unit) -> Unit {
    val activity = LocalContext.current as? FragmentActivity
    val text = LocalAppResources.current
    return { onSuccess ->
        if (activity == null) onSuccess()
        else {
            val prompt = BiometricPrompt(activity, ContextCompat.getMainExecutor(activity),
                object : BiometricPrompt.AuthenticationCallback() {
                    override fun onAuthenticationSucceeded(result: BiometricPrompt.AuthenticationResult) = onSuccess()
                })
            val info = BiometricPrompt.PromptInfo.Builder()
                .setTitle("filehub")
                .setSubtitle(text.getString(R.string.lock_prompt))
                .setAllowedAuthenticators(BIOMETRIC_WEAK)
                .setNegativeButtonText(text.getString(R.string.action_cancel))
                .build()
            try { prompt.authenticate(info) } catch (_: Exception) {}
        }
    }
}

@Composable
fun LockScreen(onUnlock: () -> Unit) {
    Box(Modifier.fillMaxSize().background(MaterialTheme.colorScheme.background), contentAlignment = Alignment.Center) {
        Column(horizontalAlignment = Alignment.CenterHorizontally) {
            Icon(Icons.Outlined.Lock, null, Modifier.size(64.dp), tint = MaterialTheme.colorScheme.primary)
            Spacer(Modifier.height(16.dp))
            Text(appString(R.string.lock_title), style = MaterialTheme.typography.titleMedium)
            Spacer(Modifier.height(20.dp))
            Button(onClick = onUnlock) { Text(appString(R.string.action_unlock)) }
        }
    }
}
