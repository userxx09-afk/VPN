// Copyright (c) TapperVPN contributors
// SPDX-License-Identifier: BSD-3-Clause

package localcontrol

import (
	"net"
	"slices"
	"testing"

	"golang.org/x/net/dns/dnsmessage"
)

func buildAnswer(t *testing.T, service string, aIP [4]byte, port uint16) []byte {
	t.Helper()
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{Response: true, Authoritative: true})
	inst := dnsmessage.MustNewName("TapperVPN control on tapper." + service)
	host := dnsmessage.MustNewName("tapper.local.")
	hdr := func(n dnsmessage.Name, typ dnsmessage.Type) dnsmessage.ResourceHeader {
		return dnsmessage.ResourceHeader{Name: n, Type: typ, Class: dnsmessage.ClassINET, TTL: 10}
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(b.StartAnswers())
	must(b.PTRResource(hdr(dnsmessage.MustNewName(service), dnsmessage.TypePTR), dnsmessage.PTRResource{PTR: inst}))
	must(b.StartAdditionals())
	must(b.SRVResource(hdr(inst, dnsmessage.TypeSRV), dnsmessage.SRVResource{Target: host, Port: port}))
	must(b.AResource(hdr(host, dnsmessage.TypeA), dnsmessage.AResource{A: aIP}))
	msg, err := b.Finish()
	must(err)
	return msg
}

func TestParseAnswer(t *testing.T) {
	src := &net.UDPAddr{IP: net.IPv4(10, 0, 0, 156), Port: 5353}

	got := parseAnswer(buildAnswer(t, Service, [4]byte{10, 0, 0, 156}, 8080), src)
	if want := []string{"http://10.0.0.156:8080"}; !slices.Equal(got, want) {
		t.Errorf("our service: got %v, want %v", got, want)
	}

	// Another service type is ignored.
	if got := parseAnswer(buildAnswer(t, "_http._tcp.local.", [4]byte{10, 0, 0, 156}, 80), src); got != nil {
		t.Errorf("other service: got %v, want nothing", got)
	}

	// A public address is never used, even if advertised.
	if got := parseAnswer(buildAnswer(t, Service, [4]byte{8, 8, 8, 8}, 8080), src); got != nil {
		t.Errorf("public address: got %v, want nothing", got)
	}

	if got := parseAnswer([]byte("junk"), src); got != nil {
		t.Errorf("junk: got %v", got)
	}
}

func TestApplies(t *testing.T) {
	for url, want := range map[string]bool{
		PublicURL:                   true,
		PublicURL + "/":             true,
		"http://192.168.5.210:8080": false,
		"":                          false,
	} {
		if got := Applies(url); got != want {
			t.Errorf("Applies(%q) = %v, want %v", url, got, want)
		}
	}
}
