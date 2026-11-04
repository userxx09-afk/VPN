// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

//go:build !ts_omit_tailnetlock

// Package tslockjsonv1 provides types for unmarshaling the JSON output of the
// "tappervpn lock --json=1" command:
//
//   - [LogResponse] will unmarshal the output of "tappervpn lock log --json=1"
//   - [StatusResponse] will unmarshal the output of "tappervpn lock status --json=1".
package tslockjsonv1
