// Copyright (c) TapperVPN contributors
// SPDX-License-Identifier: BSD-3-Clause
package com.tappervpn.ipn

import android.app.Activity
import android.app.AlertDialog
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Build
import android.provider.Settings
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.FileProvider
import androidx.work.Constraints
import androidx.work.CoroutineWorker
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.NetworkType
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import com.tappervpn.ipn.util.TSLog
import java.io.File
import java.net.HttpURLConnection
import java.net.URL
import java.security.MessageDigest
import java.util.concurrent.TimeUnit
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import libtappervpn.Libtappervpn
import org.json.JSONObject

/**
 * Keeps the app up to date from GitHub Releases (github.com/userxx09-afk/VPN).
 *
 * Once a day [UpdateCheckWorker] looks at the latest release. If it is newer than
 * [BuildConfig.TAPPERVPN_VERSION], it downloads SHA256SUMS and its signature, checks the
 * signature in Go (the same code every TapperVPN platform uses), downloads the APK and checks
 * its hash. Then it notifies the user, and [MainActivity] offers to install it. Android always
 * asks the user to confirm installing an app from outside an app store, and only accepts the
 * update if it is signed with the same key as the installed app.
 */
object AppUpdater {
  private const val TAG = "AppUpdater"
  private const val WORK_NAME = "app-update-check"
  private const val REPO = "userxx09-afk/VPN"
  const val APK_ASSET = "tappervpn-android.apk"
  private const val PREFS = "app-updater"
  private const val PREF_READY_VERSION = "ready-version"
  private const val PREF_DECLINED_VERSION = "declined-version"
  private const val PREF_LAST_CHECK = "last-check-ms"
  // Opening the app checks again once the last check is this old.
  private val CHECK_ON_OPEN_AFTER_MS = TimeUnit.HOURS.toMillis(6)
  private val checking = Mutex()
  const val CHANNEL_ID = "tappervpn-updates"
  const val NOTIFICATION_ID = 41
  const val EXTRA_INSTALL_UPDATE = "com.tappervpn.ipn.INSTALL_UPDATE"

