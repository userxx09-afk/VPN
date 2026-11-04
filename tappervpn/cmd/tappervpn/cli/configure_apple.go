// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

//go:build darwin

package cli

import (
	"context"
	"errors"

	"github.com/peterbourgon/ff/v3/ffcli"
)

func init() {
	maybeSysExtCmd = sysExtCmd
	maybeVPNConfigCmd = vpnConfigCmd
}

// Functions in this file provide a dummy Exec function that only prints an error message for users of the open-source
// tappervpnd distribution. On GUI builds, the Swift code in the macOS client handles these commands by not passing the
// flow of execution to the CLI.

// sysExtCmd returns a command for managing the TapperVPN system extension on macOS
// (for the Standalone variant of the client only).
func sysExtCmd() *ffcli.Command {
	return &ffcli.Command{
		Name:       "sysext",
		ShortUsage: "tappervpn configure sysext [activate|deactivate|status]",
		ShortHelp:  "Manage the system extension for macOS (Standalone variant)",
		LongHelp: "The sysext set of commands provides a way to activate, deactivate, or manage the state of the TapperVPN system extension on macOS. " +
			"This is only relevant if you are running the Standalone variant of the TapperVPN client for macOS. " +
			"To access more detailed information about system extensions installed on this Mac, run 'systemextensionsctl list'.",
		Subcommands: []*ffcli.Command{
			{
				Name:       "activate",
				ShortUsage: "tappervpn configure sysext activate",
				ShortHelp:  "Register the TapperVPN system extension with macOS.",
				LongHelp:   "This command registers the TapperVPN system extension with macOS. To run TapperVPN, you'll also need to install the VPN configuration separately (run `tappervpn configure vpn-config install`). After running this command, you need to approve the extension in System Settings > Login Items and Extensions > Network Extensions.",
				Exec:       requiresStandalone,
			},
			{
				Name:       "deactivate",
				ShortUsage: "tappervpn configure sysext deactivate",
				ShortHelp:  "Deactivate the TapperVPN system extension on macOS",
				LongHelp:   "This command deactivates the TapperVPN system extension on macOS. To completely remove TapperVPN, you'll also need to delete the VPN configuration separately (use `tappervpn configure vpn-config uninstall`).",
				Exec:       requiresStandalone,
			},
			{
				Name:       "status",
				ShortUsage: "tappervpn configure sysext status",
				ShortHelp:  "Print the enablement status of the TapperVPN system extension",
				LongHelp:   "This command prints the enablement status of the TapperVPN system extension. If the extension is not enabled, run `tappervpn sysext activate` to enable it.",
				Exec:       requiresStandalone,
			},
		},
		Exec: requiresStandalone,
	}
}

// vpnConfigCmd returns a command for managing the TapperVPN VPN configuration on macOS
// (the entry that appears in System Settings > VPN).
func vpnConfigCmd() *ffcli.Command {
	return &ffcli.Command{
		Name:       "mac-vpn",
		ShortUsage: "tappervpn configure mac-vpn [install|uninstall]",
		ShortHelp:  "Manage the VPN configuration on macOS (App Store and Standalone variants)",
		LongHelp:   "The vpn-config set of commands provides a way to add or remove the TapperVPN VPN configuration from the macOS settings. This is the entry that appears in System Settings > VPN.",
		Subcommands: []*ffcli.Command{
			{
				Name:       "install",
				ShortUsage: "tappervpn configure mac-vpn install",
				ShortHelp:  "Write the TapperVPN VPN configuration to the macOS settings",
				LongHelp:   "This command writes the TapperVPN VPN configuration to the macOS settings. This is the entry that appears in System Settings > VPN. If you are running the Standalone variant of the client, you'll also need to install the system extension separately (run `tappervpn configure sysext activate`).",
				Exec:       requiresGUI,
			},
			{
				Name:       "uninstall",
				ShortUsage: "tappervpn configure mac-vpn uninstall",
				ShortHelp:  "Delete the TapperVPN VPN configuration from the macOS settings",
				LongHelp:   "This command removes the TapperVPN VPN configuration from the macOS settings. This is the entry that appears in System Settings > VPN. If you are running the Standalone variant of the client, you'll also need to deactivate the system extension separately (run `tappervpn configure sysext deactivate`).",
				Exec:       requiresGUI,
			},
		},
		Exec: func(ctx context.Context, args []string) error {
			return errors.New("unsupported command: requires a GUI build of the macOS client")
		},
	}
}

func requiresStandalone(ctx context.Context, args []string) error {
	return errors.New("unsupported command: requires the Standalone (.pkg installer) GUI build of the client")
}

func requiresGUI(ctx context.Context, args []string) error {
	return errors.New("unsupported command: requires a GUI build of the macOS client")
}
