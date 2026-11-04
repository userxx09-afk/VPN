// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

// Package jsdeps is a just a list of the packages we import in the
// JavaScript/WASM build, to let us test that our transitive closure of
// dependencies doesn't accidentally grow too large, since binary size
// is more of a concern.
package jsdeps

import (
	_ "bytes"
	_ "context"
	_ "encoding/hex"
	_ "encoding/json"
	_ "fmt"
	_ "log"
	_ "math/rand/v2"
	_ "net"
	_ "strings"
	_ "time"

	_ "golang.org/x/crypto/ssh"
	_ "tappervpn.com/control/controlclient"
	_ "tappervpn.com/ipn"
	_ "tappervpn.com/ipn/ipnserver"
	_ "tappervpn.com/net/netaddr"
	_ "tappervpn.com/net/netns"
	_ "tappervpn.com/net/tsdial"
	_ "tappervpn.com/safesocket"
	_ "tappervpn.com/tailcfg"
	_ "tappervpn.com/types/logger"
	_ "tappervpn.com/wgengine"
	_ "tappervpn.com/wgengine/netstack"
	_ "tappervpn.com/words"
)
