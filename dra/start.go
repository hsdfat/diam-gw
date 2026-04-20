package dra

import (
	"errors"
	"fmt"
	"net"

	"github.com/hsdfat/diam-gw/commands/base"
	"github.com/hsdfat/diam-gw/models_base"
)

// StartNode starts listening and accepting Diameter peers. It blocks until
// the listener fails or Stop() is called.
func (d *DRANode) StartNode() error {
	if d.cfg.ListenAddr == "" {
		return errors.New("dra: ListenAddr is empty")
	}
	if d.cfg.OriginHost == "" || d.cfg.OriginRealm == "" {
		return errors.New("dra: OriginHost and OriginRealm are required")
	}
	ln, err := net.Listen("tcp", d.cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("dra: listen %s: %w", d.cfg.ListenAddr, err)
	}
	d.listener = ln
	d.log(fmt.Sprintf("[%s] listening on %s", d.cfg.NodeName, d.cfg.ListenAddr))

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-d.ctx.Done():
				return nil
			default:
			}
			return fmt.Errorf("dra: accept: %w", err)
		}
		go d.handleConnection(conn)
	}
}

// handleConnection runs one peer from the very first frame (expected CER)
// through normal traffic until the socket closes.
func (d *DRANode) handleConnection(conn net.Conn) {
	peer := newPeer(conn)
	defer func() {
		peer.Close()
		d.pending.forgetPeer(peer)
		d.removePeer(peer)
		d.log(fmt.Sprintf("peer %s disconnected", peer.OriginHost))
	}()

	// --- capabilities exchange (terminated by DRA) ---
	firstMsg, err := readMessage(conn)
	if err != nil {
		d.log(fmt.Sprintf("read initial frame: %v", err))
		return
	}
	hdr, err := parseHeader(firstMsg)
	if err != nil || hdr.CommandCode != 257 || !hdr.isRequest() {
		d.log("first message was not CER; dropping")
		return
	}
	if err := d.handleCER(peer, firstMsg); err != nil {
		d.log(fmt.Sprintf("CER handling failed: %v", err))
		return
	}
	d.registerPeer(peer)
	d.log(fmt.Sprintf("peer up: host=%s realm=%s", peer.OriginHost, peer.OriginRealm))

	// --- main loop ---
	for {
		msg, err := readMessage(conn)
		if err != nil {
			return
		}
		h, err := parseHeader(msg)
		if err != nil {
			d.log(fmt.Sprintf("bad header from %s: %v", peer.OriginHost, err))
			return
		}
		switch h.CommandCode {
		case 280: // DWR / DWA — keepalive, terminated by DRA
			if h.isRequest() {
				if err := d.handleDWR(peer, msg); err != nil {
					d.log(fmt.Sprintf("DWA send failed: %v", err))
					return
				}
			}
			// DWA from remote: ignored (we don't (yet) originate DWR)
		case 282: // DPR — peer wants to go away
			if h.isRequest() {
				// RFC 6733: answer DPA then close. We don't implement
				// DPA encoding here to stay minimal — just drop the socket.
				return
			}
		default:
			// Everything else = app traffic. Forward it.
			if h.isRequest() {
				d.forwardRequest(peer, msg, h)
			} else {
				d.forwardAnswer(peer, msg, h)
			}
		}
	}
}

