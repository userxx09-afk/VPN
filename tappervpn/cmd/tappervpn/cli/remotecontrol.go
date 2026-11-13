// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"text/tabwriter"

	"github.com/peterbourgon/ff/v3/ffcli"
)

var remoteControlCmd = &ffcli.Command{
	Name:       "remotecontrol",
	ShortUsage: "tappervpn remotecontrol <subcommand>",
	ShortHelp:  "View and control other devices on your tailnet",
	LongHelp: strings.TrimSpace(`
The 'tappervpn remotecontrol' command lets this device control another device on
your tailnet: 'targets' lists devices you can ask, and 'connect' asks one to
accept a screen-sharing and input session. The other device must accept the
request on its screen before anything is shared.
`),
	Exec: func(ctx context.Context, args []string) error {
		return fmt.Errorf("tappervpn remotecontrol: use a subcommand: targets, connect")
	},
	Subcommands: []*ffcli.Command{
		{
			Name:       "targets",
			ShortUsage: "tappervpn remotecontrol targets",
			ShortHelp:  "List devices you can ask to control",
			Exec:       runRCTargets,
		},
		{
			Name:       "connect",
			ShortUsage: "tappervpn remotecontrol connect <device>",
			ShortHelp:  "Ask a device (by name or ID) to accept a control session",
			Exec:       runRCConnect,
		},
	},
}

type rcTarget struct {
	StableID string `json:"stableID"`
	Name     string `json:"name"`
	OS       string `json:"os"`
}

func rcGetTargets(ctx context.Context) ([]rcTarget, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"http://local-tappervpnd.sock/localapi/v0/remotecontrol/targets", nil)
	if err != nil {
		return nil, err
	}
	resp, err := localClient.DoLocalRequest(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var targets []rcTarget
	if err := json.Unmarshal(body, &targets); err != nil {
		return nil, err
	}
	return targets, nil
}

func runRCTargets(ctx context.Context, args []string) error {
	targets, err := rcGetTargets(ctx)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		fmt.Fprintln(Stdout, "No controllable devices found.")
		return nil
	}
	tw := tabwriter.NewWriter(Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tOS\tID")
	for _, t := range targets {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", t.Name, t.OS, t.StableID)
	}
	return tw.Flush()
}

func runRCConnect(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: tappervpn remotecontrol connect <device>")
	}
	// Accept a StableID directly, or match a device by name/ID from the list.
	id := args[0]
	name := args[0]
	if targets, err := rcGetTargets(ctx); err == nil {
		var matches []rcTarget
		for _, t := range targets {
			if t.StableID == args[0] || strings.EqualFold(t.Name, args[0]) {
				matches = append(matches, t)
			}
		}
		switch len(matches) {
		case 1:
			id, name = matches[0].StableID, matches[0].Name
		case 0:
			// fall through with the raw argument as the ID
		default:
			return fmt.Errorf("%q matches %d devices; use the exact device ID", args[0], len(matches))
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://local-tappervpnd.sock/localapi/v0/remotecontrol/connect?id="+url.QueryEscape(id), nil)
	if err != nil {
		return err
	}
	resp, err := localClient.DoLocalRequest(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	fmt.Fprintf(Stdout, "Asked %s to accept the session. Watch that device's screen to allow it.\n", name)
	return nil
}
