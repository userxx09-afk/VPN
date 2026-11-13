// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

package com.tappervpn.ipn.remotecontrol

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.Service
import android.content.Context
import android.content.Intent
import android.graphics.Bitmap
import android.graphics.PixelFormat
import android.hardware.display.DisplayManager
import android.hardware.display.VirtualDisplay
import android.media.ImageReader
import android.media.projection.MediaProjection
import android.media.projection.MediaProjectionManager
import android.os.Build
import android.os.Handler
import android.os.HandlerThread
import android.os.IBinder
import android.util.DisplayMetrics
import android.view.WindowManager
import com.tappervpn.ipn.R
import com.tappervpn.ipn.util.TSLog
import java.io.ByteArrayOutputStream

/**
 * Foreground service that captures the screen with MediaProjection and emits a
 * JPEG per frame (the universal MJPEG codec) to the Go core via
 * RemoteControlManager. It runs only between an accepted session's StartCapture
 * and StopCapture.
 *
 * MJPEG keeps the codec path dependency-free and interoperable with the Windows
 * host/controller, at the cost of more bandwidth than H.264; frames are
 * throttled and the long edge is capped to stay light.
 */
class RemoteControlHostService : Service() {
  private val TAG = "RemoteControlHost"

  private var projection: MediaProjection? = null
  private var virtualDisplay: VirtualDisplay? = null
  private var imageReader: ImageReader? = null
  private var captureThread: HandlerThread? = null
  @Volatile private var lastFrameMs = 0L

  override fun onBind(intent: Intent?): IBinder? = null

  override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
    when (intent?.action) {
      ACTION_STOP -> {
        stopEverything()
        stopSelf()
        return START_NOT_STICKY
      }
      ACTION_UPDATE -> {
        startForegroundNotice(intent.getStringExtra(EXTRA_PEER) ?: "a device")
        return START_STICKY
      }
    }

    val resultCode = intent?.getIntExtra(EXTRA_CODE, 0) ?: 0
    val data = intent?.let { IntentCompat.getProjectionData(it) }
    if (resultCode == 0 || data == null) {
      TSLog.e(TAG, "missing projection token")
      stopSelf()
      return START_NOT_STICKY
    }

