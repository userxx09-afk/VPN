// Copyright (c) TapperVPN contributors
// SPDX-License-Identifier: BSD-3-Clause

package com.tappervpn.ipn.ui.view

import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Clear
import androidx.compose.material.icons.outlined.Search
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import com.tappervpn.ipn.R
import com.tappervpn.ipn.ui.theme.searchBarColors
import com.tappervpn.ipn.ui.util.InstalledApp

/** A search box for the app lists (split tunneling, per-app exit nodes). */
@Composable
fun AppSearchField(query: String, onQueryChange: (String) -> Unit) {
  OutlinedTextField(
      value = query,
      onValueChange = onQueryChange,
      modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 8.dp),
      singleLine = true,
      shape = MaterialTheme.shapes.extraLarge,
      colors = MaterialTheme.colorScheme.searchBarColors,
      placeholder = { Text(stringResource(R.string.search_ellipsis)) },
      leadingIcon = { Icon(Icons.Outlined.Search, contentDescription = null) },
      trailingIcon = {
        if (query.isNotEmpty()) {
          IconButton(onClick = { onQueryChange("") }) {
            Icon(Icons.Outlined.Clear, contentDescription = stringResource(R.string.clear_search))
          }
        }
      },
  )
}

/** The apps whose name or package name contains [query] (all of them when it's blank). */
fun List<InstalledApp>.matching(query: String): List<InstalledApp> {
  val q = query.trim()
  if (q.isEmpty()) return this
  return filter { it.name.contains(q, ignoreCase = true) || it.packageName.contains(q, ignoreCase = true) }
}
