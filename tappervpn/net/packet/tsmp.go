// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

// TSMP is our ICMP-like "TapperVPN Message Protocol" for signaling
// TapperVPN-specific messages between nodes. It uses IP protocol 99
// (reserved for "any private encryption scheme") within
// WireGuard's normal encryption between peers and never hits the host
// network stack.

package packet

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"

	"go4.org/mem"
	"tappervpn.com/types/ipproto"
	"tappervpn.com/types/key"
)

const minTSMPSize = 7 // the rejected body is 7 bytes

// TapperVPNRejectedHeader is a TSMP message that says that one
// TapperVPN node has rejected the connection from another. Unlike a
// TCP RST, this includes a reason.
//
// On the wire, after the IP header, it's currently 7 or 8 bytes:
//   - '!'
//   - IPProto byte (IANA protocol number: TCP or UDP)
//   - byte stating rejection reason (see [TapperVPNRejectReason] for valid values)
//   - srcPort big endian uint16
//   - dstPort big endian uint16
//   - [optional] byte of flag bits:
//     lowest bit (0x1): MaybeBroken
//
// In the future it might also accept 16 byte IP flow src/dst IPs
// after the header, if they're different than the IP-level ones.
type TapperVPNRejectedHeader struct {
	IPSrc  netip.Addr            // IPv4 or IPv6 header's src IP
	IPDst  netip.Addr            // IPv4 or IPv6 header's dst IP
	Src    netip.AddrPort        // rejected flow's src
	Dst    netip.AddrPort        // rejected flow's dst
	Proto  ipproto.Proto         // proto that was rejected (TCP or UDP)
	Reason TapperVPNRejectReason // why the connection was rejected

	// MaybeBroken is whether the rejection is non-terminal (the
	// client should not fail immediately). This is sent by a
	// target when it's not sure whether it's totally broken, but
	// it might be. For example, the target tappervpnd might think
	// its host firewall or IP forwarding aren't configured
	// properly, but tappervpnd might be wrong (not having enough
	// visibility into what the OS is doing). When true, the
	// message is simply an FYI as a potential reason to use for
	// later when the pendopen connection tracking timer expires.
	MaybeBroken bool
}

const rejectFlagBitMaybeBroken = 0x1

func (rh TapperVPNRejectedHeader) String() string {
	return fmt.Sprintf("TSMP-reject-flow{%s %s > %s}: %s", rh.Proto, rh.Src, rh.Dst, rh.Reason)
}

// TSMPType is the type byte of a TSMP message. It follows the IPv4 or IPv6 header.
type TSMPType uint8

const (
	// TSMPTypeRejectedConn is the type byte for a [TapperVPNRejectedHeader].
	TSMPTypeRejectedConn TSMPType = '!'

	// TSMPTypePing is the type byte for a [TSMPPingRequest].
	TSMPTypePing TSMPType = 'p'

	// TSMPTypePong is the type byte for a [TSMPPongReply].
	TSMPTypePong TSMPType = 'o'

	// TSMPTypeDiscoAdvertisement is the type byte for a [TSMPDiscoKeyAdvertisement].
	TSMPTypeDiscoAdvertisement TSMPType = 'a'
)

type TapperVPNRejectReason byte

// IsZero reports whether r is the zero value, representing no rejection.
func (r TapperVPNRejectReason) IsZero() bool { return r == TapperVPNRejectReasonNone }

const (
	// TapperVPNRejectReasonNone is the TapperVPNRejectReason zero value.
	TapperVPNRejectReasonNone TapperVPNRejectReason = 0

	// RejectedDueToACLs means that the host rejected the connection due to ACLs.
	RejectedDueToACLs TapperVPNRejectReason = 'A'

	// RejectedDueToShieldsUp means that the host rejected the connection due to shields being up.
	RejectedDueToShieldsUp TapperVPNRejectReason = 'S'

	// RejectedDueToIPForwarding means that the relay node's IP
	// forwarding is disabled.
	RejectedDueToIPForwarding TapperVPNRejectReason = 'F'

	// RejectedDueToHostFirewall means that the target host's
	// firewall is blocking the traffic.
	RejectedDueToHostFirewall TapperVPNRejectReason = 'W'

	// RejectedDueToUnknownAppConnectorTransitIP means that the connector host has no real IP
	// mapping that matches the provided transit IP for this client, so the
	// connector has no destination to forward the connection to.
	RejectedDueToUnknownAppConnectorTransitIP TapperVPNRejectReason = 'T'
)

