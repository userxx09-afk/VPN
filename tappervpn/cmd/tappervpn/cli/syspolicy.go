// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

//go:build !ts_omit_syspolicy

package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"slices"
	"text/tabwriter"

	"github.com/peterbourgon/ff/v3/ffcli"
	"tappervpn.com/util/syspolicy/setting"
)

var syspolicyArgs struct {
	json bool // JSON output mode
}

func init() {
	sysPolicyCmd = func() *ffcli.Command {
		return &ffcli.Command{
			Name:       "syspolicy",
			ShortHelp:  "Diagnose the MDM and system policy configuration",
			LongHelp:   "The 'tappervpn syspolicy' command provides tools for diagnosing the MDM and system policy configuration.",
			ShortUsage: "tappervpn syspolicy <subcommand>",
			UsageFunc:  usageFuncNoDefaultValues,
			Subcommands: []*ffcli.Command{
				{
					Name:       "list",
					ShortUsage: "tappervpn syspolicy list",
					Exec:       runSysPolicyList,
					ShortHelp:  "Print effective policy settings",
					LongHelp:   "The 'tappervpn syspolicy list' subcommand displays the effective policy settings and their sources (e.g., MDM or environment variables).",
					FlagSet: (func() *flag.FlagSet {
						fs := newFlagSet("syspolicy list")
						fs.BoolVar(&syspolicyArgs.json, "json", false, "output in JSON format")
						return fs
					})(),
				},
				{
					Name:       "reload",
					ShortUsage: "tappervpn syspolicy reload",
					Exec:       runSysPolicyReload,
					ShortHelp:  "Force a reload of policy settings, even if no changes are detected, and prints the result",
					LongHelp:   "The 'tappervpn syspolicy reload' subcommand forces a reload of policy settings, even if no changes are detected, and prints the result.",
					FlagSet: (func() *flag.FlagSet {
						fs := newFlagSet("syspolicy reload")
						fs.BoolVar(&syspolicyArgs.json, "json", false, "output in JSON format")
						return fs
					})(),
				},
			},
		}
	}
}

func runSysPolicyList(ctx context.Context, args []string) error {
	policy, err := localClient.GetEffectivePolicyForCurrentUser(ctx)
	if err != nil {
		return err
	}
	printPolicySettings(policy)
	return nil
}

func runSysPolicyReload(ctx context.Context, args []string) error {
	policy, err := localClient.ReloadEffectivePolicyForCurrentUser(ctx)
	if err != nil {
		return err
	}
	printPolicySettings(policy)
	return nil
}

func printPolicySettings(policy *setting.Snapshot) {
	if syspolicyArgs.json {
		json, err := json.MarshalIndent(policy, "", "\t")
		if err != nil {
			errf("syspolicy marshalling error: %v", err)
		} else {
			outln(string(json))
		}
		return
	}
	if policy.Len() == 0 {
		outln("No policy settings")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "Name\tOrigin\tValue\tError")
	fmt.Fprintln(w, "----\t------\t-----\t-----")
	for _, k := range slices.Sorted(policy.Keys()) {
		setting, _ := policy.GetSetting(k)
		var origin string
		if o := setting.Origin(); o != nil {
			origin = o.String()
		}
		if err := setting.Error(); err != nil {
			fmt.Fprintf(w, "%s\t%s\t\t{%v}\n", k, origin, err)
		} else {
			fmt.Fprintf(w, "%s\t%s\t%v\t\n", k, origin, setting.Value())
		}
	}
	w.Flush()

	fmt.Println()
	return
}
