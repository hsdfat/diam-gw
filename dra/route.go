package dra

import (
	"sync"
	"sync/atomic"
)

// pendingEntry remembers what we need to answer a request once its answer
// comes back from the other side.
type pendingEntry struct {
	origPeer     *Peer  // peer that sent the original request
	origHbH      uint32 // HbH the original sender used
	origEndToEnd uint32 // preserved, but answers carry it verbatim anyway
	cmdCode      uint32
}

// pendingTable maps DRA-side (rewritten) HbH -> original context.
// DRA rewrites HbH on forward because two independent upstream peers might
// reuse the same HbH id on their own sockets — the server side must see a
// globally unique id across the DRA's outbound connections.
type pendingTable struct {
	mu sync.Mutex
	m  map[uint32]pendingEntry
}

func newPendingTable() *pendingTable {
	return &pendingTable{m: make(map[uint32]pendingEntry)}
}

func (t *pendingTable) put(hbh uint32, e pendingEntry) {
	t.mu.Lock()
	t.m[hbh] = e
	t.mu.Unlock()
}

func (t *pendingTable) take(hbh uint32) (pendingEntry, bool) {
	t.mu.Lock()
	e, ok := t.m[hbh]
	if ok {
		delete(t.m, hbh)
	}
	t.mu.Unlock()
	return e, ok
}

// forgetPeer drops all pending entries waiting on a peer that just died.
// (Their upstream senders will time out; that's fine.)
func (t *pendingTable) forgetPeer(p *Peer) {
	t.mu.Lock()
	for k, e := range t.m {
		if e.origPeer == p {
			delete(t.m, k)
		}
	}
	t.mu.Unlock()
}

// --- realm selection ---

// rrCounter gives us a lock-free round-robin step.
var rrCounter uint64

// pickPeer chooses a destination peer for a forwarded request.
// Preference order:
//  1. If Destination-Host is set and we have that peer, use it.
//  2. Otherwise round-robin among peers in Destination-Realm.
//  3. Never return the peer the request came in on (no hairpin).
func (d *DRANode) pickPeer(rv routingView, from *Peer) *Peer {
	if rv.DestinationHost != "" {
		if p := d.peerByHost(rv.DestinationHost); p != nil && p != from {
			return p
		}
	}
	candidates := d.peersByRealm(rv.DestinationRealm)
	// filter out the sender
	filtered := candidates[:0]
	for _, p := range candidates {
		if p != from {
			filtered = append(filtered, p)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	i := atomic.AddUint64(&rrCounter, 1)
	return filtered[int(i%uint64(len(filtered)))]
}
