// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

package com.tappervpn.ipn.ui.view

import android.content.Context
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Button
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.repeatOnLifecycle
import com.tappervpn.ipn.AppExitNodes
import com.tappervpn.ipn.R
import com.tappervpn.ipn.ui.theme.warningListItem

private const val PREFS = "usage_access_notice"
private const val DISMISSED = "dismissed"

/**
 * After TapperVPN is installed, a reminder on the main screen to turn on Usage access (needed for
 * per-app exit nodes), with a button straight into that settings screen. It goes away once access
 * is on or the user taps "Not now". Not shown on devices with no such screen (Fire TV).
 */
@Composable
fun UsageAccessNotice() {
  val context = LocalContext.current
  val prefs = remember { context.getSharedPreferences(PREFS, Context.MODE_PRIVATE) }
  var dismissed by remember { mutableStateOf(prefs.getBoolean(DISMISSED, false)) }
  var hasAccess by remember { mutableStateOf(AppExitNodes.hasUsageAccess(context)) }
  val intent = remember { AppExitNodes.usageAccessIntent(context) }

  // Check again each time the user comes back, e.g. from Settings with access turned on.
  val lifecycleOwner = LocalLifecycleOwner.current
  LaunchedEffect(Unit) {
    lifecycleOwner.lifecycle.repeatOnLifecycle(Lifecycle.State.RESUMED) {
      hasAccess = AppExitNodes.hasUsageAccess(context)
    }
  }

  if (dismissed || hasAccess || intent == null) return

  Box(modifier = Modifier.background(color = MaterialTheme.colorScheme.surfaceContainer)) {
    Box(
        modifier =
            Modifier.padding(start = 16.dp, end = 16.dp, top = 8.dp, bottom = 8.dp)
                .clip(shape = RoundedCornerShape(10.dp))
                .fillMaxWidth()
    ) {
      ListItem(
          colors = MaterialTheme.colorScheme.warningListItem,
          headlineContent = {
            Text(
                stringResource(R.string.usage_access_notice_title),
                style = MaterialTheme.typography.titleMedium,
            )
          },
          supportingContent = {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
              Text(
                  stringResource(R.string.usage_access_notice_body),
                  style = MaterialTheme.typography.bodyMedium,
              )
              Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Button(
                    onClick = {
                      try {
                        context.startActivity(intent)
                      } catch (e: Exception) {
                        // No settings screen after all; nothing to open.
                      }
                    }
                ) {
                  Text(stringResource(R.string.usage_access_notice_open))
                }
                TextButton(
                    onClick = {
                      prefs.edit().putBoolean(DISMISSED, true).apply()
                      dismissed = true
                    }
                ) {
                  Text(stringResource(R.string.usage_access_notice_later))
                }
              }
            }
          },
      )
    }
  }
}