func (r TapperVPNRejectReason) String() string {
	switch r {
	case RejectedDueToACLs:
		return "acl"
	case RejectedDueToShieldsUp:
		return "shields"
	case RejectedDueToIPForwarding:
		return "host-ip-forwarding-unavailable"
	case RejectedDueToHostFirewall:
		return "host-firewall"
	case RejectedDueToUnknownAppConnectorTransitIP:
		return "app-connector-transit-ip-unknown"
	}
	return fmt.Sprintf("0x%02x", byte(r))
}

func (h TapperVPNRejectedHeader) hasFlags() bool {
	return h.MaybeBroken // the only one currently
}

func (h TapperVPNRejectedHeader) Len() int {
	v := 1 + // TSMPType byte
		1 + // IPProto byte
		1 + // TapperVPNRejectReason byte
		2*2 // 2 uint16 ports
	if h.IPSrc.Is4() {
		v += ip4HeaderLength
	} else if h.IPSrc.Is6() {
		v += ip6HeaderLength
	}
	if h.hasFlags() {
		v++
	}
	return v
}

func (h TapperVPNRejectedHeader) Marshal(buf []byte) error {
	if len(buf) < h.Len() {
		return errSmallBuffer
	}
	if len(buf) > maxPacketLength {
		return errLargePacket
	}
	if h.Src.Addr().Is4() {
		iph := IP4Header{
			IPProto: ipproto.TSMP,
			Src:     h.IPSrc,
			Dst:     h.IPDst,
		}
		iph.Marshal(buf)
		buf = buf[ip4HeaderLength:]
	} else if h.Src.Addr().Is6() {
		iph := IP6Header{
			IPProto: ipproto.TSMP,
			Src:     h.IPSrc,
			Dst:     h.IPDst,
		}
		iph.Marshal(buf)
		buf = buf[ip6HeaderLength:]
	} else {
		return errors.New("bogus src IP")
	}
	buf[0] = byte(TSMPTypeRejectedConn)
	buf[1] = byte(h.Proto)
	buf[2] = byte(h.Reason)
	binary.BigEndian.PutUint16(buf[3:5], h.Src.Port())
	binary.BigEndian.PutUint16(buf[5:7], h.Dst.Port())

	if h.hasFlags() {
		var flags byte
		if h.MaybeBroken {
			flags |= rejectFlagBitMaybeBroken
		}
		buf[7] = flags
	}
	return nil
}

// AsTapperVPNRejectedHeader parses pp as an incoming rejection
// connection TSMP message.
//
// ok reports whether pp was a valid TSMP rejection packet.
func (pp *Parsed) AsTapperVPNRejectedHeader() (h TapperVPNRejectedHeader, ok bool) {
	p := pp.Payload()
	if len(p) < 7 || p[0] != byte(TSMPTypeRejectedConn) {
		return
	}
	h = TapperVPNRejectedHeader{
		Proto:  ipproto.Proto(p[1]),
		Reason: TapperVPNRejectReason(p[2]),
		IPSrc:  pp.Src.Addr(),
		IPDst:  pp.Dst.Addr(),
		Src:    netip.AddrPortFrom(pp.Dst.Addr(), binary.BigEndian.Uint16(p[3:5])),
		Dst:    netip.AddrPortFrom(pp.Src.Addr(), binary.BigEndian.Uint16(p[5:7])),
	}
	if len(p) > 7 {
		flags := p[7]
		h.MaybeBroken = (flags & rejectFlagBitMaybeBroken) != 0
	}
	return h, true
}

// TSMPPingRequest is a TSMP message that's like an ICMP ping request.
//
// On the wire, after the IP header, it's currently 9 bytes:
//   - 'p' (TSMPTypePing)
//   - 8 opaque ping bytes to copy back in the response
type TSMPPingRequest struct {
	Data [8]byte
}

