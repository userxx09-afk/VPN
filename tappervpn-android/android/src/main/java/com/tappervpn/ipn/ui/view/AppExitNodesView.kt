// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

package com.tappervpn.ipn.ui.view

import androidx.compose.foundation.Image
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.RadioButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.core.graphics.drawable.toBitmap
import androidx.lifecycle.viewmodel.compose.viewModel
import com.tappervpn.ipn.R
import com.tappervpn.ipn.ui.util.InstalledApp
import com.tappervpn.ipn.ui.util.Lists
import com.tappervpn.ipn.ui.viewModel.AppExitNodeChoice
import com.tappervpn.ipn.ui.viewModel.AppExitNodesViewModel

/** Lists installed apps and lets the user pick an exit node (or normal internet) for each. */
@Composable
fun AppExitNodesView(
    backToSettings: BackNavigation,
    model: AppExitNodesViewModel = viewModel(),
) {
  val installedApps by model.installedApps.collectAsState()
  val assignments by model.assignments.collectAsState()
  val exitNodes by model.exitNodes.collectAsState()
  val hasUsageAccess by model.hasUsageAccess.collectAsState()
  val pickingFor by model.pickingFor.collectAsState()
  val iconSize = 40.dp
  val iconSizePx = with(LocalDensity.current) { iconSize.roundToPx() }

  val needsUsageAccess by model.needsUsageAccess.collectAsState()
  // Notice when the user comes back from Settings with usage access turned on.
  LaunchedEffect(Unit) {
    while (true) {
      model.refreshUsageAccess()
      kotlinx.coroutines.delay(2000)
    }
  }

  pickingFor?.let { app ->
    ExitNodeChoiceDialog(
        app = app,
        exitNodes = exitNodes,
        selectedId = assignments[app.packageName]?.nodeId,
        onChoose = { model.assign(app.packageName, it) },
        onDismiss = { model.pick(null) },
    )
  }

  // Apps with an exit node first, then the rest; both alphabetical.
  val sortedApps =
      remember(installedApps, assignments) {
        installedApps.sortedBy { if (assignments.containsKey(it.packageName)) 0 else 1 }
      }

  Scaffold(topBar = { Header(titleRes = R.string.app_exit_nodes, onBack = backToSettings) }) {
      innerPadding ->
    LazyColumn(modifier = Modifier.padding(innerPadding)) {
      item("explainer") {
        ListItem(headlineContent = { Text(stringResource(R.string.app_exit_nodes_explainer)) })
      }
      if (needsUsageAccess && !hasUsageAccess) {
        item("usageAccess") {
          ListItem(
              modifier = Modifier.clickable { model.openUsageAccessSettings() },
              headlineContent = {
                Text(
                    stringResource(R.string.app_exit_nodes_no_usage_access),
                    color = MaterialTheme.colorScheme.error,
                )
              }
          )
        }
      }
      if (exitNodes.isEmpty()) {
        item("noExitNodes") {
          ListItem(
              headlineContent = { Text(stringResource(R.string.app_exit_nodes_no_exit_nodes)) }
          )
        }
      }
      item("count") {
        Lists.SectionDivider(stringResource(R.string.app_exit_nodes_count, assignments.size))
      }
      items(sortedApps, key = { it.packageName }) { app ->
        val icon =
            remember(app.packageName, iconSizePx) {
              model.installedAppsManager.packageManager
                  .getApplicationIcon(app.packageName)
                  .toBitmap(width = iconSizePx, height = iconSizePx)
                  .asImageBitmap()
            }
        val assigned = assignments[app.packageName]
        ListItem(
            modifier = Modifier.clickable { model.pick(app) },
            headlineContent = { Text(app.name, fontWeight = FontWeight.SemiBold) },
            leadingContent = {
              Image(bitmap = icon, contentDescription = null, modifier = Modifier.size(iconSize))
            },
            supportingContent = {
              Text(
                  assigned?.nodeName ?: stringResource(R.string.app_exit_nodes_normal),
                  color =
                      if (assigned != null) MaterialTheme.colorScheme.primary
                      else MaterialTheme.colorScheme.secondary,
              )
            },
        )
        Lists.ItemDivider()
      }
    }
  }
}

@Composable
private fun ExitNodeChoiceDialog(
    app: InstalledApp,
    exitNodes: List<AppExitNodeChoice>,
    selectedId: String?,
    onChoose: (AppExitNodeChoice?) -> Unit,
    onDismiss: () -> Unit,
) {
  AlertDialog(
      onDismissRequest = onDismiss,
      title = { Text(app.name) },
      text = {
        Column(modifier = Modifier.verticalScroll(rememberScrollState())) {
          ChoiceRow(
              label = stringResource(R.string.app_exit_nodes_normal),
              selected = selectedId == null,
              onClick = { onChoose(null) },
          )
          for (node in exitNodes) {
            ChoiceRow(
                label =
                    if (node.online) node.name
                    else stringResource(R.string.app_exit_nodes_offline, node.name),
                selected = node.id == selectedId,
                onClick = { onChoose(node) },
            )
          }
        }
      },
      confirmButton = {
        TextButton(onClick = onDismiss) { Text(stringResource(android.R.string.cancel)) }
      },
  )
}

@Composable
private fun ChoiceRow(label: String, selected: Boolean, onClick: () -> Unit) {
  ListItem(
      modifier = Modifier.clickable(onClick = onClick),
      leadingContent = { RadioButton(selected = selected, onClick = null) },
      headlineContent = { Text(label) },
  )
}
