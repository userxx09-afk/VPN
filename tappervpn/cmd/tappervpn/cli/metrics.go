// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/peterbourgon/ff/v3/ffcli"
	"tappervpn.com/atomicfile"
)

var metricsCmd = &ffcli.Command{
	Name:      "metrics",
	ShortHelp: "Show TapperVPN metrics",
	LongHelp: strings.TrimSpace(`

The 'tappervpn metrics' command shows TapperVPN user-facing metrics (as opposed
to internal metrics printed by 'tappervpn debug metrics').

For more information about TapperVPN metrics, refer to
https://tailscale.com/s/client-metrics

`),
	ShortUsage: "tappervpn metrics <subcommand> [flags]",
	UsageFunc:  usageFuncNoDefaultValues,
	Exec:       runMetricsNoSubcommand,
	Subcommands: []*ffcli.Command{
		{
			Name:       "print",
			ShortUsage: "tappervpn metrics print",
			Exec:       runMetricsPrint,
			ShortHelp:  "Print current metric values in Prometheus text format",
		},
		{
			Name:       "write",
			ShortUsage: "tappervpn metrics write <path>",
			Exec:       runMetricsWrite,
			ShortHelp:  "Write metric values to a file",
			LongHelp: strings.TrimSpace(`

The 'tappervpn metrics write' command writes metric values to a text file provided as its
only argument. It's meant to be used alongside Prometheus node exporter, allowing TapperVPN
metrics to be consumed and exported by the textfile collector.

As an example, to export TapperVPN metrics on an Ubuntu system running node exporter, you
can regularly run 'tappervpn metrics write /var/lib/prometheus/node-exporter/tappervpnd.prom'
using cron or a systemd timer.

	`),
		},
	},
}

// runMetricsNoSubcommand prints metric values if no subcommand is specified.
func runMetricsNoSubcommand(ctx context.Context, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("tappervpn metrics: unknown subcommand: %s", args[0])
	}

	return runMetricsPrint(ctx, args)
}

// runMetricsPrint prints metric values to stdout.
func runMetricsPrint(ctx context.Context, args []string) error {
	out, err := localClient.UserMetrics(ctx)
	if err != nil {
		return err
	}
	Stdout.Write(out)
	return nil
}

// runMetricsWrite writes metric values to a file.
func runMetricsWrite(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: tappervpn metrics write <path>")
	}
	path := args[0]
	out, err := localClient.UserMetrics(ctx)
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(path, out, 0644)
}
