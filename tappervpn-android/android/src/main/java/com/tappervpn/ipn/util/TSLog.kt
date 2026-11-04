// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause
package com.tappervpn.ipn.util

import android.content.Context
import android.util.Log
import libtappervpn.Libtappervpn

object TSLog {
  private lateinit var appContext: Context
  var libtappervpnWrapper = LibtappervpnWrapper()

  fun init(context: Context) {
    appContext = context.applicationContext
  }

  fun d(tag: String?, message: String) {
    Log.d(tag, message)
    libtappervpnWrapper.sendLog(tag, message)
  }

  fun w(tag: String, message: String) {
    Log.w(tag, message)
    libtappervpnWrapper.sendLog(tag, message)
  }

  fun v(tag: String?, message: String) {
    if (isUnstableRelease()) {
      Log.v(tag, message)
      libtappervpnWrapper.sendLog(tag, message)
    }
  }

  // Overloaded function without Throwable because Java does not support default parameters
  @JvmStatic
  fun e(tag: String?, message: String) {
    Log.e(tag, message)
    libtappervpnWrapper.sendLog(tag, message)
  }

  fun e(tag: String?, message: String, throwable: Throwable? = null) {
    if (throwable == null) {
      Log.e(tag, message)
      libtappervpnWrapper.sendLog(tag, message)
    } else {
      Log.e(tag, message, throwable)
      libtappervpnWrapper.sendLog(tag, "$message ${throwable.localizedMessage}")
    }
  }

  private fun isUnstableRelease(): Boolean {
    val versionName =
        appContext.packageManager.getPackageInfo(appContext.packageName, 0).versionName

    // Extract the middle number and check if it's odd
    val middleNumber = versionName?.split(".")?.getOrNull(1)?.toIntOrNull()
    return middleNumber?.let { it % 2 == 1 } ?: false
  }

  class LibtappervpnWrapper {
    public fun sendLog(tag: String?, message: String) {
      val logTag = tag ?: ""
      Libtappervpn.sendLog((logTag + ": " + message).toByteArray(Charsets.UTF_8))
    }
  }
}
