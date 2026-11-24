package com.filehub.backup.ui

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.selection.selectableGroup
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.Role
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.Typography
import androidx.compose.material3.lightColorScheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.Switch
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.runtime.SideEffect
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.graphics.toArgb
import androidx.core.view.WindowCompat
import androidx.compose.runtime.Composable
import androidx.compose.animation.animateColorAsState
import androidx.compose.animation.core.animateDpAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.Font
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.rememberTextMeasurer
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.filehub.backup.R

// Shared semantic colors keep every screen readable in light and dark appearance.
object Fh {
    val Bg: Color @Composable get() = MaterialTheme.colorScheme.background
    val BgAlt: Color @Composable get() = MaterialTheme.colorScheme.surfaceVariant
    val Card: Color @Composable get() = MaterialTheme.colorScheme.surface
    val Border: Color @Composable get() = MaterialTheme.colorScheme.outline
    val Divider: Color @Composable get() = MaterialTheme.colorScheme.outlineVariant
    val Text: Color @Composable get() = MaterialTheme.colorScheme.onSurface
    val Text2: Color @Composable get() = MaterialTheme.colorScheme.onSurfaceVariant
    val Muted: Color @Composable get() = MaterialTheme.colorScheme.onSurfaceVariant
    val Faint: Color @Composable get() = MaterialTheme.colorScheme.onSurfaceVariant
    val Chevron: Color @Composable get() = MaterialTheme.colorScheme.onSurfaceVariant
    val Accent: Color @Composable get() = MaterialTheme.colorScheme.primary
    val OnAccent: Color @Composable get() = MaterialTheme.colorScheme.onPrimary
    val AccentDeep: Color @Composable get() = MaterialTheme.colorScheme.onPrimaryContainer
    val AccentSoft: Color @Composable get() = MaterialTheme.colorScheme.primaryContainer
    val AccentPill: Color @Composable get() = MaterialTheme.colorScheme.primaryContainer
    val Green: Color @Composable get() = MaterialTheme.colorScheme.tertiary
    val GreenDeep: Color @Composable get() = MaterialTheme.colorScheme.tertiary
    val GreenSoft: Color @Composable get() = MaterialTheme.colorScheme.tertiaryContainer
    val Toast: Color @Composable get() = MaterialTheme.colorScheme.inverseSurface
    val Track: Color @Composable get() = MaterialTheme.colorScheme.outline
    val Dark = Color(0xFF090C12) // Media viewer deliberately stays dark.
    val Heart = Color(0xFFF75C66)
    val CardShape = RoundedCornerShape(16.dp)
    val TileShape = RoundedCornerShape(12.dp)
}

val Golos = FontFamily(
    Font(R.font.golos_400, FontWeight.Normal),
    Font(R.font.golos_500, FontWeight.Medium),
    Font(R.font.golos_600, FontWeight.SemiBold),
    Font(R.font.golos_700, FontWeight.Bold),
    Font(R.font.golos_800, FontWeight.ExtraBold),
)

private fun t(size: Int, weight: FontWeight, lh: Int = (size * 1.35f).toInt()) =
    TextStyle(fontFamily = Golos, fontSize = size.sp, fontWeight = weight, lineHeight = lh.sp)

private val FhTypography = Typography(
    displaySmall = t(26, FontWeight.Bold),
    headlineMedium = t(24, FontWeight.Bold),
    headlineSmall = t(21, FontWeight.Bold),
    titleLarge = t(19, FontWeight.Bold),
    titleMedium = t(16, FontWeight.Bold),
    titleSmall = t(14, FontWeight.SemiBold),
    bodyLarge = t(15, FontWeight.Normal),
    bodyMedium = t(14, FontWeight.Normal),
    bodySmall = t(12, FontWeight.Normal),
    labelLarge = t(14, FontWeight.SemiBold),
    labelMedium = t(12, FontWeight.SemiBold),
    labelSmall = t(11, FontWeight.Medium),
)

