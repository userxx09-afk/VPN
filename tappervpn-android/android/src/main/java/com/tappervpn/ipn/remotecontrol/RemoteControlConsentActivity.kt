// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

package com.tappervpn.ipn.remotecontrol

import android.app.Activity
import android.content.Context
import android.content.Intent
import android.media.projection.MediaProjectionManager
import android.os.Build
import android.os.Bundle
import androidx.compose.foundation.layout.height
import com.tappervpn.ipn.util.TSLog
import android.widget.Toast
import android.provider.Settings
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.material3.Button
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import com.tappervpn.ipn.ui.theme.AppTheme

/**
 * Full-screen prompt shown when a peer asks to control this device. It is the
 * single gate: unless the user taps Allow (and then grants screen capture), the
 * session never starts. Designed to be drivable with a TV remote (visible,
 * focusable buttons; Back = deny).
 */
class RemoteControlConsentActivity : ComponentActivity() {

  private var decided = false

  private val projectionLauncher =
      registerForActivityResult(ActivityResultContracts.StartActivityForResult()) { result ->
        if (result.resultCode == Activity.RESULT_OK && result.data != null) {
          RemoteControlManager.projectionResultCode = result.resultCode
          RemoteControlManager.projectionData = result.data
          if (!RemoteControlAccessibilityService.isEnabled() && !isFireTv()) openControlSetting()
          finishWith(true)
        } else {
          finishWith(false)
        }
      }

  override fun onCreate(savedInstanceState: Bundle?) {
    super.onCreate(savedInstanceState)
    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O_MR1) {
      setShowWhenLocked(true)
      setTurnScreenOn(true)
    }

    val peerName = intent.getStringExtra(EXTRA_PEER_NAME) ?: "A device"

    setContent {
      AppTheme {
        val allowFocus = remember { FocusRequester() }
        LaunchedEffect(Unit) { runCatching { allowFocus.requestFocus() } }
        Surface(modifier = Modifier.fillMaxSize()) {
          Column(
              modifier = Modifier.fillMaxSize().padding(32.dp),
              verticalArrangement = Arrangement.Center,
              horizontalAlignment = Alignment.CenterHorizontally,
          ) {
            Text(
                text = "Allow remote control?",
                textAlign = TextAlign.Center,
                modifier = Modifier.padding(bottom = 12.dp),
            )
            Text(
                text =
                    "“$peerName” on your TapperVPN network wants to view this " +
                        "screen and control this device. This only works while you allow it.",
                textAlign = TextAlign.Center,
                modifier = Modifier.padding(bottom = 16.dp),
            )
            if (!RemoteControlAccessibilityService.isEnabled()) {
              Text(
                  text =
                      if (isFireTv())
                          "Control (not just watching) isn't switched on for this TV yet. Fire TV " +
                              "doesn't show it in Accessibility settings: the Tapper Dashboard " +
                              "turns it on by itself within 15 minutes."
                      else
                          "To let them control it (not just watch), turn on TapperVPN in " +
                              "Accessibility settings. After you choose Allow, they'll open for you.",
                  textAlign = TextAlign.Center,
                  modifier = Modifier.padding(bottom = 16.dp),
              )
            }
            Spacer(Modifier.height(16.dp))
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.Center,
            ) {
              OutlinedButton(onClick = { finishWith(false) }) { Text("Deny") }
              Spacer(Modifier.width(24.dp))
              Button(
                  onClick = { onAllow() },
                  modifier = Modifier.focusRequester(allowFocus),
              ) {
                Text("Allow")
              }
            }
          }
        }
      }
    }
  }

  private fun onAllow() {
    // Allowing control also requires the system screen-capture consent, so we
    // chain MediaProjection's own permission dialog here.
    val mpm = getSystemService(MEDIA_PROJECTION_SERVICE) as MediaProjectionManager
    projectionLauncher.launch(mpm.createScreenCaptureIntent())
  }

  // Fire TV's Accessibility settings only list Amazon's own services, so the
  // dashboard on the home server switches ours on over adb instead.
  private fun isFireTv() = packageManager.hasSystemFeature("amazon.hardware.fire_tv")

  /** Opens Accessibility settings so the user can turn on remote control input. */
  private fun openControlSetting() {
    runCatching {
      startActivity(
          Intent(Settings.ACTION_ACCESSIBILITY_SETTINGS).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
      Toast.makeText(
              this,
              "Turn on “TapperVPN” here so the other device can control this one",
              Toast.LENGTH_LONG)
          .show()
    }
        .onFailure { TSLog.e("RemoteControlConsent", "accessibility settings: ${it.message}") }
  }

  // Back button or any other dismissal is a denial.
  override fun onDestroy() {
    if (!decided) RemoteControlManager.deliverConsent(false)
    super.onDestroy()
  }

  private fun finishWith(allow: Boolean) {
    if (!decided) {
      decided = true
      RemoteControlManager.deliverConsent(allow)
    }
    finish()
  }

  companion object {
    private const val EXTRA_PEER_NAME = "peerName"
    private const val EXTRA_PEER_ID = "peerID"

    fun launch(context: Context, peerName: String, peerID: String) {
      val i =
          Intent(context, RemoteControlConsentActivity::class.java).apply {
            addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
            putExtra(EXTRA_PEER_NAME, peerName)
            putExtra(EXTRA_PEER_ID, peerID)
          }
      context.startActivity(i)
    }
  }
}
