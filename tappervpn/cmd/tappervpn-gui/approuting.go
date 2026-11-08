// Copyright (c) TapperVPN contributors
// SPDX-License-Identifier: BSD-3-Clause

//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"tappervpn.com/client/tappervpn/apitype"
)

// AppRouting is per-app routing as the window shows it: which apps use the
// exit node. It's kept by the service (LocalAPI "app-routing") and only
// applies while an exit node is selected.
type AppRouting struct {
	Available  bool     `json:"available"`            // the service supports it
	Mode       string   `json:"mode"`                 // "off": every app; "only": just Apps; "except": all but Apps
	Apps       []string `json:"apps"`                 // exe names, e.g. "chrome.exe"
	Source     string   `json:"source,omitempty"`     // where the active rules come from: "app", "control", ""
	ControlSet bool     `json:"controlSet,omitempty"` // the server sets rules for this PC, overriding these
	Error      string   `json:"error,omitempty"`      // why the active rules were rejected
}

// policy is the subset of the service's per-app policy the window edits
// (see feature/approuting/policy.go).
type policy struct {
	Version            int    `json:"version"`
	Default            string `json:"default,omitempty"`
	Unidentified       string `json:"unidentified,omitempty"`
	GatewayUnavailable string `json:"gatewayUnavailable,omitempty"`
	Rules              []struct {
		Name   string `json:"name,omitempty"`
		Action string `json:"action"`
		Apps   []struct {
			Windows *struct {
				Exe  string `json:"exe,omitempty"`
				Path string `json:"path,omitempty"`
			} `json:"windows,omitempty"`
		} `json:"apps"`
	} `json:"rules,omitempty"`
}

type appRoutingStatus struct {
	Policy     json.RawMessage `json:"policy"`
	Source     string          `json:"source"`
	Error      string          `json:"error"`
	ControlSet bool            `json:"controlSet"`
}

func (h *hub) appRoutingCall(ctx context.Context, method string, body any) (*appRoutingStatus, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://"+apitype.LocalAPIHost+"/localapi/v0/app-routing", rd)
	if err != nil {
		return nil, err
	}
	res, err := h.lc.DoLocalRequest(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s", strings.TrimSpace(string(b)))
	}
	var st appRoutingStatus
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

func (h *hub) readAppRouting(ctx context.Context) AppRouting {
	st, err := h.appRoutingCall(ctx, "GET", nil)
	if err != nil {
		return AppRouting{Mode: "off", Apps: []string{}} // older service without it
	}
	ar := AppRouting{Available: true, Mode: "off", Apps: []string{},
		Source: st.Source, ControlSet: st.ControlSet, Error: st.Error}
	var p policy
	if len(st.Policy) > 0 && string(st.Policy) != "null" && json.Unmarshal(st.Policy, &p) == nil {
		for _, r := range p.Rules {
			for _, a := range r.Apps {
				if a.Windows != nil {
					name := a.Windows.Exe
					if name == "" {
						name = a.Windows.Path
					}
					if name != "" && !slices.Contains(ar.Apps, name) {
						ar.Apps = append(ar.Apps, name)
					}
				}
			}
		}
		if p.Default == "direct" {
			ar.Mode = "only"
		} else {
			ar.Mode = "except"
		}
	}
	return ar
}

// SetAppRouting saves which apps use the exit node. mode "off" (or no apps)
// sends every app through the exit node, like having no rules.
func (h *hub) SetAppRouting(mode string, apps []string) error {
	var clean []string
	for _, a := range apps {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		if !strings.ContainsAny(a, `\/`) && !strings.HasSuffix(strings.ToLower(a), ".exe") {
			a += ".exe"
		}
		if !slices.ContainsFunc(clean, func(c string) bool { return strings.EqualFold(c, a) }) {
			clean = append(clean, a)
		}
	}
	var pol any // nil: no rules
	if (mode == "only" || mode == "except") && len(clean) > 0 {
		def, action := "gateway", "direct"
		if mode == "only" {
			def, action = "direct", "gateway"
		}
		type win struct {
			Exe  string `json:"exe,omitempty"`
			Path string `json:"path,omitempty"`
		}
		type app struct {
			Windows win `json:"windows"`
		}
		var list []app
		for _, a := range clean {
			if strings.ContainsAny(a, `\/`) {
				list = append(list, app{win{Path: a}})
			} else {
				list = append(list, app{win{Exe: a}})
			}
		}
		pol = map[string]any{
			"version":      1,
			"default":      def,
			"unidentified": def,
			"rules": []any{map[string]any{
				"name": "Chosen in the TapperVPN app", "action": action, "apps": list,
			}},
		}
	}
	return h.do("Saving per-app routing", func(ctx context.Context) error {
		_, err := h.appRoutingCall(ctx, "POST", map[string]any{"policy": pol})
		return err
	})
}

// Apps that are part of Windows rather than something to route.
var skipApps = map[string]bool{
	"explorer.exe": true, "applicationframehost.exe": true, "textinputhost.exe": true,
	"shellexperiencehost.exe": true, "startmenuexperiencehost.exe": true, "searchhost.exe": true,
	"systemsettings.exe": true, "lockapp.exe": true, "tappervpn-gui.exe": true, "dwm.exe": true,
}

var (
	procEnumWindows              = user32.NewProc("EnumWindows")
	procIsWindowVisible          = user32.NewProc("IsWindowVisible")
	procGetWindowTextLengthW     = user32.NewProc("GetWindowTextLengthW")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
)

// runningApps lists the programs that have a visible window right now, by
// exe name, for the "add an app" picker.
func runningApps() []string {
	pids := map[uint32]bool{}
	cb := syscall.NewCallback(func(hwnd, _ uintptr) uintptr {
		if v, _, _ := procIsWindowVisible.Call(hwnd); v == 0 {
			return 1
		}
		if n, _, _ := procGetWindowTextLengthW.Call(hwnd); n == 0 {
			return 1
		}
		var pid uint32
		procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
		pids[pid] = true
		return 1
	})
	procEnumWindows.Call(cb, 0)
	var names []string
	for pid := range pids {
		hp, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
		if err != nil {
			continue
		}
		buf := make([]uint16, windows.MAX_PATH)
		n := uint32(len(buf))
		err = windows.QueryFullProcessImageName(hp, 0, &buf[0], &n)
		windows.CloseHandle(hp)
		if err != nil {
			continue
		}
		name := strings.ToLower(filepath.Base(windows.UTF16ToString(buf[:n])))
		if !skipApps[name] && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}
