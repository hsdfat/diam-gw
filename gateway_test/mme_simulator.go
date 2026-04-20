package gateway_test

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hsdfat/diam-gw/commands/base"
	"github.com/hsdfat/diam-gw/commands/s13"
	"github.com/hsdfat/diam-gw/commands/s6a"
	"github.com/hsdfat/diam-gw/models_base"
	"github.com/hsdfat/diam-gw/pkg/logger"
)

// MMESimulator is a minimal MME Diameter peer that connects to a DRA and can
// send AIR (S6a) / MICR (S13) requests, correlating answers by Hop-by-Hop ID.
// It's intentionally raw TCP (no reuse of client.Connection) so the test has
// full control over the peer state machine and isn't coupled to the client
// package under test.
type MMESimulator struct {
	originHost  string
	originRealm string
	draAddr     string

	conn    net.Conn
	connMu  sync.Mutex
	writeMu sync.Mutex // serializes frame writes so concurrent SendAIR/SendMICR don't interleave bytes

	ctx    context.Context
	cancel context.CancelFunc

	hbh atomic.Uint32
	e2e atomic.Uint32

	// pending maps our outbound HbH -> channel that receives the raw answer
	pendingMu sync.Mutex
	pending   map[uint32]chan []byte

	// stats for asserting counts
	sentRequests     atomic.Uint64
	receivedAnswers  atomic.Uint64

	log logger.Logger
}

// NewMMESimulator builds (but does not connect) an MME simulator.
// originHost / originRealm are what the DRA will see in CER; the DRA uses
// Origin-Host to identify this peer for later targeted routing.
func NewMMESimulator(ctx context.Context, draAddr, originHost, originRealm string, log logger.Logger) *MMESimulator {
	ctx, cancel := context.WithCancel(ctx)
	// Seed HbH/E2E with the low 16 bits of a timestamp so parallel MME
	// instances inside the same test don't collide on the DRA's pending
	// table (DRA rewrites HbH on forward, but starting offsets still help
	// reading logs).
	seed := uint32(time.Now().UnixNano() & 0xFFFF)
	m := &MMESimulator{
		originHost:  originHost,
		originRealm: originRealm,
		draAddr:     draAddr,
		ctx:         ctx,
		cancel:      cancel,
		pending:     make(map[uint32]chan []byte),
		log:         log,
	}
	m.hbh.Store(seed)
	m.e2e.Store(seed)
	return m
}

// Start connects to the DRA, performs CER/CEA, and starts the read loop.
// Blocks until handshake completes or fails.
func (m *MMESimulator) Start() error {
	conn, err := net.DialTimeout("tcp", m.draAddr, 3*time.Second)
	if err != nil {
		return fmt.Errorf("dial DRA %s: %w", m.draAddr, err)
	}
	m.connMu.Lock()
	m.conn = conn
	m.connMu.Unlock()

	if err := m.sendCER(); err != nil {
		_ = conn.Close()
		return fmt.Errorf("CER: %w", err)
	}
	// Read CEA inline (before starting the async reader) so we can surface
	// handshake errors synchronously.
	cea, err := readFramed(conn, 3*time.Second)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("read CEA: %w", err)
	}
	hdr, err := parseHdr(cea)
	if err != nil || hdr.cmdCode != 257 {
		_ = conn.Close()
		return fmt.Errorf("expected CEA, got cmd=%d", hdr.cmdCode)
	}

	go m.readLoop()
	m.log.Infow("MME connected to DRA", "origin_host", m.originHost, "dra", m.draAddr)
	return nil
}

// Stop closes the DRA connection.
func (m *MMESimulator) Stop() {
	m.cancel()
	m.connMu.Lock()
	if m.conn != nil {
		_ = m.conn.Close()
		m.conn = nil
	}
	m.connMu.Unlock()
}

