// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause
package com.tappervpn.ipn

import android.app.AppOpsManager
import android.app.usage.UsageEvents
import android.app.usage.UsageStatsManager
import android.content.Context
import android.os.Process
import com.tappervpn.ipn.ui.localapi.Client
import com.tappervpn.ipn.ui.model.Ipn
import com.tappervpn.ipn.ui.notifier.Notifier
import com.tappervpn.ipn.util.TSLog
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import org.json.JSONObject

/**
 * Per-app exit nodes (TapperVPN).
 *
 * Each assigned app is routed through the VPN, and whenever it comes to the foreground the exit
 * node is switched to the one assigned to it. Apps without an assignment bypass the VPN and use the
 * normal internet connection. On a TV only one app is in front at a time, so this gives each app
 * its own exit node; an assigned app left running in the background follows whichever assigned app
 * was opened last.
 *
 * Seeing the foreground app needs the "usage access" app-op, which Fire TV has no settings screen
 * for; grant it with `adb shell appops set com.tappervpn.ipn GET_USAGE_STATS allow`.
 */
object AppExitNodes {
  private const val TAG = "AppExitNodes"
  private const val PREFS_FILE = "appExitNodes"
  private const val PREFS_KEY = "assignments"
  private const val POLL_MS = 1000L

  /** The exit node assigned to an app. [nodeName] is only for display. */
  data class Assignment(val nodeId: String, val nodeName: String)

  private val _assignments = MutableStateFlow<Map<String, Assignment>>(emptyMap())
  /** Package name to assignment. Empty means the feature is off. */
  val assignments: StateFlow<Map<String, Assignment>> = _assignments

  private var loaded = false
  private var watcher: Job? = null
  @Volatile private var lastForeground: String? = null

  private fun prefs(context: Context) =
      context.getSharedPreferences(PREFS_FILE, Context.MODE_PRIVATE)

  /** Returns the current assignments, loading them from storage on first use. */
  @Synchronized
  fun get(context: Context): Map<String, Assignment> {
    if (!loaded) {
      _assignments.value = decode(prefs(context).getString(PREFS_KEY, null))
      loaded = true
    }
    return _assignments.value
  }

  /**
   * Assigns [assignment] to [packageName], or removes its assignment when null. The VPN is
   * restarted so its app list matches.
   */
  @Synchronized
  fun set(context: Context, packageName: String, assignment: Assignment?) {
    val updated = get(context).toMutableMap()
    if (assignment == null) updated.remove(packageName) else updated[packageName] = assignment
    _assignments.value = updated
    prefs(context).edit().putString(PREFS_KEY, encode(updated)).apply()
    lastForeground = null // re-evaluate the foreground app against the new list
    UninitializedApp.get().restartVPN()
  }

  /** Reports whether this app may read foreground-app events. */
  fun hasUsageAccess(context: Context): Boolean {
    val ops = context.getSystemService(Context.APP_OPS_SERVICE) as AppOpsManager
    @Suppress("DEPRECATION")
    val mode =
        ops.checkOpNoThrow(
            AppOpsManager.OPSTR_GET_USAGE_STATS,
            Process.myUid(),
            context.packageName,
        )
    return mode == AppOpsManager.MODE_ALLOWED
  }

  /** Starts watching the foreground app; called while the VPN service runs. Idempotent. */
  @Synchronized
  fun startWatching(context: Context, scope: CoroutineScope) {
    if (watcher?.isActive == true) return
    val appContext = context.applicationContext
    watcher =
        scope.launch(Dispatchers.Default) {
          val usm = appContext.getSystemService(Context.USAGE_STATS_SERVICE) as UsageStatsManager
          var since = System.currentTimeMillis() - 10_000
          var warned = false
          while (isActive) {
            if (get(appContext).isNotEmpty()) {
              if (!hasUsageAccess(appContext)) {
                if (!warned) {
                  TSLog.w(TAG, "no usage access; per-app exit nodes can't follow the foreground app")
                  warned = true
                }
              } else {
                warned = false
                val now = System.currentTimeMillis()
                latestForeground(usm, since, now)?.let { onForeground(appContext, it) }
                since = now
              }
            }
            delay(POLL_MS)
          }
        }
  }

  @Synchronized
  fun stopWatching() {
    watcher?.cancel()
    watcher = null
    lastForeground = null
  }

  private fun latestForeground(usm: UsageStatsManager, from: Long, to: Long): String? {
    val events = usm.queryEvents(from, to) ?: return null
    val e = UsageEvents.Event()
    var latest: String? = null
    while (events.hasNextEvent()) {
      events.getNextEvent(e)
      @Suppress("DEPRECATION")
      if (e.eventType == UsageEvents.Event.MOVE_TO_FOREGROUND) latest = e.packageName
    }
    return latest
  }

  private fun onForeground(context: Context, pkg: String) {
    if (pkg == lastForeground) return
    lastForeground = pkg
    // Unassigned apps bypass the VPN, so the exit node doesn't matter to them; leaving it alone
    // avoids needless switches when passing through the launcher.
    val assignment = get(context)[pkg] ?: return
    val current = Notifier.prefs.value?.activeExitNodeID
    if (current == assignment.nodeId) return
    TSLog.d(TAG, "$pkg in foreground; switching exit node to ${assignment.nodeName}")
    val prefsOut = Ipn.MaskedPrefs()
    prefsOut.ExitNodeID = assignment.nodeId
    Client(CoroutineScope(Dispatchers.IO)).editPrefs(prefsOut) { result ->
      result.exceptionOrNull()?.let { TSLog.e(TAG, "switching exit node for $pkg: $it") }
    }
  }

  private fun encode(m: Map<String, Assignment>): String {
    val o = JSONObject()
    for ((pkg, a) in m) {
      o.put(pkg, JSONObject().put("id", a.nodeId).put("name", a.nodeName))
    }
    return o.toString()
  }

  private fun decode(s: String?): Map<String, Assignment> {
    if (s.isNullOrEmpty()) return emptyMap()
    return try {
      val o = JSONObject(s)
      o.keys().asSequence().associateWith { pkg ->
        val a = o.getJSONObject(pkg)
        Assignment(a.getString("id"), a.optString("name", a.getString("id")))
      }
    } catch (e: Exception) {
      TSLog.e(TAG, "ignoring unreadable assignments: $e")
      emptyMap()
    }
  }
}
