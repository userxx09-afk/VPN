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
import android.widget.Toast
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
    // A PC needs a mouse, which a TV remote doesn't have: the D-pad moves a
    // pointer instead (Menu switches back to arrow keys).
    if (hostKind == "windows") {
      pointerX = width / 2
      pointerY = height / 2
      pointerMode = true
      runOnUiThread {
        Toast.makeText(
                this,
                "D-pad moves the mouse · OK clicks · hold OK to right-click\n" +
                    "Rewind/Fast-forward scroll · Menu switches to arrow keys · hold Back to exit",
                Toast.LENGTH_LONG)
            .show()
      }
    }
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

  // Pointer mode (controlling a PC from a TV remote): host-pixel position.
  @Volatile private var pointerMode = false
  @Volatile private var pointerX = 0
  @Volatile private var pointerY = 0
  private var okLongPressed = false

  override fun onKeyDown(keyCode: Int, event: KeyEvent?): Boolean {
    // Holding Back leaves the session (a short press is sent as Esc/Back).
    if (keyCode == KeyEvent.KEYCODE_BACK && (event?.repeatCount ?: 0) > 0) {
      finish()
      return true
    }
    if (pointerKeyDown(keyCode, event)) return true
    if (forwardKey(keyCode, event)) return true
    return super.onKeyDown(keyCode, event)
  }

  private fun pointerKeyDown(keyCode: Int, event: KeyEvent?): Boolean {
    when (keyCode) {
      KeyEvent.KEYCODE_MENU -> {
        if ((event?.repeatCount ?: 0) == 0 && hostW > 0) {
          pointerMode = !pointerMode
          Toast.makeText(
                  this,
                  if (pointerMode) "D-pad moves the mouse" else "D-pad sends arrow keys",
                  Toast.LENGTH_SHORT)
              .show()
        }
        return true
      }
      KeyEvent.KEYCODE_MEDIA_REWIND,
      KeyEvent.KEYCODE_CHANNEL_UP,
      KeyEvent.KEYCODE_PAGE_UP -> {
        app.sendRemoteControlInput(RC.SCROLL, 0, 3, 0, true, 0, "")
        return true
      }
      KeyEvent.KEYCODE_MEDIA_FAST_FORWARD,
      KeyEvent.KEYCODE_CHANNEL_DOWN,
      KeyEvent.KEYCODE_PAGE_DOWN -> {
        app.sendRemoteControlInput(RC.SCROLL, 0, -3, 0, true, 0, "")
        return true
      }
    }
    if (!pointerMode) return false
    val repeat = event?.repeatCount ?: 0
    // Small steps for a tap, speeding up while the button is held.
    val base = (hostW / 120).coerceAtLeast(4)
    val step = base * (1 + repeat.coerceAtMost(12))
    when (keyCode) {
      KeyEvent.KEYCODE_DPAD_UP -> pointerY -= step
      KeyEvent.KEYCODE_DPAD_DOWN -> pointerY += step
      KeyEvent.KEYCODE_DPAD_LEFT -> pointerX -= step
      KeyEvent.KEYCODE_DPAD_RIGHT -> pointerX += step
      KeyEvent.KEYCODE_DPAD_CENTER,
      KeyEvent.KEYCODE_ENTER,
      KeyEvent.KEYCODE_NUMPAD_ENTER -> {
        if (repeat == 0) okLongPressed = false
        if (repeat == 15 && !okLongPressed) { // held about half a second
          okLongPressed = true
          click(1)
        }
        return true // a normal click is sent on release
      }
      else -> return false
    }
    pointerX = pointerX.coerceIn(0, (hostW - 1).coerceAtLeast(0))
    pointerY = pointerY.coerceIn(0, (hostH - 1).coerceAtLeast(0))
    app.sendRemoteControlInput(RC.POINTER_MOVE, pointerX, pointerY, 0, true, 0, "")
    return true
  }

  private fun click(button: Int) {
    app.sendRemoteControlInput(RC.POINTER_MOVE, pointerX, pointerY, 0, true, 0, "")
    app.sendRemoteControlInput(RC.POINTER_BUTTON, pointerX, pointerY, button, true, 0, "")
    app.sendRemoteControlInput(RC.POINTER_BUTTON, pointerX, pointerY, button, false, 0, "")
  }

  override fun onKeyUp(keyCode: Int, event: KeyEvent?): Boolean {
    if (pointerMode) {
      when (keyCode) {
        KeyEvent.KEYCODE_DPAD_UP,
        KeyEvent.KEYCODE_DPAD_DOWN,
        KeyEvent.KEYCODE_DPAD_LEFT,
        KeyEvent.KEYCODE_DPAD_RIGHT -> return true
        KeyEvent.KEYCODE_DPAD_CENTER,
        KeyEvent.KEYCODE_ENTER,
        KeyEvent.KEYCODE_NUMPAD_ENTER -> {
          if (!okLongPressed) click(0)
          okLongPressed = false
          return true
        }
      }
    }
    if (keyCode == KeyEvent.KEYCODE_MENU) return true
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
    pointerX = hx
    pointerY = hy
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
