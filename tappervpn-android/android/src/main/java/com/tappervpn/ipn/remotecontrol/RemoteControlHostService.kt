// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

package com.tappervpn.ipn.remotecontrol

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.hardware.display.DisplayManager
import android.hardware.display.VirtualDisplay
import android.media.MediaCodec
import android.media.MediaCodecInfo
import android.media.MediaFormat
import android.media.projection.MediaProjection
import android.media.projection.MediaProjectionManager
import android.os.Build
import android.os.IBinder
import android.util.DisplayMetrics
import android.view.Surface
import android.view.WindowManager
import com.tappervpn.ipn.R
import com.tappervpn.ipn.util.TSLog
import java.nio.ByteBuffer

/**
 * Foreground service that captures the screen with MediaProjection, encodes it
 * to H.264 with the hardware MediaCodec encoder, and pushes each encoded frame
 * (Annex-B, codec config first) to the Go core via RemoteControlManager. It runs
 * only between an accepted session's StartCapture and StopCapture.
 */
class RemoteControlHostService : Service() {
  private val TAG = "RemoteControlHost"

  private var projection: MediaProjection? = null
  private var virtualDisplay: VirtualDisplay? = null
  private var encoder: MediaCodec? = null
  private var inputSurface: Surface? = null
  @Volatile private var running = false
  private var encoderThread: Thread? = null

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
    // Cap the long edge to keep the stream light on bandwidth and CPU.
    val (encW, encH) = scaleToMax(width, height, 1280)

    val format =
        MediaFormat.createVideoFormat(MediaFormat.MIMETYPE_VIDEO_AVC, encW, encH).apply {
          setInteger(
              MediaFormat.KEY_COLOR_FORMAT,
              MediaCodecInfo.CodecCapabilities.COLOR_FormatSurface)
          setInteger(MediaFormat.KEY_BIT_RATE, 4_000_000)
          setInteger(MediaFormat.KEY_FRAME_RATE, 30)
          setInteger(MediaFormat.KEY_I_FRAME_INTERVAL, 2)
          if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.M) {
            setInteger(
                MediaFormat.KEY_BITRATE_MODE,
                MediaCodecInfo.EncoderCapabilities.BITRATE_MODE_VBR)
          }
        }

    val codec = MediaCodec.createEncoderByType(MediaFormat.MIMETYPE_VIDEO_AVC)
    codec.configure(format, null, null, MediaCodec.CONFIGURE_FLAG_ENCODE)
    inputSurface = codec.createInputSurface()
    codec.start()
    encoder = codec

    virtualDisplay =
        proj.createVirtualDisplay(
            "tappervpn-remote",
            encW,
            encH,
            dpi,
            DisplayManager.VIRTUAL_DISPLAY_FLAG_AUTO_MIRROR,
            inputSurface,
            null,
            null)

    running = true
    encoderThread = Thread({ drainEncoder() }, "rc-encoder").also { it.start() }
    TSLog.d(TAG, "capture started ${encW}x${encH}")
  }

  private fun drainEncoder() {
    val codec = encoder ?: return
    val bufInfo = MediaCodec.BufferInfo()
    while (running) {
      val outIndex = codec.dequeueOutputBuffer(bufInfo, 10_000)
      if (outIndex < 0) continue
      val buf: ByteBuffer? = codec.getOutputBuffer(outIndex)
      if (buf != null && bufInfo.size > 0) {
        buf.position(bufInfo.offset)
        buf.limit(bufInfo.offset + bufInfo.size)
        val bytes = ByteArray(bufInfo.size)
        buf.get(bytes)
        // Codec config (SPS/PPS) and every frame are Annex-B; the controller's
        // decoder is fed the same bytes verbatim.
        RemoteControlManager.pushFrame(bytes)
      }
      codec.releaseOutputBuffer(outIndex, false)
      if (bufInfo.flags and MediaCodec.BUFFER_FLAG_END_OF_STREAM != 0) break
    }
  }

  private fun stopEverything() {
    running = false
    encoderThread?.join(500)
    encoderThread = null
    runCatching { virtualDisplay?.release() }
    virtualDisplay = null
    runCatching {
      encoder?.stop()
      encoder?.release()
    }
    encoder = null
    runCatching { inputSurface?.release() }
    inputSurface = null
    runCatching { projection?.stop() }
    projection = null
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
      startForeground(NOTIF_ID, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_MEDIA_PROJECTION)
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

  // H.264 requires even dimensions.
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

    /** Keeps the EXTRA_DATA key name in one place for IntentCompat. */
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
