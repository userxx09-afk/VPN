// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package approuting

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"unsafe"

	"golang.org/x/sys/windows"
	"tappervpn.com/types/ipproto"
)

// See https://learn.microsoft.com/en-us/windows/win32/api/iphlpapi/nf-iphlpapi-getextendedtcptable
// and GetExtendedUdpTable.
const (
	tcpTableOwnerPIDAll = 5 // TCP_TABLE_OWNER_PID_ALL
	udpTableOwnerPID    = 1 // UDP_TABLE_OWNER_PID
)

var (
	iphlpapi            = windows.NewLazySystemDLL("iphlpapi.dll")
	getExtendedTcpTable = iphlpapi.NewProc("GetExtendedTcpTable")
	getExtendedUdpTable = iphlpapi.NewProc("GetExtendedUdpTable")
)

// MIB_TCPROW_OWNER_PID
type mibTCPRowOwnerPID struct {
	state, localAddr, localPort, remoteAddr, remotePort, pid uint32
}

// MIB_TCP6ROW_OWNER_PID
type mibTCP6RowOwnerPID struct {
	localAddr   [16]byte
	localScope  uint32
	localPort   uint32
	remoteAddr  [16]byte
	remoteScope uint32
	remotePort  uint32
	state       uint32
	pid         uint32
}

// MIB_UDPROW_OWNER_PID
type mibUDPRowOwnerPID struct {
	localAddr, localPort, pid uint32
}

// MIB_UDP6ROW_OWNER_PID
type mibUDP6RowOwnerPID struct {
	localAddr  [16]byte
	localScope uint32
	localPort  uint32
	pid        uint32
}

func newOSOwnerLookup() *ownerLookup {
	return newOwnerLookup(fetchSocketTable, processImagePath)
}

func fetchSocketTable() ([]sockRow, error) {
	var rows []sockRow
	for _, q := range []struct {
		proc  *windows.LazyProc
		proto ipproto.Proto
		fam   uint32
		class uint32
	}{
		{getExtendedTcpTable, ipproto.TCP, windows.AF_INET, tcpTableOwnerPIDAll},
		{getExtendedTcpTable, ipproto.TCP, windows.AF_INET6, tcpTableOwnerPIDAll},
		{getExtendedUdpTable, ipproto.UDP, windows.AF_INET, udpTableOwnerPID},
		{getExtendedUdpTable, ipproto.UDP, windows.AF_INET6, udpTableOwnerPID},
	} {
		buf, err := callTable(q.proc, q.fam, q.class)
		if err != nil {
			return nil, fmt.Errorf("%s(af=%d): %w", q.proc.Name, q.fam, err)
		}
		rows = appendRows(rows, buf, q.proto, q.fam)
	}
	return rows, nil
}

// callTable calls GetExtendedTcpTable or GetExtendedUdpTable, growing the
// buffer as needed. The returned buffer starts with a uint32 row count.
func callTable(proc *windows.LazyProc, fam, class uint32) ([]byte, error) {
	size := uint32(16 << 10)
	for range 5 {
		buf := make([]byte, size)
		ret, _, _ := proc.Call(
			uintptr(unsafe.Pointer(&buf[0])),
			uintptr(unsafe.Pointer(&size)),
			0, // unsorted
			uintptr(fam),
			uintptr(class),
			0, // reserved
		)
		switch windows.Errno(ret) {
		case 0:
			return buf[:size], nil
		case windows.ERROR_INSUFFICIENT_BUFFER:
			if size > 16<<20 {
				return nil, fmt.Errorf("unreasonable table size %d", size)
			}
			size += 4 << 10 // the table may grow between calls
			continue
		default:
			return nil, windows.Errno(ret)
		}
	}
	return nil, errors.New("socket table kept growing")
}

func appendRows(rows []sockRow, buf []byte, proto ipproto.Proto, fam uint32) []sockRow {
	if len(buf) < 4 {
		return rows
	}
	n := int(binary.LittleEndian.Uint32(buf))
	// Rows start after the count, aligned to the row's alignment (4 for
	// all four row types).
	base := unsafe.Pointer(&buf[4])
	avail := len(buf) - 4
	switch {
	case proto == ipproto.TCP && fam == windows.AF_INET:
		n = min(n, avail/int(unsafe.Sizeof(mibTCPRowOwnerPID{})))
		for _, r := range unsafe.Slice((*mibTCPRowOwnerPID)(base), n) {
			rows = append(rows, sockRow{proto, netip.AddrPortFrom(addr4(r.localAddr), port(r.localPort)), r.pid})
		}
	case proto == ipproto.TCP && fam == windows.AF_INET6:
		n = min(n, avail/int(unsafe.Sizeof(mibTCP6RowOwnerPID{})))
		for _, r := range unsafe.Slice((*mibTCP6RowOwnerPID)(base), n) {
			rows = append(rows, sockRow{proto, netip.AddrPortFrom(netip.AddrFrom16(r.localAddr), port(r.localPort)), r.pid})
		}
	case proto == ipproto.UDP && fam == windows.AF_INET:
		n = min(n, avail/int(unsafe.Sizeof(mibUDPRowOwnerPID{})))
		for _, r := range unsafe.Slice((*mibUDPRowOwnerPID)(base), n) {
			rows = append(rows, sockRow{proto, netip.AddrPortFrom(addr4(r.localAddr), port(r.localPort)), r.pid})
		}
	case proto == ipproto.UDP && fam == windows.AF_INET6:
		n = min(n, avail/int(unsafe.Sizeof(mibUDP6RowOwnerPID{})))
		for _, r := range unsafe.Slice((*mibUDP6RowOwnerPID)(base), n) {
			rows = append(rows, sockRow{proto, netip.AddrPortFrom(netip.AddrFrom16(r.localAddr), port(r.localPort)), r.pid})
		}
	}
	return rows
}

// addr4 converts an IPv4 address stored in network byte order in a DWORD.
func addr4(v uint32) netip.Addr {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	return netip.AddrFrom4(b)
}

// port converts a port stored in network byte order in the low 16 bits of
// a DWORD.
func port(v uint32) uint16 {
	return uint16(v>>8&0xff) | uint16(v&0xff)<<8
}

func processImagePath(pid uint32) (string, error) {
	switch pid {
	case 0:
		return "", errors.New("idle process")
	case 4:
		return "System", nil
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return "", err
	}
	return windows.UTF16ToString(buf[:n]), nil
}
