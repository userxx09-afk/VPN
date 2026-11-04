// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/peterbourgon/ff/v3/ffcli"
)

var whoamiCmd = &ffcli.Command{
	Name:       "whoami",
	ShortUsage: "tappervpn whoami [--json]",
	ShortHelp:  "Show the machine and user identity of the current machine",
	LongHelp: strings.TrimSpace(`
	'tappervpn whoami' shows the machine and user identity of the current machine.
	It is equivalent to running 'tappervpn whois' against one of the current machine's own TapperVPN IP addresses.
	`),
	Exec: runWhoami,
	FlagSet: func() *flag.FlagSet {
		fs := newFlagSet("whoami")
		fs.BoolVar(&whoamiArgs.json, "json", false, "output in JSON format")
		return fs
	}(),
}

var whoamiArgs struct {
	json bool // output in JSON format
}

func runWhoami(ctx context.Context, args []string) error {
	if len(args) > 0 {
		return errors.New("too many arguments, expected none")
	}
	st, err := localClient.StatusWithoutPeers(ctx)
	if err != nil {
		return err
	}
	if len(st.TapperVPNIPs) == 0 {
		return fmt.Errorf("no current TapperVPN IP address; state: %v", st.BackendState)
	}
	who, err := localClient.WhoIsProto(ctx, "", st.TapperVPNIPs[0].String())
	if err != nil {
		return err
	}
	return printWhoIs(who, st.CurrentTailnet, whoamiArgs.json)
}
