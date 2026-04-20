package dra

import (
	"encoding/binary"
	"errors"
)

// Diameter header is always 20 bytes. We parse it inline (big-endian) so we
// don't need the unexported helpers in commands/base.

const headerLen = 20

// AVP codes we care about at the DRA layer (no full decode needed).
const (
	avpOriginHost       uint32 = 264
	avpOriginRealm      uint32 = 296
	avpDestinationHost  uint32 = 293
	avpDestinationRealm uint32 = 283
)

// parsedHeader is the shallow header view DRA uses for routing decisions.
type parsedHeader struct {
	Version       uint8
	Length        uint32 // total message length incl. header
	Flags         byte
	CommandCode   uint32
	ApplicationID uint32
	HopByHopID    uint32
	EndToEndID    uint32
}

func (h parsedHeader) isRequest() bool { return h.Flags&0x80 != 0 }

// parseHeader parses the 20-byte Diameter header.
func parseHeader(b []byte) (parsedHeader, error) {
	if len(b) < headerLen {
		return parsedHeader{}, errors.New("short header")
	}
	return parsedHeader{
		Version:       b[0],
		Length:        uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3]),
		Flags:         b[4],
		CommandCode:   uint32(b[5])<<16 | uint32(b[6])<<8 | uint32(b[7]),
		ApplicationID: binary.BigEndian.Uint32(b[8:12]),
		HopByHopID:    binary.BigEndian.Uint32(b[12:16]),
		EndToEndID:    binary.BigEndian.Uint32(b[16:20]),
	}, nil
}

// setHopByHop rewrites the HbH id in a full message buffer in place.
func setHopByHop(msg []byte, id uint32) {
	binary.BigEndian.PutUint32(msg[12:16], id)
}

// avpLoc describes where an AVP sits in the message body.
type avpLoc struct {
	start    int // offset of AVP header within the whole message
	hdrLen   int // 8 or 12 (with vendor)
	dataLen  int // length of AVP data (without padding)
	paddedTo int // padded AVP total length (multiple of 4)
}

// iterateAVPs walks top-level AVPs in a whole Diameter message, calling fn
// for each one. Returns the first error fn yields (or nil).
// The caller gets (code, vendorID, loc, body) — body is a slice into msg.
func iterateAVPs(msg []byte, fn func(code, vendorID uint32, loc avpLoc, body []byte) error) error {
	if len(msg) < headerLen {
		return errors.New("message too short")
	}
	pos := headerLen
	for pos < len(msg) {
		if pos+8 > len(msg) {
			return errors.New("truncated AVP header")
		}
		code := binary.BigEndian.Uint32(msg[pos : pos+4])
		flags := msg[pos+4]
		avpLen := uint32(msg[pos+5])<<16 | uint32(msg[pos+6])<<8 | uint32(msg[pos+7])
		if avpLen < 8 || int(avpLen) > len(msg)-pos {
			return errors.New("bad AVP length")
		}
		hdr := 8
		var vendor uint32
		if flags&0x80 != 0 { // V-bit
			if pos+12 > len(msg) {
				return errors.New("truncated vendor AVP")
			}
			vendor = binary.BigEndian.Uint32(msg[pos+8 : pos+12])
			hdr = 12
		}
		dataLen := int(avpLen) - hdr
		if dataLen < 0 {
			return errors.New("bad AVP data length")
		}
		padded := int(avpLen)
		if padded%4 != 0 {
			padded += 4 - (padded % 4)
		}
		loc := avpLoc{start: pos, hdrLen: hdr, dataLen: dataLen, paddedTo: padded}
		body := msg[pos+hdr : pos+hdr+dataLen]
		if err := fn(code, vendor, loc, body); err != nil {
			return err
		}
		pos += padded
	}
	return nil
}

// routingView is what DRA extracts from a forwarded message to make a
// routing decision. Empty strings mean "not present".
type routingView struct {
	OriginHost       string
	OriginRealm      string
	DestinationHost  string
	DestinationRealm string
	OriginHostLoc    avpLoc
	OriginRealmLoc   avpLoc
	hasOriginHost    bool
	hasOriginRealm   bool
}

// extractRouting pulls the four routing AVPs out of a message in one pass.
// Vendor-specific variants are ignored (these AVPs are base protocol, vendor=0).
func extractRouting(msg []byte) (routingView, error) {
	var rv routingView
	err := iterateAVPs(msg, func(code, vendor uint32, loc avpLoc, body []byte) error {
		if vendor != 0 {
			return nil
		}
		switch code {
		case avpOriginHost:
			rv.OriginHost = string(body)
			rv.OriginHostLoc = loc
			rv.hasOriginHost = true
		case avpOriginRealm:
			rv.OriginRealm = string(body)
			rv.OriginRealmLoc = loc
			rv.hasOriginRealm = true
		case avpDestinationHost:
			rv.DestinationHost = string(body)
		case avpDestinationRealm:
			rv.DestinationRealm = string(body)
		}
		return nil
	})
	return rv, err
}

// rewriteOriginHostRealm returns a new message with Origin-Host / Origin-Realm
// AVPs replaced by DRA's own identity. It's a proxy rewrite — the standard
// DRA behavior so downstream peers see "the message came from DRA".
//
// Implementation note: because AVPs are length-prefixed with 4-byte padding,
// we rebuild the whole message rather than patching in place.
func rewriteOriginHostRealm(msg []byte, newHost, newRealm string) ([]byte, error) {
	if len(msg) < headerLen {
		return nil, errors.New("message too short")
	}
	out := make([]byte, 0, len(msg)+32)
	out = append(out, msg[:headerLen]...) // header placeholder; length fixed below
	wroteHost := false
	wroteRealm := false
	err := iterateAVPs(msg, func(code, vendor uint32, loc avpLoc, body []byte) error {
		if vendor == 0 && code == avpOriginHost {
			out = append(out, encodeStringAVP(avpOriginHost, newHost, true)...)
			wroteHost = true
			return nil
		}
		if vendor == 0 && code == avpOriginRealm {
			out = append(out, encodeStringAVP(avpOriginRealm, newRealm, true)...)
			wroteRealm = true
			return nil
		}
		// copy AVP verbatim (including padding)
		out = append(out, msg[loc.start:loc.start+loc.paddedTo]...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	// If the message didn't have them at all (shouldn't happen for proper
	// Diameter but be defensive), append them.
	if !wroteHost {
		out = append(out, encodeStringAVP(avpOriginHost, newHost, true)...)
	}
	if !wroteRealm {
		out = append(out, encodeStringAVP(avpOriginRealm, newRealm, true)...)
	}
	// Fix message length (3-byte field, big-endian, at bytes [1:4]).
	total := uint32(len(out))
	out[1] = byte(total >> 16)
	out[2] = byte(total >> 8)
	out[3] = byte(total)
	return out, nil
}

// encodeStringAVP builds an AVP for a DiameterIdentity / UTF8String / OctetString
// payload (all are just bytes on the wire). mandatory sets the M-bit.
// Output is padded to a 4-byte boundary.
func encodeStringAVP(code uint32, value string, mandatory bool) []byte {
	payload := []byte(value)
	total := 8 + len(payload)
	padded := total
	if padded%4 != 0 {
		padded += 4 - (padded % 4)
	}
	b := make([]byte, padded)
	binary.BigEndian.PutUint32(b[0:4], code)
	var flags byte
	if mandatory {
		flags |= 0x40
	}
	b[4] = flags
	b[5] = byte(total >> 16)
	b[6] = byte(total >> 8)
	b[7] = byte(total)
	copy(b[8:], payload)
	return b
}