private val LightColors = lightColorScheme(
    primary = Color(0xFF006DE2), onPrimary = Color.White,
    primaryContainer = Color(0xFFE8F1FF), onPrimaryContainer = Color(0xFF0059BD),
    secondary = Color(0xFF526078), onSecondary = Color.White,
    secondaryContainer = Color(0xFFEDF1F7), onSecondaryContainer = Color(0xFF354154),
    tertiary = Color(0xFF207145), tertiaryContainer = Color(0xFFE4F3E9), onTertiaryContainer = Color(0xFF175332),
    background = Color(0xFFFAFAF8), onBackground = Color(0xFF1B2030),
    surface = Color.White, onSurface = Color(0xFF1B2030),
    surfaceVariant = Color(0xFFF0F2F4), onSurfaceVariant = Color(0xFF596274),
    outline = Color(0xFFCDD2DA), outlineVariant = Color(0xFFE9EBEF),
    error = Color(0xFFB52E35), errorContainer = Color(0xFFFFE9E9), onErrorContainer = Color(0xFF821E26),
    inverseSurface = Color(0xFF272D3C), inverseOnSurface = Color(0xFFF5F6FA),
)
private val DarkColors = darkColorScheme(
    primary = Color(0xFF93C5FF), onPrimary = Color(0xFF003264),
    primaryContainer = Color(0xFF17395E), onPrimaryContainer = Color(0xFFB5D7FF),
    secondary = Color(0xFFBEC9DC), onSecondary = Color(0xFF273246),
    secondaryContainer = Color(0xFF293345), onSecondaryContainer = Color(0xFFD7DFEE),
    tertiary = Color(0xFF8CD5A6), tertiaryContainer = Color(0xFF1B392B), onTertiaryContainer = Color(0xFFB5ECC7),
    background = Color(0xFF10141D), onBackground = Color(0xFFF0F2F7),
    surface = Color(0xFF1A202C), onSurface = Color(0xFFF0F2F7),
    surfaceVariant = Color(0xFF232B39), onSurfaceVariant = Color(0xFFB7C0CF),
    outline = Color(0xFF515E71), outlineVariant = Color(0xFF2C3443),
    error = Color(0xFFFFB4B8), errorContainer = Color(0xFF4D252C), onErrorContainer = Color(0xFFFFDADD),
    inverseSurface = Color(0xFFE3E8F0), inverseOnSurface = Color(0xFF272D3C),
)

@Composable
fun AppTheme(content: @Composable () -> Unit) {
    val appearance = rememberAppearance()
    val dark = appearance.mode == AppearanceMode.DARK ||
        (appearance.mode == AppearanceMode.SYSTEM && isSystemInDarkTheme())
    val colors = if (dark) DarkColors else LightColors
    val view = LocalView.current
    SideEffect {
        val window = view.context.findActivity()?.window
        if (window != null) {
            window.statusBarColor = colors.background.toArgb()
            window.navigationBarColor = (if (android.os.Build.VERSION.SDK_INT >= 27) colors.surface else Color(0xFF1B2030)).toArgb()
            WindowCompat.getInsetsController(window, view).apply {
                isAppearanceLightStatusBars = !dark
                isAppearanceLightNavigationBars = !dark
            }
        }
    }
    CompositionLocalProvider(LocalAppearance provides appearance) {
        ProvideAppLanguage {
            MaterialTheme(colorScheme = colors, typography = FhTypography, shapes = androidx.compose.material3.Shapes(
                small = Fh.TileShape, medium = Fh.CardShape, large = Fh.CardShape, extraLarge = Fh.CardShape), content = content)
        }
    }
}

@Composable
fun FhCard(modifier: Modifier = Modifier, shape: RoundedCornerShape = Fh.CardShape, content: @Composable () -> Unit) {
    Surface(modifier = modifier, shape = shape, color = Fh.Card, content = content)
}

