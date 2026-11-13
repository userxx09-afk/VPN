// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

package com.tappervpn.ipn.remotecontrol

import android.content.Context
import android.content.Intent
import android.os.Build
import android.util.DisplayMetrics
import android.view.WindowManager
import com.tappervpn.ipn.ui.util.AndroidTVUtil
import com.tappervpn.ipn.util.TSLog
import java.util.concurrent.SynchronousQueue
import java.util.concurrent.TimeUnit

/**
 * Numeric constants mirroring feature/remotecontrol/protocol.go. Kept in sync
 * by hand (both are small and stable); a mismatch would show up immediately in
 * manual testing as the wrong action firing.
 */
object RC {
  // InputKind
  const val POINTER_MOVE = 1
  const val POINTER_BUTTON = 2
  const val SCROLL = 3
  const val KEY = 4
  const val TEXT = 5
  const val NAV = 6

  // NavAction (carried in the "button" field of a NAV event)
  const val NAV_UP = 1
  const val NAV_DOWN = 2
  const val NAV_LEFT = 3
  const val NAV_RIGHT = 4
  const val NAV_OK = 5
  const val NAV_BACK = 6
  const val NAV_HOME = 7
  const val NAV_MENU = 8
  const val NAV_PLAY_PAUSE = 9
}

/**
 * RemoteControlManager is the Kotlin side of the remote-control feature. It
 * implements the Go-declared [libtappervpn.RemoteControlHost] callbacks (consent
 * prompt, screen info, capture control, input injection) and provides helpers
 * for the controller side.
 *
 * Security: this device is only ever captured or driven after the user accepts
 * a specific peer in [requestConsent]; the Go core returns HTTP 403 to the peer
 * on denial before any stream exists.
 */
object RemoteControlManager : libtappervpn.RemoteControlHost {
  private const val TAG = "RemoteControl"

  private lateinit var appContext: Context
  private lateinit var app: libtappervpn.Application

  // Screen-capture permission token captured during the consent step, consumed
  // by the host service's StartCapture. Guarded by this object's monitor.
  @Volatile var projectionResultCode: Int = 0
  @Volatile var projectionData: Intent? = null

  // Hand-off for the blocking consent call: the consent Activity offers its
  // decision here and requestConsent (running on a Go thread) takes it.
  private val consentExchange = SynchronousQueue<Boolean>()

  @Volatile private var currentPeerName: String? = null

  fun init(context: Context, application: libtappervpn.Application) {
    appContext = context.applicationContext
    app = application
    // Register as the host implementation so this device can be controlled
    // (always subject to the per-session consent prompt below).
    app.setRemoteControlHost(this)
    TSLog.d(TAG, "registered remote-control host agent")
  }

  /** The live Go application handle, for the controller side. */
  fun application(): libtappervpn.Application = app

  // --- libtappervpn.RemoteControlHost ---

  override fun kind(): String = if (AndroidTVUtil.isAndroidTV()) "firetv" else "android"

  // We capture with the hardware H.264 encoder (MediaCodec); advertise only
  // what we actually produce.
  override fun codecs(): String = "h264"

  override fun screenWidth(): Int = screenSize().first

  override fun screenHeight(): Int = screenSize().second

  override fun requestConsent(peerName: String, peerID: String, timeoutSeconds: Int): Boolean {
    currentPeerName = peerName
    // Reset any stale projection token from a previous attempt.
    projectionResultCode = 0
    projectionData = null

    RemoteControlConsentActivity.launch(appContext, peerName, peerID)
    return try {
      val allow = consentExchange.poll(timeoutSeconds.toLong(), TimeUnit.SECONDS)
      allow == true
    } catch (e: InterruptedException) {
      false
    }
  }

  /** Called by the consent Activity to deliver the user's decision. */
  fun deliverConsent(allow: Boolean) {
    // offer() is non-blocking; if requestConsent already timed out, drop it.
    consentExchange.offer(allow)
  }

  // Go declares StartCapture with an error return; gomobile maps that to a
  // thrown exception on the Kotlin side.
  @Throws(Exception::class)
  override fun startCapture(codec: String) {
    if (projectionData == null) {
      throw IllegalStateException("no screen-capture permission granted")
    }
    RemoteControlHostService.start(appContext, projectionResultCode, projectionData!!)
  }

  override fun stopCapture() {
    RemoteControlHostService.stop(appContext)
  }

  override fun injectInput(
      kind: Int,
      x: Int,
      y: Int,
      button: Int,
      down: Boolean,
      code: Int,
      text: String
  ) {
    RemoteControlAccessibilityService.instance?.inject(kind, x, y, button, down, code, text)
        ?: TSLog.d(TAG, "input dropped: accessibility service not enabled")
  }

  override fun sessionStarted(peerName: String) {
    currentPeerName = peerName
    RemoteControlHostService.updateControlledBy(appContext, peerName)
  }

  override fun sessionEnded(peerName: String) {
    currentPeerName = null
    // The host service stops its own capture when the Go session ends via
    // StopCapture; nothing further required here.
  }

  /** Pushes one encoded frame to the active host session. */
  fun pushFrame(frame: ByteArray) = app.pushRemoteControlFrame(frame)

  // --- controller side ---

  /** Opens the controller viewer and asks [stableID] to accept a session. */
  fun controlPeer(context: Context, stableID: String) =
      RemoteControlViewerActivity.launch(context, stableID)

  /**
   * Returns the JSON array of controllable peers (StableID/name/os) from the Go
   * localapi. Blocking — call off the main thread. Returns "[]" on error.
   */
  fun listTargetsJSON(): String =
      try {
        val resp = app.callLocalAPI(5_000, "GET", "/localapi/v0/remotecontrol/targets", null)
        (resp.bodyBytes() ?: ByteArray(0)).decodeToString()
      } catch (e: Exception) {
        TSLog.e(TAG, "listTargets failed: ${e.message}")
        "[]"
      }

  private fun screenSize(): Pair<Int, Int> {
    val wm = appContext.getSystemService(Context.WINDOW_SERVICE) as WindowManager
    return if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
      val b = wm.currentWindowMetrics.bounds
      Pair(b.width(), b.height())
    } else {
      val m = DisplayMetrics()
      @Suppress("DEPRECATION") wm.defaultDisplay.getRealMetrics(m)
      Pair(m.widthPixels, m.heightPixels)
    }
  }
}