  fun schedule(context: Context) {
    val request =
        PeriodicWorkRequestBuilder<UpdateCheckWorker>(1, TimeUnit.DAYS)
            .setConstraints(
                Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).build())
            .build()
    WorkManager.getInstance(context)
        .enqueueUniquePeriodicWork(WORK_NAME, ExistingPeriodicWorkPolicy.KEEP, request)
  }

  private fun updateDir(context: Context) = File(context.cacheDir, "updates")

  private fun apkFile(context: Context) = File(updateDir(context), APK_ASSET)

  /** Checks GitHub and downloads a verified newer APK. Returns its version, or null. */
  suspend fun checkAndDownload(context: Context): String? =
      withContext(Dispatchers.IO) {
        val release = JSONObject(fetch("https://api.github.com/repos/$REPO/releases/latest").decodeToString())
        val version = release.getString("tag_name").removePrefix("v")
        prefs(context).edit().putLong(PREF_LAST_CHECK, System.currentTimeMillis()).apply()
        if (readyVersion(context) == version) {
          return@withContext version // already downloaded and verified
        }
        if (!isNewer(version, BuildConfig.TAPPERVPN_VERSION)) {
          TSLog.d(TAG, "up to date (installed ${BuildConfig.TAPPERVPN_VERSION}, latest $version)")
          return@withContext null
        }
        val assets = release.getJSONArray("assets")
        val urls = HashMap<String, String>()
        for (i in 0 until assets.length()) {
          val a = assets.getJSONObject(i)
          urls[a.getString("name")] = a.getString("browser_download_url")
        }
        val sums = fetch(urls["SHA256SUMS"] ?: error("release $version has no SHA256SUMS"))
        val sig = fetch(urls["SHA256SUMS.sig"] ?: error("release $version is not signed"))
        // Throws unless the signature is from the TapperVPN release key.
        val wantHash = Libtappervpn.verifyUpdate(sums, sig, APK_ASSET)

        val dir = updateDir(context).apply { mkdirs() }
        dir.listFiles()?.forEach { it.delete() }
        val apk = apkFile(context)
        download(urls[APK_ASSET] ?: error("release $version has no $APK_ASSET"), apk)
        val gotHash = sha256(apk)
        if (gotHash != wantHash) {
          apk.delete()
          error("$APK_ASSET: SHA-256 mismatch")
        }
        prefs(context).edit().putString(PREF_READY_VERSION, version).apply()
        TSLog.d(TAG, "update $version downloaded and verified")
        version
      }

  /** The version of a downloaded, verified update waiting to be installed, if any. */
  fun readyVersion(context: Context): String? {
    val v = prefs(context).getString(PREF_READY_VERSION, null) ?: return null
    if (!isNewer(v, BuildConfig.TAPPERVPN_VERSION) || !apkFile(context).exists()) {
      prefs(context).edit().remove(PREF_READY_VERSION).apply()
      return null
    }
    return v
  }

  /** Whether opening the app should check for an update (the daily check can lag on idle TVs). */
  fun shouldCheckOnOpen(context: Context): Boolean =
      readyVersion(context) == null &&
          System.currentTimeMillis() - prefs(context).getLong(PREF_LAST_CHECK, 0) >
              CHECK_ON_OPEN_AFTER_MS

  /** Result of [checkNow]. */
  sealed class CheckResult {
    data class UpToDate(val version: String) : CheckResult()

    data class Ready(val version: String) : CheckResult()

    data class Failed(val message: String) : CheckResult()
  }

  /**
   * "Check for updates": checks GitHub now and downloads a newer version. When one is ready, the
   * caller offers to install it with [promptIfReady].
   */
  suspend fun checkNow(context: Context): CheckResult =
      try {
        // One check at a time (opening the app and the button can overlap).
        val v = checking.withLock { checkAndDownload(context) }
        if (v != null) CheckResult.Ready(v) else CheckResult.UpToDate(BuildConfig.TAPPERVPN_VERSION)
      } catch (e: Exception) {
        TSLog.e(TAG, "update check failed: $e")
        CheckResult.Failed(e.message ?: e.toString())
      }

  fun notifyReady(context: Context, version: String) {
    val intent =
        Intent(context, MainActivity::class.java).apply {
          putExtra(EXTRA_INSTALL_UPDATE, true)
          flags = Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TOP
        }
    val pending =
        PendingIntent.getActivity(
            context, 0, intent, PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE)
    val notification =
        NotificationCompat.Builder(context, CHANNEL_ID)
            .setSmallIcon(R.drawable.ic_notification)
            .setContentTitle(context.getString(R.string.update_ready_title))
            .setContentText(context.getString(R.string.update_ready_text, version))
            .setContentIntent(pending)
            .setAutoCancel(true)
            .build()
    try {
      NotificationManagerCompat.from(context).notify(NOTIFICATION_ID, notification)
    } catch (e: SecurityException) {
      TSLog.d(TAG, "can't post update notification: $e") // notifications not allowed
    }
  }

  /**
   * Offers to install a downloaded update. Shown when the app opens, so it also works on TVs,
   * where notifications are easy to miss. [fromNotification] skips the "not now" memory.
   */
  fun promptIfReady(activity: Activity, fromNotification: Boolean) {
    val version = readyVersion(activity) ?: return
    if (!fromNotification && prefs(activity).getString(PREF_DECLINED_VERSION, null) == version) {
      return
    }
    AlertDialog.Builder(activity)
        .setTitle(R.string.update_ready_title)
        .setMessage(activity.getString(R.string.update_ready_text, version))
        .setPositiveButton(R.string.update_install) { _, _ -> install(activity) }
        .setNegativeButton(R.string.update_not_now) { _, _ ->
          prefs(activity).edit().putString(PREF_DECLINED_VERSION, version).apply()
        }
        .show()
  }

  private fun install(activity: Activity) {
    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O &&
        !activity.packageManager.canRequestPackageInstalls()) {
      // One-time: let TapperVPN install its own updates. The prompt shows again on return.
      prefs(activity).edit().remove(PREF_DECLINED_VERSION).apply()
      activity.startActivity(
          Intent(
              Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES,
              Uri.parse("package:${activity.packageName}")))
      return
    }
    val uri =
        FileProvider.getUriForFile(
            activity, "${activity.packageName}.updates", apkFile(activity))
    activity.startActivity(
        Intent(Intent.ACTION_VIEW).apply {
          setDataAndType(uri, "application/vnd.android.package-archive")
          addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_ACTIVITY_NEW_TASK)
        })
  }

  private fun prefs(context: Context) = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)

  /** Compares dotted versions numerically: 0.10.0 is newer than 0.9.1. */
  fun isNewer(version: String, current: String): Boolean {
    fun parts(v: String) = v.substringBefore('-').split('.').map { it.toIntOrNull() ?: 0 }
    val a = parts(version)
    val b = parts(current)
    for (i in 0 until maxOf(a.size, b.size)) {
      val x = a.getOrElse(i) { 0 }
      val y = b.getOrElse(i) { 0 }
      if (x != y) return x > y
    }
    return current.contains('-') && !version.contains('-') // 0.2.0 is newer than 0.2.0-dev
  }

  private fun open(url: String): HttpURLConnection =
      (URL(url).openConnection() as HttpURLConnection).apply {
        connectTimeout = 30_000
        readTimeout = 60_000
        setRequestProperty("User-Agent", "TapperVPN-Android/${BuildConfig.TAPPERVPN_VERSION}")
        if (responseCode != 200) error("GET $url: HTTP $responseCode")
      }

  private fun fetch(url: String): ByteArray = open(url).inputStream.use { it.readBytes() }

  private fun download(url: String, dest: File) {
    open(url).inputStream.use { input -> dest.outputStream().use { input.copyTo(it) } }
  }

  private fun sha256(file: File): String {
    val md = MessageDigest.getInstance("SHA-256")
    file.inputStream().use { input ->
      val buf = ByteArray(64 * 1024)
      while (true) {
        val n = input.read(buf)
        if (n < 0) break
        md.update(buf, 0, n)
      }
    }
    return md.digest().joinToString("") { "%02x".format(it) }
  }
}

class UpdateCheckWorker(appContext: Context, params: WorkerParameters) :
    CoroutineWorker(appContext, params) {
  override suspend fun doWork(): Result {
    val r = AppUpdater.checkNow(applicationContext)
    if (r is AppUpdater.CheckResult.Ready) AppUpdater.notifyReady(applicationContext, r.version)
    return Result.success() // on failure, try again at the next daily run, not in a retry loop
  }
}
