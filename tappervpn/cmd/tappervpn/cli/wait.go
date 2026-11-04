// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package cli

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/peterbourgon/ff/v3/ffcli"
	"tappervpn.com/ipn"
	"tappervpn.com/types/logger"
	"tappervpn.com/util/backoff"
)

var waitCmd = &ffcli.Command{
	Name:      "wait",
	ShortHelp: "Wait for TapperVPN interface/IPs to be ready for binding",
	LongHelp: strings.TrimSpace(`
Wait for TapperVPN resources to be available. As of 2026-01-02, the only
resource that's available to wait for by is the TapperVPN interface and its
IPs.

With no arguments, this command will block until tappervpnd is up, its backend is running,
and the TapperVPN interface is up and has a TapperVPN IP address assigned to it.

If running in userspace-networking mode, this command only waits for tappervpnd and
the Running state, as no physical network interface exists.

A future version of this command may support waiting for other types of resources.

The command returns exit code 0 on success, and non-zero on failure or timeout.

To wait on a specific type of IP address, use 'tappervpn ip' in combination with
the 'tappervpn wait' command. For example, to wait for an IPv4 address:

    tappervpn wait && tappervpn ip --assert=<specific-IP-address>

Linux systemd users can wait for the "tappervpn-online.target" target, which runs
this command.

More generally, a service that wants to bind to (listen on) a TapperVPN interface or IP address
can run it like 'tappervpn wait && /path/to/service [...]' to ensure that TapperVPN is ready
before the program starts.
`),

	ShortUsage: "tappervpn wait",
	Exec:       runWait,
	FlagSet: (func() *flag.FlagSet {
		fs := newFlagSet("wait")
		fs.DurationVar(&waitArgs.timeout, "timeout", 0, "how long to wait before giving up (0 means wait indefinitely)")
		return fs
	})(),
}

var waitArgs struct {
	timeout time.Duration
}

func runWait(ctx context.Context, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("unexpected arguments: %q", args)
	}
	if waitArgs.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, waitArgs.timeout)
		defer cancel()
	}

	bo := backoff.NewBackoff("wait", logger.Discard, 2*time.Second)
	for {
		_, err := localClient.StatusWithoutPeers(ctx)
		bo.BackOff(ctx, err)
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}

	watcher, err := localClient.WatchIPNBus(ctx, ipn.NotifyInitialState)
	if err != nil {
		return err
	}
	defer watcher.Close()
	var firstIP netip.Addr
	for {
		not, err := watcher.Next()
		if err != nil {
			return err
		}
		if not.State != nil && *not.State == ipn.Running {

			st, err := localClient.StatusWithoutPeers(ctx)
			if err != nil {
				return err
			}
			if len(st.TapperVPNIPs) > 0 {
				firstIP = st.TapperVPNIPs[0]
				break
			}
		}
	}

	st, err := localClient.StatusWithoutPeers(ctx)
	if err != nil {
		return err
	}
	if !st.TUN {
		// No TUN; nothing more to wait for.
		return nil
	}

	// Verify we have an interface using that IP.
	for {
		err := checkForInterfaceIP(firstIP)
		if err == nil {
			return nil
		}
		bo.BackOff(ctx, err)
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
}

func checkForInterfaceIP(ip netip.Addr) error {
	ifs, err := net.Interfaces()
	if err != nil {
		return err
	}
	for _, ifi := range ifs {
		addrs, err := ifi.Addrs()
		if err != nil {
			return err
		}
		for _, addr := range addrs {
			var aip netip.Addr
			switch v := addr.(type) {
			case *net.IPNet:
				aip, _ = netip.AddrFromSlice(v.IP)
			case *net.IPAddr:
				aip, _ = netip.AddrFromSlice(v.IP)
			}
			if aip.Unmap() == ip {
				return nil
			}
		}
	}
	return fmt.Errorf("no interface has IP %v", ip)
}