@Composable
fun FhRoundButton(onClick: () -> Unit, modifier: Modifier = Modifier, size: Int = 48, content: @Composable () -> Unit) {
    Surface(
        modifier = modifier.size(size.coerceAtLeast(48).dp), shape = Fh.TileShape,
        color = Color.Transparent, onClick = onClick,
    ) { Box(contentAlignment = Alignment.Center) { content() } }
}

@Composable
fun FhSwitch(checked: Boolean, onChecked: (Boolean) -> Unit, label: String? = null) {
    val description = label ?: appString(R.string.backup_auto_upload)
    Switch(checked = checked, onCheckedChange = onChecked, modifier = Modifier.heightIn(min = 48.dp).semantics { contentDescription = description })
}

/** Small gray section label above grouped cards («Папки», «Куда сохранять»…). */
@Composable
fun FhSectionLabel(text: String, modifier: Modifier = Modifier) {
    Text(text, modifier, color = Fh.Muted, fontSize = 12.5.sp, fontWeight = FontWeight.SemiBold, fontFamily = Golos)
}

/** Segmented control row: equal-width pills, accent-filled when selected. */
@Composable
fun FhSegmented(
    options: List<Pair<String, String>>,
    selected: String,
    onSelect: (String) -> Unit,
    modifier: Modifier = Modifier,
    fitLabels: Boolean = false,
) {
    val density = LocalDensity.current
    val labelStyle = TextStyle(fontSize = 13.sp, fontWeight = FontWeight.SemiBold, fontFamily = Golos)
    BoxWithConstraints(modifier.fillMaxWidth()) {
        val stacked = if (fitLabels && options.isNotEmpty()) {
            val measurer = rememberTextMeasurer()
            val available = with(density) {
                ((constraints.maxWidth - (options.size - 1) * 8.dp.roundToPx()) / options.size) - 2 * 10.dp.roundToPx()
            }
            options.any { (_, label) -> measurer.measure(label, style = labelStyle, maxLines = 1).size.width > available }
        } else {
            (options.size >= 3 && maxWidth < 300.dp) || density.fontScale > 1.2f
        }
        val option: @Composable (Pair<String, String>, Modifier) -> Unit = { (id, label), itemModifier ->
            val on = id == selected
            Surface(
                onClick = { onSelect(id) },
                modifier = itemModifier.semantics { this.selected = on; role = Role.RadioButton },
                shape = Fh.TileShape, color = if (on) Fh.Accent else Fh.Card,
                border = BorderStroke(1.dp, if (on) Fh.Accent else Fh.Border),
            ) {
                Box(Modifier.heightIn(min = 48.dp).padding(horizontal = 10.dp, vertical = 12.dp), contentAlignment = Alignment.Center) {
                    Text(label, color = if (on) Fh.OnAccent else Fh.Text2,
                        style = labelStyle, textAlign = TextAlign.Center,
                        maxLines = if (fitLabels) Int.MAX_VALUE else 1,
                        overflow = if (fitLabels) TextOverflow.Clip else TextOverflow.Ellipsis)
                }
            }
        }
        if (stacked) Column(Modifier.fillMaxWidth().selectableGroup(), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            options.forEach { option(it, Modifier.fillMaxWidth()) }
        } else Row(Modifier.fillMaxWidth().selectableGroup(), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            options.forEach { option(it, Modifier.weight(1f)) }
        }
    }
}

/** «File hub» wordmark: dark + accent. */
@Composable
fun FhWordmark(size: Int = 21) {
    Row {
        Text("File", fontFamily = Golos, fontWeight = FontWeight.ExtraBold, fontSize = size.sp, color = Fh.Text, letterSpacing = (-0.5).sp)
        Text("hub", fontFamily = Golos, fontWeight = FontWeight.ExtraBold, fontSize = size.sp, color = Fh.Accent, letterSpacing = (-0.5).sp)
    }
}
