package dra

import (
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

// PeerRole is inferred from configuration or from CER contents — DRA treats
// MMEs and HSSes symmetrically, but it's useful for logging/debugging.
type PeerRole int

const (
	RoleUnknown PeerRole = iota
	RoleClient           // generic "sent us a request" peer (e.g. MME)
	RoleServer           // generic "we forward requests to" peer (e.g. HSS)
)

// Peer represents one accepted TCP connection after CER/CEA has succeeded.
// "Origin-Host" and "Origin-Realm" are learned from that peer's CER.
type Peer struct {
	conn net.Conn

	OriginHost  string
	OriginRealm string
	Role        PeerRole

	// serializes writes; reads happen from a single goroutine so they
	// don't need a lock.
	writeMu sync.Mutex

	// closed guards double-close.
	closeOnce sync.Once
	closedCh  chan struct{}
}

func newPeer(conn net.Conn) *Peer {
	return &Peer{
		conn:     conn,
		closedCh: make(chan struct{}),
	}
}

// Send writes a full Diameter message to the peer. Thread-safe.
func (p *Peer) Send(msg []byte) error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	_ = p.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, err := p.conn.Write(msg)
	return err
}

// Close tears down the connection exactly once.
func (p *Peer) Close() {
	p.closeOnce.Do(func() {
		_ = p.conn.Close()
		close(p.closedCh)
	})
}

// readMessage reads exactly one framed Diameter message off the connection.
// Framing: 4-byte prefix (version + 3-byte length) then (length-4) more bytes.
func readMessage(conn net.Conn) ([]byte, error) {
	// We don't impose a read deadline here — the caller (the read loop)
	// sets it around idle-detection or watchdog if it wants.
	var prefix [4]byte
	if _, err := io.ReadFull(conn, prefix[:]); err != nil {
		return nil, err
	}
	if prefix[0] != 1 {
		return nil, errors.New("bad Diameter version")
	}
	total := uint32(prefix[1])<<16 | uint32(prefix[2])<<8 | uint32(prefix[3])
	if total < headerLen || total > 1<<20 {
		return nil, errors.New("implausible message length")
	}
	msg := make([]byte, total)
	copy(msg[:4], prefix[:])
	if _, err := io.ReadFull(conn, msg[4:]); err != nil {
		return nil, err
	}
	return msg, nil
}

// --- DRANode peer registry helpers ---

func (d *DRANode) registerPeer(p *Peer) {
	d.peersMu.Lock()
	defer d.peersMu.Unlock()
	if old, ok := d.peers[p.OriginHost]; ok && old != p {
		old.Close()
	}
	d.peers[p.OriginHost] = p
}

func (d *DRANode) removePeer(p *Peer) {
	d.peersMu.Lock()
	defer d.peersMu.Unlock()
	if cur, ok := d.peers[p.OriginHost]; ok && cur == p {
		delete(d.peers, p.OriginHost)
	}
}

func (d *DRANode) peerByHost(host string) *Peer {
	d.peersMu.RLock()
	defer d.peersMu.RUnlock()
	return d.peers[host]
}

// peersByRealm returns all peers whose Origin-Realm matches. Used for
// realm-based routing (the typical DRA forwarding case).
func (d *DRANode) peersByRealm(realm string) []*Peer {
	d.peersMu.RLock()
	defer d.peersMu.RUnlock()
	out := make([]*Peer, 0, 2)
	for _, p := range d.peers {
		if p.OriginRealm == realm {
			out = append(out, p)
		}
	}
	return out
}