// SendAIR builds and sends an AIR (Authentication-Information-Request) for
// the given IMSI, targeting destRealm (+ optional destHost for sticky
// routing). Blocks until AIA arrives or timeout.
func (m *MMESimulator) SendAIR(imsi, destRealm, destHost string, timeout time.Duration) ([]byte, error) {
	hbh := m.hbh.Add(1)
	e2e := m.e2e.Add(1)

	air := s6a.NewAuthenticationInformationRequest()
	air.Header.HopByHopID = hbh
	air.Header.EndToEndID = e2e
	air.SessionId = models_base.UTF8String(fmt.Sprintf("%s;%d;%d", m.originHost, hbh, e2e))
	air.OriginHost = models_base.DiameterIdentity(m.originHost)
	air.OriginRealm = models_base.DiameterIdentity(m.originRealm)
	air.DestinationRealm = models_base.DiameterIdentity(destRealm)
	if destHost != "" {
		dh := models_base.DiameterIdentity(destHost)
		air.DestinationHost = &dh
	}
	air.AuthSessionState = models_base.Enumerated(1) // NO_STATE_MAINTAINED
	air.UserName = models_base.UTF8String(imsi)
	air.VisitedPlmnId = models_base.OctetString([]byte{0x00, 0xF1, 0x10}) // arbitrary

	data, err := air.Marshal()
	if err != nil {
		return nil, fmt.Errorf("marshal AIR: %w", err)
	}
	return m.sendAndWait(hbh, data, timeout)
}

// SendMICR builds and sends an MICR (ME-Identity-Check-Request) for the given
// IMEI, targeting destRealm (+ optional destHost). Blocks until MICA arrives
// or timeout.
func (m *MMESimulator) SendMICR(imei, destRealm, destHost string, timeout time.Duration) ([]byte, error) {
	hbh := m.hbh.Add(1)
	e2e := m.e2e.Add(1)

	micr := s13.NewMEIdentityCheckRequest()
	micr.Header.HopByHopID = hbh
	micr.Header.EndToEndID = e2e
	micr.SessionId = models_base.UTF8String(fmt.Sprintf("%s;%d;%d", m.originHost, hbh, e2e))
	micr.OriginHost = models_base.DiameterIdentity(m.originHost)
	micr.OriginRealm = models_base.DiameterIdentity(m.originRealm)
	micr.DestinationRealm = models_base.DiameterIdentity(destRealm)
	if destHost != "" {
		dh := models_base.DiameterIdentity(destHost)
		micr.DestinationHost = &dh
	}
	micr.AuthSessionState = models_base.Enumerated(1)
	un := models_base.UTF8String(imei)
	micr.UserName = &un
	imeiStr := models_base.UTF8String(imei)
	micr.TerminalInformation = &s13.TerminalInformation{
		Imei: &imeiStr,
	}

	data, err := micr.Marshal()
	if err != nil {
		return nil, fmt.Errorf("marshal MICR: %w", err)
	}
	return m.sendAndWait(hbh, data, timeout)
}

// Stats returns counters for asserting in tests.
func (m *MMESimulator) Stats() (sent, received uint64) {
	return m.sentRequests.Load(), m.receivedAnswers.Load()
}

// --- internals ---

func (m *MMESimulator) sendCER() error {
	cer := base.NewCapabilitiesExchangeRequest()
	cer.Header.HopByHopID = m.hbh.Add(1)
	cer.Header.EndToEndID = m.e2e.Add(1)
	cer.OriginHost = models_base.DiameterIdentity(m.originHost)
	cer.OriginRealm = models_base.DiameterIdentity(m.originRealm)
	cer.ProductName = models_base.UTF8String("mme-sim")
	cer.VendorId = models_base.Unsigned32(10415)

	if la, ok := m.conn.LocalAddr().(*net.TCPAddr); ok {
		cer.HostIpAddress = []models_base.Address{models_base.Address(la.IP)}
	}
	cer.AuthApplicationId = []models_base.Unsigned32{
		models_base.Unsigned32(s6a.S6A_APPLICATION_ID),
		models_base.Unsigned32(s13.S13_APPLICATION_ID),
	}

	b, err := cer.Marshal()
	if err != nil {
		return fmt.Errorf("marshal CER: %w", err)
	}
	return writeAll(m.conn, b, 2*time.Second)
}

func (m *MMESimulator) sendAndWait(hbh uint32, data []byte, timeout time.Duration) ([]byte, error) {
	ch := make(chan []byte, 1)
	m.pendingMu.Lock()
	m.pending[hbh] = ch
	m.pendingMu.Unlock()
	defer func() {
		m.pendingMu.Lock()
		delete(m.pending, hbh)
		m.pendingMu.Unlock()
	}()

	m.connMu.Lock()
	conn := m.conn
	m.connMu.Unlock()
	if conn == nil {
		return nil, errors.New("MME not connected")
	}
	m.writeMu.Lock()
	err := writeAll(conn, data, 2*time.Second)
	m.writeMu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}
	m.sentRequests.Add(1)

	select {
	case ans := <-ch:
		m.receivedAnswers.Add(1)
		return ans, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("timeout waiting for answer to HbH=%d", hbh)
	case <-m.ctx.Done():
		return nil, m.ctx.Err()
	}
}

