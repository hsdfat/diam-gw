// Package dra implements a minimal Diameter Routing Agent.
//
// DRA only uses commands/base for full encode/decode of CER/CEA and DWR/DWA
// (the messages it terminates as a peer). All other traffic (S6a ULR/AIR,
// CLR/IDR, etc.) is forwarded with only a shallow header/AVP peek: we look
// up Destination-Realm (and optional Destination-Host), pick a peer, rewrite
// the Hop-by-Hop id so answers can be correlated back, and forward.
package dra

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
)

// Node is the generic node contract in this codebase.
// Only StartNode is implemented here per request; the others are stubs.
type Node interface {
	GetNodeName() string
	GetLogs() string
	StartNode() error
}

// Config is the minimal config a DRA needs.
type Config struct {
	NodeName    string   // human-readable
	ListenAddr  string   // e.g. "0.0.0.0:3868"
	OriginHost  string   // DRA's own Origin-Host (DiameterIdentity)
	OriginRealm string   // DRA's own Origin-Realm
	ProductName string   // CER/CEA Product-Name
	VendorID    uint32   // IANA vendor id (0 is fine for a generic DRA)
	HostIPs     []net.IP // local IPs to advertise in CER/CEA
	// SupportedApps: Auth-Application-Id values DRA relays (e.g. 16777251 = S6a/S6d).
	SupportedApps []uint32
}

// DRANode is a Diameter Routing Agent. It implements Node.
type DRANode struct {
	cfg Config

	ctx    context.Context
	cancel context.CancelFunc

	listener net.Listener

	peersMu sync.RWMutex
	peers   map[string]*Peer // key: Origin-Host plus remote endpoint

	// pending indexes in-flight requests so answers can be routed back
	// to the peer that sent the original request (by rewritten HbH id).
	pending *pendingTable

	// monotonic generator for DRA-side Hop-by-Hop rewrites
	hbhSeq uint32

	// tiny ring-buffer of log lines for GetLogs()
	logsMu sync.Mutex
	logs   []string
}

// NewDRANode constructs a DRANode from Config.
func NewDRANode(cfg Config) *DRANode {
	ctx, cancel := context.WithCancel(context.Background())
	return &DRANode{
		cfg:     cfg,
		ctx:     ctx,
		cancel:  cancel,
		peers:   make(map[string]*Peer),
		pending: newPendingTable(),
	}
}

// GetNodeName returns the configured node name. (stub per requirements)
func (d *DRANode) GetNodeName() string { return d.cfg.NodeName }

// GetLogs returns a snapshot of recent log lines. (stub per requirements)
func (d *DRANode) GetLogs() string {
	d.logsMu.Lock()
	defer d.logsMu.Unlock()
	out := ""
	for _, l := range d.logs {
		out += l + "\n"
	}
	return out
}

// nextHbH returns a fresh Hop-by-Hop id for rewrites.
func (d *DRANode) nextHbH() uint32 {
	return atomic.AddUint32(&d.hbhSeq, 1)
}

// log appends a line to the in-memory ring (bounded to 512 entries).
func (d *DRANode) log(line string) {
	d.logsMu.Lock()
	defer d.logsMu.Unlock()
	d.logs = append(d.logs, line)
	if len(d.logs) > 512 {
		d.logs = d.logs[len(d.logs)-512:]
	}
}

// Stop closes the listener and all peer connections.
func (d *DRANode) Stop() {
	d.cancel()
	if d.listener != nil {
		_ = d.listener.Close()
	}
	d.peersMu.Lock()
	for _, p := range d.peers {
		p.Close()
	}
	d.peers = map[string]*Peer{}
	d.peersMu.Unlock()
}
