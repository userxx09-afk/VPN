// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package vmtest_test

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/creachadair/mds/shell"
	"tappervpn.com/tailcfg"
	"tappervpn.com/tstest"
	"tappervpn.com/tstest/natlab/vmtest"
	"tappervpn.com/tstest/natlab/vnet"
)

// TestTapperVPNSSH_Ubuntu exercises the TapperVPN SSH server ("tappervpn up
// --ssh", not a system sshd) on an Ubuntu node, with an Ubuntu node as the SSH
// client. It tests logging in as both root and a non-root user, which
// exercises the incubator's su-based login path.
func TestTapperVPNSSH_Ubuntu(t *testing.T) {
	testSuite := newTestSuite(t, "ubuntu", vmtest.Ubuntu2404)

	ubuntu := testSuite.server

	if out, err := testSuite.env.SSHExec(ubuntu, "useradd -m -s /bin/bash tsuser"); err != nil {
		t.Fatalf("useradd: %v\n%s", err, out)
	}

	testSuite.waitSSH(t, "ubuntu", "root")

	testSuite.check(t, "ubuntu root login", "root", "id -un && pwd", "root\n/root")
	testSuite.check(t, "ubuntu non-root login", "tsuser", "id -un && pwd", "tsuser\n/home/tsuser")

	if out, code := testSuite.ssh(t, "nosuchuser", "true"); code == 0 {
		t.Errorf("ubuntu nonexistent user: ssh succeeded, want failure:\n%s", out)
	}

	testSuite.checkAutogroupNonroot(t, "ubuntu")
}

// TestTapperVPNSSH_FreeBSD exercises the TapperVPN SSH server ("tappervpn up
// --ssh", not a system sshd) on a FreeBSD node, with an Ubuntu node as the SSH
// client. It tests logging in as both root and a non-root user, which
// exercises the incubator's su-based login path.
func TestTapperVPNSSH_FreeBSD(t *testing.T) {
	testSuite := newTestSuite(t, "freebsd", vmtest.FreeBSD150)

	freebsd := testSuite.server

	if out, err := testSuite.env.SSHExec(freebsd, "pw useradd tsuser -m"); err != nil {
		t.Fatalf("pw useradd: %v\n%s", err, out)
	}

	testSuite.waitSSH(t, "freebsd", "root")

	testSuite.check(t, "freebsd root login", "root", "id -un && pwd", "root\n/root")
	testSuite.check(t, "freebsd non-root login", "tsuser", "id -un && pwd", "tsuser\n/home/tsuser")

	if out, code := testSuite.ssh(t, "nosuchuser", "true"); code == 0 {
		t.Errorf("freebsd nonexistent user: ssh succeeded, want failure:\n%s", out)
	}

	testSuite.checkAutogroupNonroot(t, "freebsd")
}

// TestTapperVPNSSH_Gokrazy exercises the gokrazy-specific cases in the
// TapperVPN SSH server ("tappervpn up --ssh", not a system sshd), with an
// Ubuntu node as the SSH client. util/osuser hard-codes the login shell to
// /tmp/serial-busybox/ash and falls back to a synthesized root user (uid 0,
// home dir "/") when user lookup fails, since gokrazy has no user database;
// and the incubator's findSU refuses to use su on gokrazy, so sessions are
// handled in-process.
func TestTapperVPNSSH_Gokrazy(t *testing.T) {
	testSuite := newTestSuite(t, "gokrazy", vmtest.Gokrazy)

	testSuite.waitSSH(t, "gokrazy", "root")

	// $0 is the login shell the session ran the command with, which on gokrazy
	// is hard-coded rather than looked up with getent. These sessions also
	// implicitly exercise the incubator's su-less in-process path, since
	// gokrazy has no su.
	testSuite.check(t, "gokrazy root login shell", "root", "echo $0", "/tmp/serial-busybox/ash")
	testSuite.check(t, "gokrazy nonexistent user maps to root", "nosuchuser", "pwd", "/")
}

// ssh runs cmd as user on the node at ip over TapperVPN SSH, from the
// client node, returning the combined output and the ssh client's exit
// code. The command run via Env.SSHExec always exits 0 so that its
// transport-error (exit 255) retry loop doesn't kick in when a
// TapperVPN SSH connection is expected to fail.
const exitMarker = "tappervpn-ssh-exit="

type sshTest struct {
	client   *vmtest.Node
	server   *vmtest.Node
	serverIP netip.Addr
	env      *vmtest.Env
}

