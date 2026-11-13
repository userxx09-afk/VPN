// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

package com.tappervpn.ipn.remotecontrol

import android.app.Activity
import android.content.Context
import android.content.Intent
import android.graphics.BitmapFactory
import android.graphics.Rect
import android.os.Bundle
import android.view.KeyEvent
import android.view.MotionEvent
import android.view.SurfaceHolder
import android.view.SurfaceView
import com.tappervpn.ipn.util.TSLog
import kotlin.concurrent.thread

/**
 * Controller viewer: shows the remote device's MJPEG screen stream (one JPEG per
 * frame) and forwards this device's remote/keyboard/touch input to it.
 * Implements the Go-declared [libtappervpn.RemoteControlViewer]; the blocking
 * connect runs on a worker thread and this Activity finishes when the session
 * ends.
 */
class RemoteControlViewerActivity : Activity(), libtappervpn.RemoteControlViewer {
  private val TAG = "RemoteControlViewer"

  private lateinit var surfaceView: SurfaceView
  @Volatile private var surfaceReady = false

  // Negotiated host screen size, used to map local touches to host coordinates.
  @Volatile private var hostW = 0
  @Volatile private var hostH = 0

  private val app by lazy { RemoteControlManager.application() }

  override fun onCreate(savedInstanceState: Bundle?) {
    super.onCreate(savedInstanceState)
    surfaceView = SurfaceView(this)
    setContentView(surfaceView)
    surfaceView.holder.addCallback(
        object : SurfaceHolder.Callback {
          override fun surfaceCreated(holder: SurfaceHolder) {
            surfaceReady = true
          }

          override fun surfaceChanged(holder: SurfaceHolder, format: Int, w: Int, h: Int) {}

          override fun surfaceDestroyed(holder: SurfaceHolder) {
            surfaceReady = false
          }
        })

    val stableID = intent.getStringExtra(EXTRA_ID)
    if (stableID.isNullOrEmpty()) {
      finish()
      return
    }
    // connectRemoteControl blocks for the whole session; run it off the UI
    // thread and close when it returns (session ended or peer declined).
    thread(name = "rc-connect") {
      try {
        app.connectRemoteControl(stableID, this)
      } catch (e: Exception) {
        TSLog.e(TAG, "connect failed: ${e.message}")
      } finally {
        runOnUiThread { finish() }
      }
    }
  }

  override fun onDestroy() {
    app.stopRemoteControl()
    super.onDestroy()
  }

  // --- libtappervpn.RemoteControlViewer (called from Go session threads) ---

  override fun start(codec: String, width: Int, height: Int, hostKind: String) {
    hostW = width
    hostH = height
  }

  override fun frame(frame: ByteArray) {
    if (!surfaceReady) return
    val holder = surfaceView.holder
    val surface = holder.surface
    if (surface == null || !surface.isValid) return
    val bmp = BitmapFactory.decodeByteArray(frame, 0, frame.size) ?: return
    val canvas = holder.lockCanvas() ?: run { bmp.recycle(); return }
    try {
      canvas.drawBitmap(bmp, null, Rect(0, 0, canvas.width, canvas.height), null)
    } catch (e: Exception) {
      TSLog.e(TAG, "draw: ${e.message}")
    } finally {
      holder.unlockCanvasAndPost(canvas)
      bmp.recycle()
    }
  }

  override fun resize(width: Int, height: Int) {
    hostW = width
    hostH = height
  }

  override fun close() {
    runOnUiThread { finish() }
  }

  // --- input forwarding ---

  override fun onKeyDown(keyCode: Int, event: KeyEvent?): Boolean {
    if (forwardKey(keyCode, event)) return true
    return super.onKeyDown(keyCode, event)
  }

  override fun onKeyUp(keyCode: Int, event: KeyEvent?): Boolean {
    val nav = keyCodeToNav(keyCode)
    if (nav != 0) {
      app.sendRemoteControlInput(RC.NAV, 0, 0, nav, true, 0, "")
      return true
    }
    return super.onKeyUp(keyCode, event)
  }

  private fun forwardKey(keyCode: Int, event: KeyEvent?): Boolean {
    if (keyCodeToNav(keyCode) != 0) return true // handled on key-up
    val ch = event?.unicodeChar ?: 0
    if (ch != 0) {
      app.sendRemoteControlInput(RC.TEXT, 0, 0, 0, true, 0, ch.toChar().toString())
      return true
    }
    return false
  }

  private fun keyCodeToNav(keyCode: Int): Int =
      when (keyCode) {
        KeyEvent.KEYCODE_DPAD_UP -> RC.NAV_UP
        KeyEvent.KEYCODE_DPAD_DOWN -> RC.NAV_DOWN
        KeyEvent.KEYCODE_DPAD_LEFT -> RC.NAV_LEFT
        KeyEvent.KEYCODE_DPAD_RIGHT -> RC.NAV_RIGHT
        KeyEvent.KEYCODE_DPAD_CENTER,
        KeyEvent.KEYCODE_ENTER,
        KeyEvent.KEYCODE_NUMPAD_ENTER -> RC.NAV_OK
        KeyEvent.KEYCODE_BACK -> RC.NAV_BACK
        KeyEvent.KEYCODE_MENU -> RC.NAV_MENU
        KeyEvent.KEYCODE_MEDIA_PLAY_PAUSE -> RC.NAV_PLAY_PAUSE
        else -> 0
      }

  override fun onTouchEvent(event: MotionEvent?): Boolean {
    event ?: return false
    val vw = surfaceView.width.coerceAtLeast(1)
    val vh = surfaceView.height.coerceAtLeast(1)
    val hx = (event.x / vw * hostW).toInt()
    val hy = (event.y / vh * hostH).toInt()
    when (event.actionMasked) {
      MotionEvent.ACTION_DOWN,
      MotionEvent.ACTION_MOVE -> app.sendRemoteControlInput(RC.POINTER_MOVE, hx, hy, 0, true, 0, "")
      MotionEvent.ACTION_UP -> {
        app.sendRemoteControlInput(RC.POINTER_MOVE, hx, hy, 0, true, 0, "")
        app.sendRemoteControlInput(RC.POINTER_BUTTON, hx, hy, 0, true, 0, "")
        app.sendRemoteControlInput(RC.POINTER_BUTTON, hx, hy, 0, false, 0, "")
      }
    }
    return true
  }

  companion object {
    private const val EXTRA_ID = "stableID"

    fun launch(context: Context, stableID: String) {
      context.startActivity(
          Intent(context, RemoteControlViewerActivity::class.java)
              .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
              .putExtra(EXTRA_ID, stableID))
    }
  }
}