func (pp *Parsed) AsTSMPPing() (h TSMPPingRequest, ok bool) {
	if pp.IPProto != ipproto.TSMP {
		return
	}
	p := pp.Payload()
	if len(p) < 9 || p[0] != byte(TSMPTypePing) {
		return
	}
	copy(h.Data[:], p[1:])
	return h, true
}

type TSMPPongReply struct {
	IPHeader    Header
	Data        [8]byte
	PeerAPIPort uint16
}

// AsTSMPPong returns pp as a TSMPPongReply and whether it is one.
// The pong.IPHeader field is not populated.
func (pp *Parsed) AsTSMPPong() (pong TSMPPongReply, ok bool) {
	if pp.IPProto != ipproto.TSMP {
		return
	}
	p := pp.Payload()
	if len(p) < 9 || p[0] != byte(TSMPTypePong) {
		return
	}
	copy(pong.Data[:], p[1:])
	if len(p) >= 11 {
		pong.PeerAPIPort = binary.BigEndian.Uint16(p[9:])
	}
	return pong, true
}

func (h TSMPPongReply) Len() int {
	return h.IPHeader.Len() + 11
}

func (h TSMPPongReply) Marshal(buf []byte) error {
	if len(buf) < h.Len() {
		return errSmallBuffer
	}
	if err := h.IPHeader.Marshal(buf); err != nil {
		return err
	}
	buf = buf[h.IPHeader.Len():]
	buf[0] = byte(TSMPTypePong)
	copy(buf[1:], h.Data[:])
	binary.BigEndian.PutUint16(buf[9:11], h.PeerAPIPort)
	return nil
}

// TSMPDiscoKeyAdvertisement is a TSMP message that's used for distributing
// [key.DiscoPublic]. It is sent immediately following eligible WireGuard
// session establishment starting at [tappervpn.com/tailcfg.CapabilityVersion] 144.
//
// On the wire, after the IP header, it's currently 33 bytes:
//   - 'a' (TSMPTypeDiscoAdvertisement)
//   - 32 disco key bytes
type TSMPDiscoKeyAdvertisement struct {
	Src, Dst netip.Addr // Src and Dst are set from the parent IP Header when parsing.
	Key      key.DiscoPublic
}

// Marshal marshals ka to a wire representation appropriate for transmission
// over WireGuard to a remote client. A marshaled [TSMPDiscoKeyAdvertisement]
// must not exceed [github.com/tailscale/wireguard-go/device.MaxPriorityMessageContentSize],
// or wireguard-go will reject and discard the payload.
func (ka *TSMPDiscoKeyAdvertisement) Marshal() ([]byte, error) {
	var iph Header
	if ka.Src.Is4() {
		iph = IP4Header{
			IPProto: ipproto.TSMP,
			Src:     ka.Src,
			Dst:     ka.Dst,
		}
	} else {
		iph = IP6Header{
			IPProto: ipproto.TSMP,
			Src:     ka.Src,
			Dst:     ka.Dst,
		}
	}
	payload := make([]byte, 0, 1+key.DiscoPublicRawLen)
	payload = append(payload, byte(TSMPTypeDiscoAdvertisement))
	payload = ka.Key.AppendTo(payload)
	if len(payload) != 33 {
		// Mostly to safeguard against ourselves changing this in the future.
		return []byte{}, fmt.Errorf("expected payload length 33, got %d", len(payload))
	}

	return Generate(iph, payload[:]), nil
}

func (pp *Parsed) AsTSMPDiscoAdvertisement() (tka TSMPDiscoKeyAdvertisement, ok bool) {
	if pp.IPProto != ipproto.TSMP {
		return
	}
	p := pp.Payload()
	if len(p) < 33 || p[0] != byte(TSMPTypeDiscoAdvertisement) {
		return
	}
	tka.Src = pp.Src.Addr()
	tka.Dst = pp.Dst.Addr()
	tka.Key = key.DiscoPublicFromRaw32(mem.B(p[1:33]))

	return tka, true
}
