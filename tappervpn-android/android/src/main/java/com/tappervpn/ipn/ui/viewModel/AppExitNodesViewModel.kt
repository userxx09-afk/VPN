// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

package com.tappervpn.ipn.ui.viewModel

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.tappervpn.ipn.App
import com.tappervpn.ipn.AppExitNodes
import com.tappervpn.ipn.ui.notifier.Notifier
import com.tappervpn.ipn.ui.util.InstalledApp
import com.tappervpn.ipn.ui.util.InstalledAppsManager
import com.tappervpn.ipn.ui.util.set
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.flow.flowOn
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn

/** An exit node that apps can be assigned to. */
data class AppExitNodeChoice(val id: String, val name: String, val online: Boolean)

class AppExitNodesViewModel : ViewModel() {
  val installedAppsManager = InstalledAppsManager(packageManager = App.get().packageManager)

  val installedApps: StateFlow<List<InstalledApp>> =
      flow { emit(installedAppsManager.fetchInstalledApps()) }
          .flowOn(Dispatchers.IO)
          .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5000), listOf())

  val assignments: StateFlow<Map<String, AppExitNodes.Assignment>> = AppExitNodes.assignments

  val exitNodes: StateFlow<List<AppExitNodeChoice>> =
      Notifier.netmap
          .map { nm ->
            (nm?.Peers ?: emptyList())
                .filter { it.isExitNode && !it.isMullvadNode }
                .map { AppExitNodeChoice(it.StableID, it.displayName, it.Online == true) }
                .sortedBy { it.name.lowercase() }
          }
          .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5000), listOf())

  val hasUsageAccess: StateFlow<Boolean> = MutableStateFlow(true)

  /** The app whose exit node is being picked, or null when the picker is closed. */
  val pickingFor: StateFlow<InstalledApp?> = MutableStateFlow(null)

  init {
    AppExitNodes.get(App.get())
    refreshUsageAccess()
  }

  /** The assigned apps use more than one exit node, so usage access is needed. */
  val needsUsageAccess: StateFlow<Boolean> = MutableStateFlow(false)

  fun refreshUsageAccess() {
    hasUsageAccess.set(AppExitNodes.hasUsageAccess(App.get()))
    needsUsageAccess.set(AppExitNodes.needsUsageAccess(App.get()))
  }

  /** Opens the settings screen for usage access. Returns false if the device has none (Fire TV). */
  fun openUsageAccessSettings(): Boolean {
    val intent = AppExitNodes.usageAccessIntent(App.get()) ?: return false
    return try {
      App.get().startActivity(intent)
      true
    } catch (e: Exception) {
      false
    }
  }

  fun pick(app: InstalledApp?) {
    pickingFor.set(app)
  }

  /** Assigns [node] to [packageName]; null means normal internet. */
  fun assign(packageName: String, node: AppExitNodeChoice?) {
    AppExitNodes.set(
        App.get(),
        packageName,
        node?.let { AppExitNodes.Assignment(it.id, it.name) },
    )
    pickingFor.set(null)
    val wasNeeded = needsUsageAccess.value
    refreshUsageAccess()
    // The first time apps need different exit nodes, take the user straight to the switch.
    if (!wasNeeded && needsUsageAccess.value && !hasUsageAccess.value) openUsageAccessSettings()
  }
}
