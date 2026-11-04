// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

//go:build !ios && !ts_omit_tpm

package condregister

import _ "tappervpn.com/feature/tpm"
