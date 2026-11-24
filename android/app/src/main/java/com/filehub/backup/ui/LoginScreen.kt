package com.filehub.backup.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Dns
import androidx.compose.material.icons.outlined.Lock
import androidx.compose.material.icons.outlined.Person
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.filehub.backup.Api
import com.filehub.backup.Prefs
import com.filehub.backup.R
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

@Composable
fun LoginScreen(prefs: Prefs, onLoggedIn: () -> Unit) {
    val scope = rememberCoroutineScope()
    var server by rememberSaveable { mutableStateOf(prefs.server) }
    var user by rememberSaveable { mutableStateOf(prefs.user) }
    var pass by remember { mutableStateOf(prefs.pass) }
    var busy by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<Int?>(null) }

    val fieldColors = OutlinedTextFieldDefaults.colors(
        focusedBorderColor = Fh.Accent,
        unfocusedBorderColor = Fh.Border,
        focusedLabelColor = Fh.Accent,
        unfocusedLabelColor = Fh.Muted,
        cursorColor = Fh.Accent,
        focusedLeadingIconColor = Fh.AccentDeep,
        unfocusedLeadingIconColor = Fh.Muted,
        focusedTextColor = Fh.Text,
        unfocusedTextColor = Fh.Text,
    )

    Box(
        Modifier.fillMaxSize().background(Fh.Bg).imePadding().verticalScroll(rememberScrollState()),
        contentAlignment = Alignment.Center
    ) {
        Column(
            Modifier.widthIn(max = 440.dp).fillMaxWidth().padding(horizontal = 24.dp, vertical = 32.dp),
            horizontalAlignment = Alignment.CenterHorizontally
        ) {
            FhWordmark(30)
            Spacer(Modifier.height(24.dp))
            Text(appString(R.string.login_title), color = Fh.Text, style = MaterialTheme.typography.headlineMedium)
            Spacer(Modifier.height(8.dp))
            Text(appString(R.string.login_subtitle), color = Fh.Muted,
                style = MaterialTheme.typography.bodyMedium, textAlign = androidx.compose.ui.text.style.TextAlign.Center)
            Spacer(Modifier.height(24.dp))

            LanguagePicker(Modifier.fillMaxWidth())
            Spacer(Modifier.height(20.dp))

            FhCard(Modifier.fillMaxWidth()) {
                Column(Modifier.padding(horizontal = 18.dp, vertical = 20.dp)) {
                    OutlinedTextField(server, { server = it; pass = "" }, enabled = !busy, placeholder = { Text("http://filehub.local:8080") }, label = { Text(appString(R.string.login_server)) }, singleLine = true,
                        leadingIcon = { Icon(Icons.Outlined.Dns, null) },
                        shape = RoundedCornerShape(12.dp), colors = fieldColors,
                        modifier = Modifier.fillMaxWidth())
                    Spacer(Modifier.height(10.dp))
                    OutlinedTextField(user, { user = it; pass = "" }, enabled = !busy, label = { Text(appString(R.string.login_username)) }, singleLine = true,
                        leadingIcon = { Icon(Icons.Outlined.Person, null) },
                        shape = RoundedCornerShape(12.dp), colors = fieldColors,
                        modifier = Modifier.fillMaxWidth())
                    Spacer(Modifier.height(10.dp))
                    OutlinedTextField(pass, { pass = it }, enabled = !busy, label = { Text(appString(R.string.login_password)) }, singleLine = true,
                        visualTransformation = PasswordVisualTransformation(),
                        leadingIcon = { Icon(Icons.Outlined.Lock, null) },
                        shape = RoundedCornerShape(12.dp), colors = fieldColors,
                        modifier = Modifier.fillMaxWidth())

                    error?.let { message ->
                        Spacer(Modifier.height(10.dp))
                        Text(appString(message), color = MaterialTheme.colorScheme.error, fontSize = 12.5.sp, fontFamily = Golos)
                    }

                    Spacer(Modifier.height(20.dp))
                    Button(
                        onClick = {
                            error = null; busy = true
                            val submittedServer = server.trim(); val submittedUser = user.trim(); val submittedPass = pass
                            scope.launch {
                                val ok = withContext(Dispatchers.IO) { Api.login(submittedServer, submittedUser, submittedPass, replaceAccount = true) }
                                busy = false
                                if (ok) {
                                    try {
                                        withContext(Dispatchers.IO) { prefs.pass = submittedPass }
                                        prefs.server = submittedServer; prefs.user = submittedUser
                                        pass = ""
                                        onLoggedIn()
                                    } catch (_: Exception) {
                                        error=R.string.login_password_storage_error
                                    }
                                } else error = R.string.login_invalid_credentials_or_server
                            }
                        },
                        enabled = !busy && server.isNotBlank() && user.isNotBlank(),
                        modifier = Modifier.fillMaxWidth(),
                        shape = RoundedCornerShape(12.dp),
                        colors = ButtonDefaults.buttonColors(containerColor = Fh.Accent, contentColor = Fh.OnAccent),
                        contentPadding = PaddingValues(vertical = 14.dp)
                    ) {
                        if (busy) CircularProgressIndicator(Modifier.size(22.dp), color = Fh.OnAccent, strokeWidth = 2.dp)
                        else Text(appString(R.string.action_sign_in), fontSize = 15.sp, fontWeight = FontWeight.Bold, fontFamily = Golos)
                    }
                }
            }
        }
    }
}