// handleCER decodes the CER, records the peer's identity, and sends a CEA.
func (d *DRANode) handleCER(peer *Peer, msg []byte) error {
	cer := base.NewCapabilitiesExchangeRequest()
	if err := cer.Unmarshal(msg); err != nil {
		return fmt.Errorf("decode CER: %w", err)
	}
	peer.OriginHost = string(cer.OriginHost)
	peer.OriginRealm = string(cer.OriginRealm)

	cea := base.NewCapabilitiesExchangeAnswer()
	cea.Header.HopByHopID = cer.Header.HopByHopID
	cea.Header.EndToEndID = cer.Header.EndToEndID
	cea.ResultCode = models_base.Unsigned32(base.DIAMETER_SUCCESS)
	cea.OriginHost = models_base.DiameterIdentity(d.cfg.OriginHost)
	cea.OriginRealm = models_base.DiameterIdentity(d.cfg.OriginRealm)
	cea.VendorId = models_base.Unsigned32(d.cfg.VendorID)
	cea.ProductName = models_base.UTF8String(d.cfg.ProductName)
	for _, ip := range d.cfg.HostIPs {
		cea.HostIpAddress = append(cea.HostIpAddress, models_base.Address(ip))
	}
	// Echo the peer's advertised auth apps so it knows we'll relay them.
	for _, app := range d.cfg.SupportedApps {
		cea.AuthApplicationId = append(cea.AuthApplicationId, models_base.Unsigned32(app))
	}
	out, err := cea.Marshal()
	if err != nil {
		return fmt.Errorf("encode CEA: %w", err)
	}
	return peer.Send(out)
}

// handleDWR replies with a DWA so the peer's watchdog stays happy.
func (d *DRANode) handleDWR(peer *Peer, msg []byte) error {
	dwr := base.NewDeviceWatchdogRequest()
	if err := dwr.Unmarshal(msg); err != nil {
		return fmt.Errorf("decode DWR: %w", err)
	}
	dwa := base.NewDeviceWatchdogAnswer()
	dwa.Header.HopByHopID = dwr.Header.HopByHopID
	dwa.Header.EndToEndID = dwr.Header.EndToEndID
	dwa.ResultCode = models_base.Unsigned32(base.DIAMETER_SUCCESS)
	dwa.OriginHost = models_base.DiameterIdentity(d.cfg.OriginHost)
	dwa.OriginRealm = models_base.DiameterIdentity(d.cfg.OriginRealm)
	out, err := dwa.Marshal()
	if err != nil {
		return err
	}
	return peer.Send(out)
}

// forwardRequest: peek routing AVPs, choose destination peer, rewrite
// Origin-Host/Realm to DRA's identity, rewrite HbH so answers come back
// to us, remember the mapping, and send.
func (d *DRANode) forwardRequest(from *Peer, msg []byte, h parsedHeader) {
	rv, err := extractRouting(msg)
	if err != nil {
		d.log(fmt.Sprintf("forward: parse AVPs: %v", err))
		d.sendProtocolError(from, h, base.DIAMETER_INVALID_AVP_VALUE)
		return
	}
	if rv.DestinationRealm == "" {
		d.log("forward: missing Destination-Realm")
		d.sendProtocolError(from, h, base.DIAMETER_REALM_NOT_SERVED)
		return
	}
	to := d.pickPeer(rv, from)
	if to == nil {
		d.log(fmt.Sprintf("forward: no peer for realm=%q host=%q",
			rv.DestinationRealm, rv.DestinationHost))
		d.sendProtocolError(from, h, base.DIAMETER_UNABLE_TO_DELIVER)
		return
	}
	// Rewrite Origin-Host / Origin-Realm to DRA's identity (proxy behavior).
	rewritten, err := rewriteOriginHostRealm(msg, d.cfg.OriginHost, d.cfg.OriginRealm)
	if err != nil {
		d.log(fmt.Sprintf("forward: rewrite origin: %v", err))
		d.sendProtocolError(from, h, base.DIAMETER_UNABLE_TO_COMPLY)
		return
	}
	// Assign a new HbH so multiple senders can't collide on our outbound
	// side, and remember the original so we can restore it on the answer.
	newHbH := d.nextHbH()
	setHopByHop(rewritten, newHbH)
	d.pending.put(newHbH, pendingEntry{
		origPeer:     from,
		origHbH:      h.HopByHopID,
		origEndToEnd: h.EndToEndID,
		cmdCode:      h.CommandCode,
	})
	if err := to.Send(rewritten); err != nil {
		d.log(fmt.Sprintf("forward: send to %s failed: %v", to.OriginHost, err))
		d.pending.take(newHbH)
		d.sendProtocolError(from, h, base.DIAMETER_UNABLE_TO_DELIVER)
		return
	}
	d.log(fmt.Sprintf("forward REQ cmd=%d %s -> %s (realm=%s)",
		h.CommandCode, from.OriginHost, to.OriginHost, rv.DestinationRealm))
}