func (m *MMESimulator) readLoop() {
	for {
		m.connMu.Lock()
		conn := m.conn
		m.connMu.Unlock()
		if conn == nil {
			return
		}
		msg, err := readFramed(conn, 0)
		if err != nil {
			if m.ctx.Err() == nil {
				m.log.Debugw("MME read loop ended", "err", err)
			}
			return
		}
		hdr, err := parseHdr(msg)
		if err != nil {
			continue
		}
		// DWR from DRA — reply with DWA so our peering stays alive.
		if hdr.cmdCode == 280 && hdr.isRequest {
			m.replyDWA(msg)
			continue
		}
		// Answers: route to the waiter by HbH. DRA's answer-forwarding
		// restores the ORIGINAL (our) HbH, so this direct lookup works.
		if !hdr.isRequest {
			m.pendingMu.Lock()
			ch, ok := m.pending[hdr.hbh]
			m.pendingMu.Unlock()
			if ok {
				select {
				case ch <- msg:
				default:
				}
			}
		}
	}
}

func (m *MMESimulator) replyDWA(reqBytes []byte) {
	dwr := base.NewDeviceWatchdogRequest()
	if err := dwr.Unmarshal(reqBytes); err != nil {
		return
	}
	dwa := base.NewDeviceWatchdogAnswer()
	dwa.Header.HopByHopID = dwr.Header.HopByHopID
	dwa.Header.EndToEndID = dwr.Header.EndToEndID
	dwa.ResultCode = models_base.Unsigned32(2001)
	dwa.OriginHost = models_base.DiameterIdentity(m.originHost)
	dwa.OriginRealm = models_base.DiameterIdentity(m.originRealm)
	b, err := dwa.Marshal()
	if err != nil {
		return
	}
	m.connMu.Lock()
	c := m.conn
	m.connMu.Unlock()
	if c != nil {
		m.writeMu.Lock()
		_ = writeAll(c, b, 2*time.Second)
		m.writeMu.Unlock()
	}
}

// --- tiny framing helpers shared by the simulators ---

type shallowHdr struct {
	length    uint32
	flags     byte
	cmdCode   uint32
	appID     uint32
	hbh       uint32
	e2e       uint32
	isRequest bool
}

func parseHdr(b []byte) (shallowHdr, error) {
	if len(b) < 20 {
		return shallowHdr{}, errors.New("short header")
	}
	return shallowHdr{
		length:    uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3]),
		flags:     b[4],
		cmdCode:   uint32(b[5])<<16 | uint32(b[6])<<8 | uint32(b[7]),
		appID:     binary.BigEndian.Uint32(b[8:12]),
		hbh:       binary.BigEndian.Uint32(b[12:16]),
		e2e:       binary.BigEndian.Uint32(b[16:20]),
		isRequest: b[4]&0x80 != 0,
	}, nil
}

// readFramed reads one complete Diameter message (4-byte prefix + body).
// deadline=0 means no deadline.
func readFramed(conn net.Conn, deadline time.Duration) ([]byte, error) {
	if deadline > 0 {
		_ = conn.SetReadDeadline(time.Now().Add(deadline))
		defer conn.SetReadDeadline(time.Time{})
	}
	var prefix [4]byte
	if _, err := io.ReadFull(conn, prefix[:]); err != nil {
		return nil, err
	}
	total := uint32(prefix[1])<<16 | uint32(prefix[2])<<8 | uint32(prefix[3])
	if total < 20 || total > 1<<20 {
		return nil, errors.New("implausible message length")
	}
	msg := make([]byte, total)
	copy(msg[:4], prefix[:])
	if _, err := io.ReadFull(conn, msg[4:]); err != nil {
		return nil, err
	}
	return msg, nil
}

func writeAll(conn net.Conn, b []byte, deadline time.Duration) error {
	if deadline > 0 {
		_ = conn.SetWriteDeadline(time.Now().Add(deadline))
		defer conn.SetWriteDeadline(time.Time{})
	}
	_, err := conn.Write(b)
	return err
}
