// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

package com.tappervpn.ipn.remotecontrol

import android.accessibilityservice.AccessibilityService
import android.accessibilityservice.GestureDescription
import android.graphics.Path
import android.os.Bundle
import android.view.accessibility.AccessibilityEvent
import android.view.accessibility.AccessibilityNodeInfo
import com.tappervpn.ipn.util.TSLog

/**
 * Injects remote input into the device using the accessibility APIs — the only
 * mechanism available to an unprivileged, sideloaded app (no ADB, no root).
 *
 * What works: HOME / BACK / RECENTS, taps and swipes anywhere (dispatchGesture),
 * and typing into the focused text field. What cannot work from here: raw
 * D-pad / key events delivered into other apps (Android reserves key injection
 * for system/signature apps). Directional nav is therefore approximated with
 * short swipes, which drives most scrollable UIs but will not move the launcher
 * highlight in apps that only respond to real D-pad keys. This limitation is
 * surfaced to the user in the app.
 */
class RemoteControlAccessibilityService : AccessibilityService() {

  override fun onServiceConnected() {
    super.onServiceConnected()
    instance = this
    TSLog.d(TAG, "accessibility service connected")
  }

  override fun onUnbind(intent: android.content.Intent?): Boolean {
    if (instance === this) instance = null
    return super.onUnbind(intent)
  }

  override fun onDestroy() {
    if (instance === this) instance = null
    super.onDestroy()
  }

  override fun onAccessibilityEvent(event: AccessibilityEvent?) {}

  override fun onInterrupt() {}

  /** Applies one input event (fields follow feature/remotecontrol/protocol.go). */
  fun inject(kind: Int, x: Int, y: Int, button: Int, down: Boolean, code: Int, text: String) {
    when (kind) {
      RC.POINTER_MOVE -> {
        lastX = x.toFloat()
        lastY = y.toFloat()
      }
      RC.POINTER_BUTTON -> {
        // Treat a press-release as a tap at the last known pointer position.
        if (!down) tap(lastX, lastY)
      }
      RC.SCROLL -> {
        // Wheel delta: swipe opposite the scroll direction from the pointer.
        swipe(lastX, lastY, lastX - x * SCROLL_STEP, lastY - y * SCROLL_STEP)
      }
      RC.TEXT -> typeText(text)
      RC.NAV -> nav(button)
      RC.KEY -> TSLog.d(TAG, "ignoring raw key $code (not injectable without system privilege)")
    }
  }

  private fun nav(action: Int) {
    when (action) {
      RC.NAV_HOME -> performGlobalAction(GLOBAL_ACTION_HOME)
      RC.NAV_BACK -> performGlobalAction(GLOBAL_ACTION_BACK)
      RC.NAV_MENU -> performGlobalAction(GLOBAL_ACTION_RECENTS)
      RC.NAV_OK -> clickFocusedOrCenter()
      RC.NAV_UP -> swipeFromCenter(0f, -SWIPE)
      RC.NAV_DOWN -> swipeFromCenter(0f, SWIPE)
      RC.NAV_LEFT -> swipeFromCenter(-SWIPE, 0f)
      RC.NAV_RIGHT -> swipeFromCenter(SWIPE, 0f)
      RC.NAV_PLAY_PAUSE -> clickFocusedOrCenter()
    }
  }

  private fun clickFocusedOrCenter() {
    val focused = rootInActiveWindow?.findFocus(AccessibilityNodeInfo.FOCUS_INPUT)
        ?: rootInActiveWindow?.findFocus(AccessibilityNodeInfo.FOCUS_ACCESSIBILITY)
    if (focused != null && focused.isClickable) {
      focused.performAction(AccessibilityNodeInfo.ACTION_CLICK)
    } else {
      tap(centerX(), centerY())
    }
  }

  private fun typeText(text: String) {
    val node =
        rootInActiveWindow?.findFocus(AccessibilityNodeInfo.FOCUS_INPUT) ?: return
    val args =
        Bundle().apply {
          putCharSequence(AccessibilityNodeInfo.ACTION_ARGUMENT_SET_TEXT_CHARSEQUENCE, text)
        }
    node.performAction(AccessibilityNodeInfo.ACTION_SET_TEXT, args)
  }

  private fun tap(x: Float, y: Float) {
    val path = Path().apply { moveTo(x.coerceAtLeast(0f), y.coerceAtLeast(0f)) }
    val stroke = GestureDescription.StrokeDescription(path, 0, 40)
    dispatchGesture(GestureDescription.Builder().addStroke(stroke).build(), null, null)
  }

  private fun swipe(x1: Float, y1: Float, x2: Float, y2: Float) {
    val path =
        Path().apply {
          moveTo(x1.coerceAtLeast(0f), y1.coerceAtLeast(0f))
          lineTo(x2.coerceAtLeast(0f), y2.coerceAtLeast(0f))
        }
    val stroke = GestureDescription.StrokeDescription(path, 0, 120)
    dispatchGesture(GestureDescription.Builder().addStroke(stroke).build(), null, null)
  }

  private fun swipeFromCenter(dx: Float, dy: Float) =
      swipe(centerX(), centerY(), centerX() + dx, centerY() + dy)

  private fun centerX(): Float = resources.displayMetrics.widthPixels / 2f
  private fun centerY(): Float = resources.displayMetrics.heightPixels / 2f

  companion object {
    private const val TAG = "RemoteControlA11y"
    private const val SWIPE = 300f
    private const val SCROLL_STEP = 40f

    @Volatile var instance: RemoteControlAccessibilityService? = null

    private var lastX = 0f
    private var lastY = 0f
  }
}
