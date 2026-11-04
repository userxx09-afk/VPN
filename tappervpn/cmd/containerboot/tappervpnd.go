// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"tappervpn.com/client/local"
)

func startTapperVPNd(ctx context.Context, cfg *settings) (*local.Client, *os.Process, error) {
	args := tappervpndArgs(cfg)
	// tappervpnd runs without context, since it needs to persist
	// beyond the startup timeout in ctx.
	cmd := exec.Command("tappervpnd", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
	if cfg.CertShareMode != "" {
		cmd.Env = append(os.Environ(), "TS_CERT_SHARE_MODE="+cfg.CertShareMode)
	}
	log.Printf("Starting tappervpnd")
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("starting tappervpnd failed: %w", err)
	}

	// Wait for the socket file to appear, otherwise API ops will racily fail.
	log.Printf("Waiting for tappervpnd socket at %s", cfg.Socket)
	for {
		if ctx.Err() != nil {
			return nil, nil, errors.New("timed out waiting for tappervpnd socket")
		}
		_, err := os.Stat(cfg.Socket)
		if errors.Is(err, fs.ErrNotExist) {
			time.Sleep(100 * time.Millisecond)
			continue
		} else if err != nil {
			return nil, nil, fmt.Errorf("error waiting for tappervpnd socket: %w", err)
		}
		break
	}

	tsClient := &local.Client{
		Socket:        cfg.Socket,
		UseSocketOnly: true,
	}

	return tsClient, cmd.Process, nil
}

// tappervpndArgs uses cfg to construct the argv for tappervpnd.
func tappervpndArgs(cfg *settings) []string {
	args := []string{"--socket=" + cfg.Socket}
	switch {
	case cfg.KubeSecret != "":
		args = append(args, "--state=kube:"+cfg.KubeSecret)
		if cfg.StateDir == "" {
			cfg.StateDir = "/tmp"
		}
		fallthrough
	case cfg.StateDir != "":
		args = append(args, "--statedir="+cfg.StateDir)
	default:
		args = append(args, "--state=mem:", "--statedir=/tmp")
	}

	if cfg.UserspaceMode {
		args = append(args, "--tun=userspace-networking")
	} else if err := ensureTunFile(cfg.Root); err != nil {
		log.Fatalf("ensuring that /dev/net/tun exists: %v", err)
	}

	if cfg.SOCKSProxyAddr != "" {
		args = append(args, "--socks5-server="+cfg.SOCKSProxyAddr)
	}
	if cfg.HTTPProxyAddr != "" {
		args = append(args, "--outbound-http-proxy-listen="+cfg.HTTPProxyAddr)
	}
	if cfg.TapperVPNdConfigFilePath != "" {
		args = append(args, "--config="+cfg.TapperVPNdConfigFilePath)
	}
	// Once enough proxy versions have been released for all the supported
	// versions to understand this cfg setting, the operator can stop
	// setting TS_TAPPERVPND_EXTRA_ARGS for the debug flag.
	if cfg.DebugAddrPort != "" && !strings.Contains(cfg.DaemonExtraArgs, cfg.DebugAddrPort) {
		args = append(args, "--debug="+cfg.DebugAddrPort)
	}
	if cfg.DaemonExtraArgs != "" {
		args = append(args, strings.Fields(cfg.DaemonExtraArgs)...)
	}
	return args
}

// tappervpnUp uses cfg to run 'tappervpn up' everytime containerboot starts, or
// if TS_AUTH_ONCE is set, only the first time containerboot starts.
func tappervpnUp(ctx context.Context, cfg *settings) error {
	args := []string{"--socket=" + cfg.Socket, "up"}
	if cfg.AcceptDNS != nil && *cfg.AcceptDNS {
		args = append(args, "--accept-dns=true")
	} else {
		args = append(args, "--accept-dns=false")
	}
	if cfg.AuthKey != "" {
		args = append(args, "--authkey="+cfg.AuthKey)
	}
	if cfg.ClientID != "" {
		args = append(args, "--client-id="+cfg.ClientID)
	}
	if cfg.ClientSecret != "" {
		args = append(args, "--client-secret="+cfg.ClientSecret)
	}
	if cfg.IDToken != "" {
		args = append(args, "--id-token="+cfg.IDToken)
	}
	if cfg.Audience != "" {
		args = append(args, "--audience="+cfg.Audience)
	}
	// --advertise-routes can be passed an empty string to configure a
	// device (that might have previously advertised subnet routes) to not
	// advertise any routes. Respect an empty string passed by a user and
	// use it to explicitly unset the routes.
	if cfg.Routes != nil {
		args = append(args, "--advertise-routes="+*cfg.Routes)
	}
	if cfg.Hostname != "" {
		args = append(args, "--hostname="+cfg.Hostname)
	}
	if cfg.ExtraArgs != "" {
		args = append(args, strings.Fields(cfg.ExtraArgs)...)
	}
	log.Printf("Running 'tappervpn up'")
	cmd := exec.CommandContext(ctx, "tappervpn", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			// A canceled context kills the command, and cmd.Run can
			// report the subprocess's death ("signal: killed") rather
			// than the context error that caused it. Return the
			// context error so that callers (and ultimately main) can
			// recognize a graceful shutdown with errors.Is.
			return fmt.Errorf("tappervpn up failed: %w", ctxErr)
		}
		return fmt.Errorf("tappervpn up failed: %w", err)
	}
	return nil
}