// forwardAnswer: look up the pending entry by the HbH the server sent back
// (which is the one DRA assigned), restore the original HbH, rewrite
// Origin-Host/Realm to DRA, and send back to the original requester.
func (d *DRANode) forwardAnswer(from *Peer, msg []byte, h parsedHeader) {
	entry, ok := d.pending.take(h.HopByHopID)
	if !ok {
		d.log(fmt.Sprintf("forward ANS: no pending entry for HbH=%d from %s",
			h.HopByHopID, from.OriginHost))
		return
	}
	rewritten, err := rewriteOriginHostRealm(msg, d.cfg.OriginHost, d.cfg.OriginRealm)
	if err != nil {
		d.log(fmt.Sprintf("forward ANS: rewrite origin: %v", err))
		return
	}
	setHopByHop(rewritten, entry.origHbH)
	if err := entry.origPeer.Send(rewritten); err != nil {
		d.log(fmt.Sprintf("forward ANS: send to %s failed: %v",
			entry.origPeer.OriginHost, err))
		return
	}
	d.log(fmt.Sprintf("forward ANS cmd=%d %s -> %s",
		h.CommandCode, from.OriginHost, entry.origPeer.OriginHost))
}

// sendProtocolError returns a minimally-framed error answer to the sender.
// We build it by hand: flip R->0, set E=1, keep HbH/E2E, emit Result-Code +
// Origin-Host + Origin-Realm. Good enough to unstick the sender's state
// machine without pulling in every S6a/S13 answer type.
func (d *DRANode) sendProtocolError(to *Peer, req parsedHeader, resultCode uint32) {
	avps := make([]byte, 0, 64)
	// Result-Code (AVP 268, Unsigned32)
	rcAvp := make([]byte, 12)
	// code
	rcAvp[0] = 0
	rcAvp[1] = 0
	rcAvp[2] = 1
	rcAvp[3] = 12
	// flags M-bit
	rcAvp[4] = 0x40
	// length = 12
	rcAvp[5] = 0
	rcAvp[6] = 0
	rcAvp[7] = 12
	// value
	rcAvp[8] = byte(resultCode >> 24)
	rcAvp[9] = byte(resultCode >> 16)
	rcAvp[10] = byte(resultCode >> 8)
	rcAvp[11] = byte(resultCode)
	avps = append(avps, rcAvp...)
	avps = append(avps, encodeStringAVP(avpOriginHost, d.cfg.OriginHost, true)...)
	avps = append(avps, encodeStringAVP(avpOriginRealm, d.cfg.OriginRealm, true)...)

	total := uint32(headerLen + len(avps))
	out := make([]byte, headerLen, total)
	out[0] = 1 // version
	out[1] = byte(total >> 16)
	out[2] = byte(total >> 8)
	out[3] = byte(total)
	// flags: R=0, P preserve, E=1, T=0
	out[4] = (req.Flags & 0x40) | 0x20
	out[5] = byte(req.CommandCode >> 16)
	out[6] = byte(req.CommandCode >> 8)
	out[7] = byte(req.CommandCode)
	// app id
	out[8] = byte(req.ApplicationID >> 24)
	out[9] = byte(req.ApplicationID >> 16)
	out[10] = byte(req.ApplicationID >> 8)
	out[11] = byte(req.ApplicationID)
	// HbH, E2E — copy from the original request
	out[12] = byte(req.HopByHopID >> 24)
	out[13] = byte(req.HopByHopID >> 16)
	out[14] = byte(req.HopByHopID >> 8)
	out[15] = byte(req.HopByHopID)
	out[16] = byte(req.EndToEndID >> 24)
	out[17] = byte(req.EndToEndID >> 16)
	out[18] = byte(req.EndToEndID >> 8)
	out[19] = byte(req.EndToEndID)
	out = append(out, avps...)
	_ = to.Send(out)
}
