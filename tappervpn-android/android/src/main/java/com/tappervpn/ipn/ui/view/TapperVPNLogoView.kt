// Copyright (c) TapperVPN contributors
// SPDX-License-Identifier: BSD-3-Clause

package com.tappervpn.ipn.ui.view

import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.graphics.drawscope.Stroke
import com.tappervpn.ipn.ui.theme.onBackgroundLogoDotEnabled

// The TapperVPN logo: a neon green "T →" gate sign on black, after the
// "Gates →" sign in the family photo it's based on. Exit nodes are gateways.
val LogoBlack = Color(0xFF000000)
val LogoPlate = Color(0xFF0A0A0A)
val LogoPlateEdge = Color(0xFF1F6B2E)
val LogoGreen = Color(0xFF39FF14)

/**
 * Draws the logo in [modifier]'s (square) bounds. [animated] slides the arrow forward, for
 * loading states. [usesOnBackgroundColors] draws only the "T →" in the theme's on-background
 * color, for placing the logo on a colored surface.
 */
@Composable
fun TapperVPNLogoView(
    animated: Boolean = false,
    usesOnBackgroundColors: Boolean = false,
    modifier: Modifier,
) {
  val ink = if (usesOnBackgroundColors) MaterialTheme.colorScheme.onBackgroundLogoDotEnabled else LogoGreen
  val shift =
      if (animated) {
        rememberInfiniteTransition(label = "logo")
            .animateFloat(
                initialValue = -0.06f,
                targetValue = 0.06f,
                animationSpec = infiniteRepeatable(tween(600, easing = LinearEasing), RepeatMode.Reverse),
                label = "arrow")
            .value
      } else {
        0f
      }

  Canvas(modifier = modifier) {
    val s = size.minDimension
    if (!usesOnBackgroundColors) {
      drawRoundRect(LogoBlack, size = Size(s, s), cornerRadius = CornerRadius(s * 0.22f))
      val plateTop = Offset(s * 0.17f, s * 0.35f)
      val plateSize = Size(s * 0.66f, s * 0.30f)
      drawRoundRect(LogoPlate, plateTop, plateSize, CornerRadius(s * 0.04f))
      drawRoundRect(
          LogoPlateEdge, plateTop, plateSize, CornerRadius(s * 0.04f), style = Stroke(s * 0.014f))
    }
    drawSign(s, ink, shift)
  }
}

private fun DrawScope.drawSign(s: Float, ink: Color, shift: Float) {
  val w = s * 0.046f
  // "T"
  drawLine(ink, Offset(s * 0.25f, s * 0.425f), Offset(s * 0.39f, s * 0.425f), w, StrokeCap.Square)
  drawLine(ink, Offset(s * 0.32f, s * 0.425f), Offset(s * 0.32f, s * 0.585f), w, StrokeCap.Square)
  // "→"
  val y = s * 0.5f
  val x0 = s * (0.45f + shift)
  val x1 = s * (0.72f + shift)
  val head = s * 0.085f
  drawLine(ink, Offset(x0, y), Offset(x1, y), w, StrokeCap.Round)
  drawLine(ink, Offset(x1 - head, y - head), Offset(x1, y), w, StrokeCap.Round)
  drawLine(ink, Offset(x1 - head, y + head), Offset(x1, y), w, StrokeCap.Round)
}
