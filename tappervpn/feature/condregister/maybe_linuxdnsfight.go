// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

//go:build linux && !android && !ts_omit_linuxdnsfight

package condregister

import _ "tappervpn.com/feature/linuxdnsfight"
