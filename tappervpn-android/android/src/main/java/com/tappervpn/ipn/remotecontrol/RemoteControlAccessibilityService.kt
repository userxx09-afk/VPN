// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

package com.tappervpn.ipn.remotecontrol

import android.accessibilityservice.AccessibilityService
import android.accessibilityservice.GestureDescription
import android.graphics.Path
import android.os.Build
import android.os.Bundle
import android.view.View
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
      RC.NAV_UP -> moveFocus(View.FOCUS_UP, 0f, -SWIPE)
      RC.NAV_DOWN -> moveFocus(View.FOCUS_DOWN, 0f, SWIPE)
      RC.NAV_LEFT -> moveFocus(View.FOCUS_LEFT, -SWIPE, 0f)
      RC.NAV_RIGHT -> moveFocus(View.FOCUS_RIGHT, SWIPE, 0f)
      RC.NAV_PLAY_PAUSE -> clickFocusedOrCenter()
    }
  }

  /**
   * D-pad navigation. TV apps move a focus highlight rather than scroll, and
   * key injection needs system privileges, so: on Android 13+ use the real
   * D-pad global actions; otherwise move input focus to the next view in that
   * direction (what a D-pad press does); swipe only if nothing is focusable.
   */
  private fun moveFocus(direction: Int, dx: Float, dy: Float) {
    if (Build.VERSION.SDK_INT >= 33) {
      val action =
          when (direction) {
            View.FOCUS_UP -> GLOBAL_ACTION_DPAD_UP
            View.FOCUS_DOWN -> GLOBAL_ACTION_DPAD_DOWN
            View.FOCUS_LEFT -> GLOBAL_ACTION_DPAD_LEFT
            else -> GLOBAL_ACTION_DPAD_RIGHT
          }
      if (performGlobalAction(action)) return
    }
    val root = rootInActiveWindow
    val focused = root?.findFocus(AccessibilityNodeInfo.FOCUS_INPUT)
    val next = focused?.focusSearch(direction)
    if (next != null && next.performAction(AccessibilityNodeInfo.ACTION_FOCUS)) return
    if (focused == null && root != null) {
      // Nothing focused yet: focus the first focusable view, like a first D-pad press.
      firstFocusable(root)?.let { if (it.performAction(AccessibilityNodeInfo.ACTION_FOCUS)) return }
    }
    swipeFromCenter(dx, dy)
  }

  private fun firstFocusable(node: AccessibilityNodeInfo): AccessibilityNodeInfo? {
    if (node.isFocusable && node.isVisibleToUser) return node
    for (i in 0 until node.childCount) {
      val child = node.getChild(i) ?: continue
      firstFocusable(child)?.let { return it }
    }
    return null
  }

  private fun clickFocusedOrCenter() {
    if (Build.VERSION.SDK_INT >= 33 && performGlobalAction(GLOBAL_ACTION_DPAD_CENTER)) return
    var node = rootInActiveWindow?.findFocus(AccessibilityNodeInfo.FOCUS_INPUT)
        ?: rootInActiveWindow?.findFocus(AccessibilityNodeInfo.FOCUS_ACCESSIBILITY)
    // The focused view is often a child of the clickable item; walk up to it.
    while (node != null && !node.isClickable) node = node.parent
    if (node != null && node.performAction(AccessibilityNodeInfo.ACTION_CLICK)) return
    tap(centerX(), centerY())
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

    /** Whether the user has turned this service on (control works only then). */
    fun isEnabled(): Boolean = instance != null
    private const val SWIPE = 300f
    private const val SCROLL_STEP = 40f

    @Volatile var instance: RemoteControlAccessibilityService? = null

    private var lastX = 0f
    private var lastY = 0f
  }
}
