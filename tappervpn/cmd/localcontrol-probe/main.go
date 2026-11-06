// Copyright (c) TapperVPN contributors
// SPDX-License-Identifier: BSD-3-Clause

// The localcontrol-probe command shows which control server a client set up
// for the public name would use on this network.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"tappervpn.com/control/localcontrol"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	u := localcontrol.NewFinder(log.Printf, nil).Find(ctx)
	if u == "" {
		fmt.Printf("no local control server; would use %s (%v)\n", localcontrol.PublicURL, time.Since(start).Round(time.Millisecond))
		return
	}
	fmt.Printf("local control server: %s (%v)\n", u, time.Since(start).Round(time.Millisecond))
}
