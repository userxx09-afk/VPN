// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

package com.tappervpn.ipn.remotecontrol

import android.app.Activity
import android.content.Context
import android.content.Intent
import android.media.MediaCodec
import android.media.MediaFormat
import android.os.Bundle
import android.view.KeyEvent
import android.view.MotionEvent
import android.view.Surface
import android.view.SurfaceHolder
import android.view.SurfaceView
import com.tappervpn.ipn.util.TSLog
import java.nio.ByteBuffer
import kotlin.concurrent.thread

/**
 * Controller viewer: shows the remote device's H.264 screen stream and forwards
 * this device's remote/keyboard/touch input to it. Implements the Go-declared
 * [libtappervpn.RemoteControlViewer]; the blocking connect runs on a worker
 * thread and this Activity finishes when the session ends.
 */
class RemoteControlViewerActivity : Activity(), libtappervpn.RemoteControlViewer {
  private val TAG = "RemoteControlViewer"

  private lateinit var surfaceView: SurfaceView
  @Volatile private var surface: Surface? = null
  @Volatile private var decoder: MediaCodec? = null

  // Negotiated host screen size, used to map local touches to host coordinates.
  @Volatile private var hostW = 0
  @Volatile private var hostH = 0
  @Volatile private var started = false

  private val app by lazy { RemoteControlManager.application() }

  override fun onCreate(savedInstanceState: Bundle?) {
    super.onCreate(savedInstanceState)
    surfaceView = SurfaceView(this)
    setContentView(surfaceView)
    surfaceView.holder.addCallback(
        object : SurfaceHolder.Callback {
          override fun surfaceCreated(holder: SurfaceHolder) {
            surface = holder.surface
            maybeStartDecoder()
          }

          override fun surfaceChanged(holder: SurfaceHolder, format: Int, w: Int, h: Int) {}

          override fun surfaceDestroyed(holder: SurfaceHolder) {
            surface = null
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
    releaseDecoder()
    super.onDestroy()
  }

  // --- libtappervpn.RemoteControlViewer (called from Go session threads) ---

  override fun start(codec: String, width: Int, height: Int, hostKind: String) {
    hostW = width
    hostH = height
    started = true
    maybeStartDecoder()
  }

  override fun frame(frame: ByteArray) {
    val dec = decoder ?: return
    try {
      val inIndex = dec.dequeueInputBuffer(10_000)
      if (inIndex >= 0) {
        val buf: ByteBuffer = dec.getInputBuffer(inIndex) ?: return
        buf.clear()
        buf.put(frame)
        dec.queueInputBuffer(inIndex, 0, frame.size, System.nanoTime() / 1000, 0)
      }
    } catch (e: IllegalStateException) {
      TSLog.e(TAG, "decoder input: ${e.message}")
    }
  }

  override fun resize(width: Int, height: Int) {
    hostW = width
    hostH = height
  }

  override fun close() {
    runOnUiThread { finish() }
  }

  @Synchronized
  private fun maybeStartDecoder() {
    if (decoder != null || !started) return
    val s = surface ?: return
    if (hostW <= 0 || hostH <= 0) return
    try {
      val format = MediaFormat.createVideoFormat(MediaFormat.MIMETYPE_VIDEO_AVC, hostW, hostH)
      val dec = MediaCodec.createDecoderByType(MediaFormat.MIMETYPE_VIDEO_AVC)
      dec.configure(format, s, null, 0)
      dec.start()
      decoder = dec
      thread(name = "rc-decoder") { drainDecoder(dec) }
    } catch (e: Exception) {
      TSLog.e(TAG, "decoder start failed: ${e.message}")
    }
  }

  private fun drainDecoder(dec: MediaCodec) {
    val info = MediaCodec.BufferInfo()
    while (decoder === dec) {
      try {
        val outIndex = dec.dequeueOutputBuffer(info, 10_000)
        if (outIndex >= 0) {
          dec.releaseOutputBuffer(outIndex, true) // render to surface
        }
      } catch (e: IllegalStateException) {
        break
      }
    }
  }

  private fun releaseDecoder() {
    val dec = decoder
    decoder = null
    runCatching {
      dec?.stop()
      dec?.release()
    }
  }

  // --- input forwarding ---

  override fun onKeyDown(keyCode: Int, event: KeyEvent?): Boolean {
    if (forwardKey(keyCode, event, down = true)) return true
    return super.onKeyDown(keyCode, event)
  }

  override fun onKeyUp(keyCode: Int, event: KeyEvent?): Boolean {
    val nav = keyCodeToNav(keyCode)
    if (nav != 0) {
      // Fire a nav action on key-up (one discrete action per press).
      app.sendRemoteControlInput(RC.NAV, 0, 0, nav, true, 0, "")
      return true
    }
    return super.onKeyUp(keyCode, event)
  }

  private fun forwardKey(keyCode: Int, event: KeyEvent?, down: Boolean): Boolean {
    if (keyCodeToNav(keyCode) != 0) return true // handled on key-up
    val ch = event?.unicodeChar ?: 0
    if (ch != 0) {
      app.sendRemoteControlInput(RC.TEXT, 0, 0, 0, down, 0, ch.toChar().toString())
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
