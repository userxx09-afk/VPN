// Copyright (c) TapperVPN contributors
// SPDX-License-Identifier: BSD-3-Clause

package com.tappervpn.ipn.ui.view

import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.BiasAlignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import com.tappervpn.ipn.R

/**
 * The June 1983 photo (the one the logo comes from) as a banner across the top
 * of the main screen, cropped to the three people and faded into the screen.
 */
@Composable
fun HeaderPhoto(height: Dp = 170.dp) {
  val fadeTo = MaterialTheme.colorScheme.surface
  Box(modifier = Modifier.fillMaxWidth().height(height)) {
    Image(
        painter = painterResource(R.drawable.about_photo_june_1983),
        contentDescription = stringResource(R.string.about_photo_description),
        contentScale = ContentScale.Crop,
        // Keep the people (lower half of the photo) in frame.
        alignment = BiasAlignment(horizontalBias = 0f, verticalBias = 0.6f),
        modifier = Modifier.fillMaxWidth().height(height),
    )
    Box(
        modifier =
            Modifier.fillMaxWidth()
                .height(height / 3)
                .align(Alignment.BottomCenter)
                .background(Brush.verticalGradient(listOf(Color.Transparent, fadeTo)))
    )
  }
}
