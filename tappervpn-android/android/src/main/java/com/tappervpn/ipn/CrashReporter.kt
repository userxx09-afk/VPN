// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause
package com.tappervpn.ipn

import android.content.Context
import android.os.Build
import java.io.File
import java.io.PrintWriter
import java.io.StringWriter
import java.net.HttpURLConnection
import java.net.URL
import java.util.Date
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import org.json.JSONObject

/**
 * Crash reports (TapperVPN).
 *
 * A crash is saved to a file as the app dies, then sent to the admin page on tapper
 * (BuildConfig.ADMIN_URL + /api/crash) the next time the app starts, where it shows in Events.
 * Sending retries for a few minutes, since the admin page is only reachable once the VPN is up;
 * the report stays on the device until it gets through.
 */
object CrashReporter {
  private const val FILE = "last_crash.txt"
  private const val TRIES = 12
  private const val RETRY_MS = 30_000L
  private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)

  /** Saves any uncaught exception before the app dies. Call first thing in Application.onCreate. */
  fun install(context: Context) {
    val dir = context.applicationContext.filesDir
    val previous = Thread.getDefaultUncaughtExceptionHandler()
    Thread.setDefaultUncaughtExceptionHandler { thread, e ->
      try {
        val trace = StringWriter().also { e.printStackTrace(PrintWriter(it)) }.toString()
        val text =
            "time: ${Date()}\n" +
                "version: ${BuildConfig.VERSION_NAME} (${BuildConfig.VERSION_CODE})\n" +
                "device: ${Build.MANUFACTURER} ${Build.MODEL}, Android ${Build.VERSION.RELEASE} " +
                "(SDK ${Build.VERSION.SDK_INT})\n" +
                "thread: ${thread.name}\n" +
                trace
        File(dir, FILE).writeText(text.take(60_000))
      } catch (_: Throwable) {
        // Never let reporting get in the way of the crash itself.
      }
      previous?.uncaughtException(thread, e)
    }
  }

  /** Sends a saved crash report, if there is one, then deletes it. */
  fun sendPending(context: Context) {
    val f = File(context.applicationContext.filesDir, FILE)
    if (!f.exists()) return
    scope.launch {
      repeat(TRIES) {
        val text =
            try {
              f.readText()
            } catch (_: Exception) {
              return@launch
            }
        if (post(text)) {
          f.delete()
          return@launch
        }
        delay(RETRY_MS)
      }
    }
  }

  private fun post(report: String): Boolean =
      try {
        val url = URL(BuildConfig.ADMIN_URL.trimEnd('/') + "/api/crash")
        val c = url.openConnection() as HttpURLConnection
        c.connectTimeout = 5_000
        c.readTimeout = 5_000
        c.requestMethod = "POST"
        c.doOutput = true
        c.setRequestProperty("Content-Type", "application/json")
        c.setRequestProperty("X-VPN-Admin", "1")
        c.outputStream.use { it.write(JSONObject().put("report", report).toString().toByteArray()) }
        val ok = c.responseCode in 200..299
        c.disconnect()
        ok
      } catch (_: Exception) {
        false
      }
}