// ssh runs a command as the specified user on the [sshTest.server] via its
// debug SSH NIC.
func (st *sshTest) ssh(t *testing.T, user string, cmd string) (string, int) {
	t.Helper()

	inner := "ssh" +
		" -o StrictHostKeyChecking=no" +
		" -o UserKnownHostsFile=/dev/null" +
		" -o BatchMode=yes" +
		" -o ConnectTimeout=10" +
		" -o LogLevel=ERROR" +
		" " + user + "@" + st.serverIP.String() +
		" " + shell.Quote(cmd)
	out, err := st.env.SSHExec(st.client, inner+" 2>&1; echo "+exitMarker+"$?")
	if err != nil {
		t.Fatalf("ssh %s@%s: %v\n%s", user, st.serverIP, err, out)
	}
	i := strings.LastIndex(out, exitMarker)
	if i == -1 {
		t.Fatalf("ssh %s@%s: no exit marker in output:\n%s", user, st.serverIP, out)
	}
	code, err := strconv.Atoi(strings.TrimSpace(out[i+len(exitMarker):]))
	if err != nil {
		t.Fatalf("ssh %s@%s: bad exit marker in output:\n%s", user, st.serverIP, out)
	}
	return strings.TrimSpace(out[:i]), code
}

// waitSSH waits for the node's TapperVPN SSH server to accept a
// connection; the first one can race tappervpnd's SSH server startup
// and WireGuard path setup.
func (st *sshTest) waitSSH(t *testing.T, name, user string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for {
		out, code := st.ssh(t, user, "echo ok")
		if code == 0 && strings.Contains(out, "ok") {
			t.Logf("[%s] TapperVPN SSH up as %s@%s", name, user, st.serverIP)
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("[%s] TapperVPN SSH as %s@%s never came up; last output (exit %d):\n%s", name, user, st.serverIP, code, out)
		}
		time.Sleep(2 * time.Second)
	}
}

// check attempts to execute a command as specified user on the server, failing
// if the output isn't what is expected.
func (st *sshTest) check(t *testing.T, desc, user string, cmd, want string) {
	t.Helper()
	out, code := st.ssh(t, user, cmd)
	if code != 0 {
		t.Errorf("%s: ssh %s@%s %q exited %d:\n%s", desc, user, st.serverIP, cmd, code, out)
		return
	}
	if out != want {
		t.Errorf("%s: ssh %s@%s %q = %q, want %q", desc, user, st.serverIP, cmd, out, want)
	}
}

// checkAutogroupNonroot adjusts the server's SSH policy to be equivalent to "autogroup:nonroot"
// and verifies that attempting to SSH as root fails.
func (st *sshTest) checkAutogroupNonroot(t *testing.T, name string) {
	t.Helper()
	orignalSSHPolicy := st.env.ControlServer().SSHPolicy
	defer func() {
		st.env.ControlServer().SetSSHPolicy(orignalSSHPolicy)
	}()

	st.env.ControlServer().SetSSHPolicy(&tailcfg.SSHPolicy{
		Rules: []*tailcfg.SSHRule{{
			Principals: []*tailcfg.SSHPrincipal{{Any: true}},
			SSHUsers:   map[string]string{"*": "="},
			Action:     &tailcfg.SSHAction{Accept: true},
		}},
	})

	if err := tstest.WaitFor(30*time.Second, func() error {
		_, code := st.ssh(t, "root", "true")
		if code == 0 {
			return fmt.Errorf("root SSH still succeeds")
		}
		return nil
	}); err != nil {
		t.Fatalf("%s: root SSH still succeeds after policy update to autogroup:nonroot-only; policy may not have propagated", name)
	}
}

func newTestSuite(t *testing.T, serverName string, serverOS vmtest.OSImage) *sshTest {
	t.Helper()
	env := vmtest.New(t)
	client := sshTestNode(env, "client", vmtest.Ubuntu2404)
	server := sshTestNode(env, serverName, serverOS, vmtest.TapperVPNSSH())
	env.Start()

	serverIP := tappervpnIP4(t, env, server)

	return &sshTest{
		client:   client,
		server:   server,
		serverIP: serverIP,
		env:      env,
	}
}

// sshTestNode adds a node named name running img behind its own
// easy-NAT network.
func sshTestNode(env *vmtest.Env, name string, img vmtest.OSImage, opts ...any) *vmtest.Node {
	n := env.NumNodes()
	opts = append([]any{
		env.AddNetwork(
			fmt.Sprintf("2.%d.%d.%d", n, n, n), // public IP
			fmt.Sprintf("192.168.%d.1/24", n), vnet.EasyNAT),
		vmtest.OS(img),
	}, opts...)
	return env.AddNode(name, opts...)
}

// tappervpnIP4 returns the node's IPv4 TapperVPN address.
func tappervpnIP4(t *testing.T, env *vmtest.Env, n *vmtest.Node) netip.Addr {
	t.Helper()
	st := env.Status(n)
	for _, ip := range st.Self.TapperVPNIPs {
		if ip.Is4() {
			return ip
		}
	}
	t.Fatalf("no IPv4 TapperVPN address for %s; have %v", n.Name(), st.Self.TapperVPNIPs)
	panic("unreachable")
}