    startForegroundNotice("a device")
    try {
      startCapture(resultCode, data)
    } catch (e: Exception) {
      TSLog.e(TAG, "capture start failed: ${e.message}")
      stopEverything()
      stopSelf()
    }
    return START_STICKY
  }

  private fun startCapture(resultCode: Int, data: Intent) {
    val mpm = getSystemService(Context.MEDIA_PROJECTION_SERVICE) as MediaProjectionManager
    val proj = mpm.getMediaProjection(resultCode, data) ?: throw IllegalStateException("null projection")
    projection = proj

    val (width, height, dpi) = displayInfo()
    val (encW, encH) = scaleToMax(width, height, 1280)

    val thread = HandlerThread("rc-capture").also { it.start() }
    captureThread = thread
    val handler = Handler(thread.looper)

    val reader = ImageReader.newInstance(encW, encH, PixelFormat.RGBA_8888, 2)
    imageReader = reader
    reader.setOnImageAvailableListener({ r -> onImage(r) }, handler)

    virtualDisplay =
        proj.createVirtualDisplay(
            "tappervpn-remote",
            encW,
            encH,
            dpi,
            DisplayManager.VIRTUAL_DISPLAY_FLAG_AUTO_MIRROR,
            reader.surface,
            null,
            handler,
        )
    TSLog.d(TAG, "capture started ${encW}x${encH}")
  }

  private fun onImage(reader: ImageReader) {
    val image = reader.acquireLatestImage() ?: return
    try {
      // Throttle to keep bandwidth/CPU modest (~12 fps).
      val now = System.currentTimeMillis()
      if (now - lastFrameMs < 80) return
      lastFrameMs = now

      val plane = image.planes[0]
      val width = image.width
      val height = image.height
      val rowStride = plane.rowStride
      val pixelStride = plane.pixelStride
      val rowPadding = rowStride - pixelStride * width

      val bmp =
          Bitmap.createBitmap(
              width + rowPadding / pixelStride, height, Bitmap.Config.ARGB_8888)
      bmp.copyPixelsFromBuffer(plane.buffer)
      val cropped =
          if (rowPadding == 0) bmp else Bitmap.createBitmap(bmp, 0, 0, width, height)

      val out = ByteArrayOutputStream()
      cropped.compress(Bitmap.CompressFormat.JPEG, 50, out)
      RemoteControlManager.pushFrame(out.toByteArray())

      if (cropped !== bmp) cropped.recycle()
      bmp.recycle()
    } catch (e: Exception) {
      TSLog.e(TAG, "frame encode: ${e.message}")
    } finally {
      image.close()
    }
  }

  private fun stopEverything() {
    runCatching { virtualDisplay?.release() }
    virtualDisplay = null
    runCatching { imageReader?.close() }
    imageReader = null
    runCatching { projection?.stop() }
    projection = null
    runCatching { captureThread?.quitSafely() }
    captureThread = null
  }

  override fun onDestroy() {
    stopEverything()
    super.onDestroy()
  }

  private fun startForegroundNotice(peer: String) {
    val nm = getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
      nm.createNotificationChannel(
          NotificationChannel(CHANNEL, "Remote control", NotificationManager.IMPORTANCE_LOW))
    }
    val n: Notification =
        Notification.Builder(this, CHANNEL)
            .setContentTitle("Screen is being shared")
            .setContentText("“$peer” is controlling this device")
            .setSmallIcon(R.drawable.ic_notification)
            .setOngoing(true)
            .build()
    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
      startForeground(NOTIF_ID, n, android.content.pm.ServiceInfo.FOREGROUND_SERVICE_TYPE_MEDIA_PROJECTION)
    } else {
      startForeground(NOTIF_ID, n)
    }
  }

  private fun displayInfo(): Triple<Int, Int, Int> {
    val wm = getSystemService(Context.WINDOW_SERVICE) as WindowManager
    val m = DisplayMetrics()
    @Suppress("DEPRECATION") wm.defaultDisplay.getRealMetrics(m)
    return Triple(m.widthPixels, m.heightPixels, m.densityDpi)
  }

  private fun scaleToMax(w: Int, h: Int, maxEdge: Int): Pair<Int, Int> {
    val longEdge = maxOf(w, h)
    if (longEdge <= maxEdge) return evenPair(w, h)
    val scale = maxEdge.toDouble() / longEdge
    return evenPair((w * scale).toInt(), (h * scale).toInt())
  }

  private fun evenPair(w: Int, h: Int) = Pair(w and 1.inv(), h and 1.inv())

  companion object {
    private const val CHANNEL = "remotecontrol"
    private const val NOTIF_ID = 0x5243 // "RC"
    private const val ACTION_STOP = "com.tappervpn.ipn.RC_STOP"
    private const val ACTION_UPDATE = "com.tappervpn.ipn.RC_UPDATE"
    private const val EXTRA_CODE = "code"
    private const val EXTRA_DATA = "data"
    private const val EXTRA_PEER = "peer"

    fun start(context: Context, resultCode: Int, data: Intent) {
      val i =
          Intent(context, RemoteControlHostService::class.java).apply {
            putExtra(EXTRA_CODE, resultCode)
            putExtra(EXTRA_DATA, data)
          }
      context.startForegroundService(i)
    }

    fun stop(context: Context) {
      context.startService(
          Intent(context, RemoteControlHostService::class.java).setAction(ACTION_STOP))
    }

    fun updateControlledBy(context: Context, peer: String) {
      context.startService(
          Intent(context, RemoteControlHostService::class.java)
              .setAction(ACTION_UPDATE)
              .putExtra(EXTRA_PEER, peer))
    }

    const val PROJECTION_DATA_KEY = EXTRA_DATA
  }
}

/** Small shim for reading the parcelable projection Intent across API levels. */
private object IntentCompat {
  fun getProjectionData(intent: Intent): Intent? {
    return if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
      intent.getParcelableExtra(RemoteControlHostService.PROJECTION_DATA_KEY, Intent::class.java)
    } else {
      @Suppress("DEPRECATION") intent.getParcelableExtra(RemoteControlHostService.PROJECTION_DATA_KEY)
    }
  }
}