// tappervpnSet uses cfg to run 'tappervpn set' to set any known configuration
// options that are passed in via environment variables. This is run after the
// node is in Running state and only if TS_AUTH_ONCE is set.
func tappervpnSet(ctx context.Context, cfg *settings) error {
	args := []string{"--socket=" + cfg.Socket, "set"}
	if cfg.AcceptDNS != nil && *cfg.AcceptDNS {
		args = append(args, "--accept-dns=true")
	} else {
		args = append(args, "--accept-dns=false")
	}
	// --advertise-routes can be passed an empty string to configure a
	// device (that might have previously advertised subnet routes) to not
	// advertise any routes. Respect an empty string passed by a user and
	// use it to explicitly unset the routes.
	if cfg.Routes != nil {
		args = append(args, "--advertise-routes="+*cfg.Routes)
	}
	if cfg.Hostname != "" {
		args = append(args, "--hostname="+cfg.Hostname)
	}
	log.Printf("Running 'tappervpn set'")
	cmd := exec.CommandContext(ctx, "tappervpn", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			// See the equivalent check in tappervpnUp.
			return fmt.Errorf("tappervpn set failed: %w", ctxErr)
		}
		return fmt.Errorf("tappervpn set failed: %w", err)
	}
	return nil
}

func watchTapperVPNdConfigChanges(ctx context.Context, path string, lc *local.Client, errCh chan<- error) {
	var (
		tickChan          <-chan time.Time
		eventChan         <-chan fsnotify.Event
		errChan           <-chan error
		tappervpndCfgDir  = filepath.Dir(path)
		prevTapperVPNdCfg []byte
	)
	if w, err := fsnotify.NewWatcher(); err != nil {
		// Creating a new fsnotify watcher would fail for example if inotify was not able to create a new file descriptor.
		// See https://github.com/tailscale/tailscale/issues/15081
		log.Printf("tappervpnd config watch: failed to create fsnotify watcher, timer-only mode: %v", err)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		tickChan = ticker.C
	} else {
		defer w.Close()
		if err := w.Add(tappervpndCfgDir); err != nil {
			errCh <- fmt.Errorf("failed to add fsnotify watch: %w", err)
			return
		}
		eventChan = w.Events
		errChan = w.Errors
	}
	b, err := os.ReadFile(path)
	if err != nil {
		errCh <- fmt.Errorf("error reading configfile: %w", err)
		return
	}
	prevTapperVPNdCfg = b
	// kubelet mounts Secrets to Pods using a series of symlinks, one of
	// which is <mount-dir>/..data that Kubernetes recommends consumers to
	// use if they need to monitor changes
	// https://github.com/kubernetes/kubernetes/blob/v1.28.1/pkg/volume/util/atomic_writer.go#L39-L61
	const kubeletMountedCfg = "..data"
	toWatch := filepath.Join(tappervpndCfgDir, kubeletMountedCfg)
	for {
		select {
		case <-ctx.Done():
			return
		case err := <-errChan:
			errCh <- fmt.Errorf("watcher error: %w", err)
			return
		case <-tickChan:
		case event := <-eventChan:
			if event.Name != toWatch {
				continue
			}
		}
		b, err := os.ReadFile(path)
		if err != nil {
			errCh <- fmt.Errorf("error reading configfile: %w", err)
			return
		}
		// For some proxy types the mounted volume also contains tappervpnd state and other files. We
		// don't want to reload config unnecessarily on unrelated changes to these files.
		if reflect.DeepEqual(b, prevTapperVPNdCfg) {
			continue
		}
		prevTapperVPNdCfg = b
		log.Printf("tappervpnd config watch: ensuring that config is up to date")
		ok, err := lc.ReloadConfig(ctx)
		if err != nil {
			errCh <- fmt.Errorf("error reloading tappervpnd config: %w", err)
			return
		}
		if ok {
			log.Printf("tappervpnd config watch: config was reloaded")
		}
	}
}
