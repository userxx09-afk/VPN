// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

package com.tappervpn.ipn.remotecontrol

import android.app.Activity
import android.graphics.Paint
import android.graphics.Color
import android.app.AlertDialog
import android.content.Context
import android.content.Intent
import android.graphics.BitmapFactory
import android.graphics.Rect
import android.os.Bundle
import android.view.KeyEvent
import android.view.MotionEvent
import android.view.SurfaceHolder
import android.view.Gravity
import android.view.ScaleGestureDetector
import android.view.SurfaceView
import android.widget.Button
import android.widget.FrameLayout
import android.widget.LinearLayout
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
    val root = FrameLayout(this)
    root.addView(surfaceView, FrameLayout.LayoutParams(-1, -1))
    // On a touchscreen, zoom buttons make small boxes and links easy to hit
    // (pinch to zoom, two fingers to move around also work).
    if (packageManager.hasSystemFeature(android.content.pm.PackageManager.FEATURE_TOUCHSCREEN)) {
      root.addView(zoomButtons(), FrameLayout.LayoutParams(-2, -2, Gravity.END or Gravity.BOTTOM))
    }
    setContentView(root)
    scaleDetector = ScaleGestureDetector(this, object : ScaleGestureDetector.SimpleOnScaleGestureListener() {
      override fun onScale(d: ScaleGestureDetector): Boolean {
        zoomBy(d.scaleFactor, d.focusX, d.focusY)
        return true
      }
    })
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
    if (hostKind == "firetv" || hostKind == "android") {
      runOnUiThread {
        Toast.makeText(
                this,
                "Remote/arrow keys move around, OK selects, touch to tap · Back to leave" +
                    zoomHint(),
                Toast.LENGTH_LONG)
            .show()
      }
    }
    if (hostKind == "windows") {
      pointerX = width / 2
      pointerY = height / 2
      pointerMode = true
      runOnUiThread {
        Toast.makeText(
                this,
                "D-pad moves the mouse · OK clicks · hold OK to right-click\n" +
                    "Rewind/Fast-forward scroll · Menu switches to arrow keys · Back to leave" +
                    zoomHint(),
                Toast.LENGTH_LONG)
            .show()
      }
    }
  }

  override fun frame(frame: ByteArray) {
    lastFrame = frame
    draw(frame)
  }

  @Volatile private var lastFrame: ByteArray? = null
  private val drawLock = Any()

  private fun redraw() {
    val f = lastFrame ?: return
    thread(name = "rc-redraw") { draw(f) }
  }

  private fun draw(frame: ByteArray) {
    synchronized(drawLock) { drawLocked(frame) }
  }

  private fun drawLocked(frame: ByteArray) {
    if (!surfaceReady) return
    val holder = surfaceView.holder
    val surface = holder.surface
    if (surface == null || !surface.isValid) return
    val bmp = BitmapFactory.decodeByteArray(frame, 0, frame.size) ?: return
    val canvas = holder.lockCanvas() ?: run { bmp.recycle(); return }
    try {
      // Fit the whole picture without stretching it; black bars fill the rest.
      val dst = zoomRect(fitRect(bmp.width, bmp.height, canvas.width, canvas.height), canvas.width, canvas.height)
      drawRect = dst
      canvas.drawColor(Color.BLACK)
      canvas.drawBitmap(bmp, null, dst, paint)
    } catch (e: Exception) {
      TSLog.e(TAG, "draw: ${e.message}")
    } finally {
      holder.unlockCanvasAndPost(canvas)
      bmp.recycle()
    }
  }

  // Where the picture is drawn on the surface, for mapping touches.
  @Volatile private var drawRect: Rect? = null
  private val paint = Paint(Paint.FILTER_BITMAP_FLAG)

  private fun fitRect(w: Int, h: Int, cw: Int, ch: Int): Rect {
    if (w <= 0 || h <= 0) return Rect(0, 0, cw, ch)
    val scale = minOf(cw.toFloat() / w, ch.toFloat() / h)
    val dw = (w * scale).toInt()
    val dh = (h * scale).toInt()
    val left = (cw - dw) / 2
    val top = (ch - dh) / 2
    return Rect(left, top, left + dw, top + dh)
  }

  // --- zoom (phones and tablets) ---
  // zoom 1 = whole screen fits; offX/offY move the enlarged picture around.
  @Volatile private var zoom = 1f
  @Volatile private var offX = 0f
  @Volatile private var offY = 0f
  private var lastTouchX = -1f
  private var lastTouchY = -1f
  private lateinit var scaleDetector: ScaleGestureDetector
  private var gesture = false // two fingers down: zoom/pan, nothing sent to the other device
  private var panX = 0f
  private var panY = 0f

  private fun zoomRect(fit: Rect, cw: Int, ch: Int): Rect {
    if (zoom <= 1.001f) { offX = 0f; offY = 0f; return fit }
    val w = fit.width() * zoom
    val h = fit.height() * zoom
    // Keep the picture over the screen: no empty space on a side it could fill.
    val minX = minOf(0f, cw - w); val maxX = maxOf(0f, cw - w)
    val minY = minOf(0f, ch - h); val maxY = maxOf(0f, ch - h)
    val cx = (cw - w) / 2 + offX
    val cy = (ch - h) / 2 + offY
    val left = if (w > cw) cx.coerceIn(minX, maxX) else (cw - w) / 2
    val top = if (h > ch) cy.coerceIn(minY, maxY) else (ch - h) / 2
    offX = left - (cw - w) / 2
    offY = top - (ch - h) / 2
    return Rect(left.toInt(), top.toInt(), (left + w).toInt(), (top + h).toInt())
  }

  /** Zoom by [factor], keeping the point under (fx, fy) on the screen where it is. */
  private fun zoomBy(factor: Float, fx: Float, fy: Float) {
    val old = zoom
    val nz = (zoom * factor).coerceIn(1f, 6f)
    if (nz == old) return
    val r = drawRect
    if (r != null && r.width() > 0) {
      // Where (fx, fy) sits in the picture now, as a fraction; put it back there after.
      val px = (fx - r.left) / r.width()
      val py = (fy - r.top) / r.height()
      val baseW = r.width() / old
      val baseH = r.height() / old
      val cw = surfaceView.width.toFloat()
      val ch = surfaceView.height.toFloat()
      val newLeft = fx - px * baseW * nz
      val newTop = fy - py * baseH * nz
      offX = newLeft - (cw - baseW * nz) / 2
      offY = newTop - (ch - baseH * nz) / 2
    }
    zoom = nz
    redraw()
  }

  private fun zoomHint() =
      if (packageManager.hasSystemFeature(android.content.pm.PackageManager.FEATURE_TOUCHSCREEN))
          "\nPinch or + to zoom in · two fingers move around" else ""

  private fun zoomButtons(): LinearLayout {
    val box = LinearLayout(this)
    box.orientation = LinearLayout.VERTICAL
    val pad = (12 * resources.displayMetrics.density).toInt()
    box.setPadding(pad, pad, pad, pad)
    fun add(label: String, desc: String, onTap: () -> Unit) {
      val b = Button(this)
      b.text = label
      b.contentDescription = desc
      b.textSize = 22f
      b.alpha = 0.8f
      b.isFocusable = false // the TV remote / keyboard keeps going to the other device
      b.setOnClickListener { onTap() }
      val size = (56 * resources.displayMetrics.density).toInt()
      box.addView(b, LinearLayout.LayoutParams(size, size).apply { topMargin = pad / 2 })
    }
    // Zoom in toward the last place you tapped, so a missed box ends up big under your finger.
    fun focus() = if (lastTouchX >= 0) Pair(lastTouchX, lastTouchY)
        else Pair(surfaceView.width / 2f, surfaceView.height / 2f)
    add("+", "Zoom in") { val (x, y) = focus(); zoomBy(1.6f, x, y) }
    add("−", "Zoom out") { val (x, y) = focus(); zoomBy(1 / 1.6f, x, y) }
    add("⤢", "Fit the whole screen") { zoom = 1f; offX = 0f; offY = 0f; redraw() }
    return box
  }

  override fun notice(text: String) {
    runOnUiThread { Toast.makeText(this, text, Toast.LENGTH_LONG).show() }
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
    // Back asks whether to leave, or to send Back (Esc on a PC) to the other device.
    if (keyCode == KeyEvent.KEYCODE_BACK) {
      if ((event?.repeatCount ?: 0) == 0) showLeaveDialog()
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

  private fun showLeaveDialog() {
    AlertDialog.Builder(this)
        .setTitle("Remote session")
        .setItems(arrayOf("Leave the session", "Send Back to the other device", "Keep controlling")) { _, which ->
          when (which) {
            0 -> finish()
            1 -> app.sendRemoteControlInput(RC.NAV, 0, 0, RC.NAV_BACK, true, 0, "")
          }
        }
        .show()
  }

  // Leaving the screen (Home, another app) ends the session, so it never
  // keeps running unseen in the background.
  override fun onStop() {
    super.onStop()
    if (!isChangingConfigurations && !isFinishing) finish()
  }

  override fun onKeyUp(keyCode: Int, event: KeyEvent?): Boolean {
    if (keyCode == KeyEvent.KEYCODE_BACK) return true
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
    scaleDetector.onTouchEvent(event)
    when (event.actionMasked) {
      MotionEvent.ACTION_POINTER_DOWN -> {
        gesture = true
        panX = (event.getX(0) + event.getX(1)) / 2
        panY = (event.getY(0) + event.getY(1)) / 2
        return true
      }
      MotionEvent.ACTION_MOVE -> if (gesture && event.pointerCount >= 2) {
        val mx = (event.getX(0) + event.getX(1)) / 2
        val my = (event.getY(0) + event.getY(1)) / 2
        offX += mx - panX
        offY += my - panY
        panX = mx
        panY = my
        redraw()
        return true
      }
    }
    if (gesture) {
      // Two-finger zoom/pan: nothing goes to the other device until all fingers lift.
      if (event.actionMasked == MotionEvent.ACTION_UP || event.actionMasked == MotionEvent.ACTION_CANCEL) gesture = false
      return true
    }
    lastTouchX = event.x
    lastTouchY = event.y
    val r = drawRect ?: Rect(0, 0, surfaceView.width.coerceAtLeast(1), surfaceView.height.coerceAtLeast(1))
    val fx = ((event.x - r.left) / r.width().coerceAtLeast(1)).coerceIn(0f, 1f)
    val fy = ((event.y - r.top) / r.height().coerceAtLeast(1)).coerceIn(0f, 1f)
    val hx = (fx * hostW).toInt()
    val hy = (fy * hostH).toInt()
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
