// Package portpool provides a bounded pool of TCP source ports bound to a
// fixed source IP address. It is used by the Diameter gateway to satisfy
// the operator requirement that all outbound connections to DRA peers MUST
// use the operator-assigned client IP and a port from a pre-allocated
// range — never an OS-chosen ephemeral port.
//
// The pool guarantees:
//
//   - Every Acquire() returns a port from [minPort, maxPort] that no other
//     active caller currently holds.
//   - Acquire() returns ErrPoolExhausted when every port in the range is
//     either in use or in TIME_WAIT quarantine; the dialer is expected to
//     surface this error rather than fall back to an ephemeral port.
//   - Release() always returns the port to the pool, even when called from
//     deferred error paths, so dial failures cannot leak ports.
//   - Optional TIME_WAIT quarantine: a released port is held off the free
//     list for QuarantineDuration before becoming acquirable again. This
//     prevents EADDRINUSE races on rapid reconnect to the same destination
//     when the kernel has not yet purged the previous 4-tuple.
//
// All operations are safe for concurrent use.
package portpool

import (
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// ErrPoolExhausted is returned by Acquire when no free port is available.
// Callers MUST treat this as a hard failure; per the operator policy, the
// dialer is forbidden from falling back to a kernel-assigned ephemeral port.
var ErrPoolExhausted = errors.New("portpool: no free port available")

// Stats is a snapshot of the pool's runtime state.
type Stats struct {
	SourceIP          net.IP
	MinPort           int
	MaxPort           int
	Total             int    // = MaxPort-MinPort+1
	InUse             int    // currently held by Acquire callers
	Quarantined       int    // released but inside TIME_WAIT grace window
	Free              int    // immediately available
	AllocationFailures uint64 // monotonic count of ErrPoolExhausted returns
}

// Config configures a Pool.
type Config struct {
	// SourceIP is the local IP that all ports in this pool are bound to.
	// It must be a non-nil, valid IP that exists on a local interface; the
	// pool itself does not verify the latter, but DialContext will fail
	// with EADDRNOTAVAIL if it does not.
	SourceIP net.IP

	// MinPort and MaxPort define the inclusive port range. Both must be in
	// [1, 65535] and MinPort <= MaxPort.
	MinPort int
	MaxPort int

	// QuarantineDuration is how long a released port is held before it
	// becomes acquirable again. Zero disables quarantine (release goes
	// straight back to the free list). A typical Linux TIME_WAIT is 60s,
	// so 60–90s is a safe value when SO_REUSEADDR is not in use.
	QuarantineDuration time.Duration

	// Now is an injectable clock for tests. Production code leaves this
	// nil; the pool uses time.Now.
	Now func() time.Time
}

// Pool is a bounded set of source ports backed by a fixed source IP.
// The zero value is not usable; construct one with New.
type Pool struct {
	cfg Config

	mu         sync.Mutex
	free       []int             // FIFO of immediately-acquirable ports
	inUse      map[int]string    // port -> owner id (for diagnostics)
	quarantine map[int]time.Time // port -> earliest re-acquire time

	failures atomic.Uint64
}

// New constructs a Pool from cfg. It returns an error if the configuration
// is invalid; otherwise the returned pool starts with every port in the
// range available.
func New(cfg Config) (*Pool, error) {
	if cfg.SourceIP == nil {
		return nil, fmt.Errorf("portpool: SourceIP is required")
	}
	if cfg.MinPort < 1 || cfg.MinPort > 65535 {
		return nil, fmt.Errorf("portpool: MinPort %d out of range [1,65535]", cfg.MinPort)
	}
	if cfg.MaxPort < 1 || cfg.MaxPort > 65535 {
		return nil, fmt.Errorf("portpool: MaxPort %d out of range [1,65535]", cfg.MaxPort)
	}
	if cfg.MinPort > cfg.MaxPort {
		return nil, fmt.Errorf("portpool: MinPort %d > MaxPort %d", cfg.MinPort, cfg.MaxPort)
	}
	if cfg.QuarantineDuration < 0 {
		return nil, fmt.Errorf("portpool: QuarantineDuration must be >= 0")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	total := cfg.MaxPort - cfg.MinPort + 1
	p := &Pool{
		cfg:        cfg,
		free:       make([]int, 0, total),
		inUse:      make(map[int]string, total),
		quarantine: make(map[int]time.Time),
	}
	for port := cfg.MinPort; port <= cfg.MaxPort; port++ {
		p.free = append(p.free, port)
	}
	return p, nil
}

// Acquire returns the next available port and records owner against it.
// owner is for diagnostics only — it shows up in InUseOwners() and is
// useful when chasing port leaks. Pass the connection id.
//
// Returns ErrPoolExhausted if every port is either in use or quarantined.
// Callers MUST NOT retry inside a tight loop on this error; instead, the
// dialer should surface it so the reconnect backoff handles the retry.
func (p *Pool) Acquire(owner string) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.sweepQuarantineLocked()

	if len(p.free) == 0 {
		p.failures.Add(1)
		return 0, ErrPoolExhausted
	}

	// Pop from the front (FIFO). FIFO ordering means a recently-released
	// port goes to the back of the line, which gives the kernel maximum
	// time to clear any TIME_WAIT residue even when QuarantineDuration is
	// 0 — useful as a defence-in-depth.
	port := p.free[0]
	p.free = p.free[1:]
	p.inUse[port] = owner

	return port, nil
}

// Release returns a port to the pool. It is safe — and a no-op — to call
// Release on a port that was never acquired or has already been released;
// this matches the way callers use it from `defer` after dial errors.
//
// If QuarantineDuration > 0 the port is parked in quarantine and will not
// be re-acquirable until the grace period elapses.
func (p *Pool) Release(port int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, ok := p.inUse[port]; !ok {
		// Either never acquired, already released, or out of range.
		// Treat as a no-op so defer'd cleanup is always safe.
		return
	}
	delete(p.inUse, port)

	if p.cfg.QuarantineDuration > 0 {
		p.quarantine[port] = p.cfg.Now().Add(p.cfg.QuarantineDuration)
		return
	}
	p.free = append(p.free, port)
}

// Contains reports whether port is within the pool's configured range.
func (p *Pool) Contains(port int) bool {
	return port >= p.cfg.MinPort && port <= p.cfg.MaxPort
}

// Stats returns a point-in-time snapshot of pool state.
func (p *Pool) Stats() Stats {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.sweepQuarantineLocked()

	total := p.cfg.MaxPort - p.cfg.MinPort + 1
	inUse := len(p.inUse)
	q := len(p.quarantine)
	free := total - inUse - q
	if free < 0 {
		free = 0
	}
	return Stats{
		SourceIP:           p.cfg.SourceIP,
		MinPort:            p.cfg.MinPort,
		MaxPort:            p.cfg.MaxPort,
		Total:              total,
		InUse:              inUse,
		Quarantined:        q,
		Free:               free,
		AllocationFailures: p.failures.Load(),
	}
}

// InUseOwners returns a copy of the (port -> owner) map. Useful for debug
// endpoints when chasing a leak. Do not call from a hot path.
func (p *Pool) InUseOwners() map[int]string {
	p.mu.Lock()
	defer p.mu.Unlock()

	out := make(map[int]string, len(p.inUse))
	for k, v := range p.inUse {
		out[k] = v
	}
	return out
}

// LocalTCPAddr is a convenience helper that builds a *net.TCPAddr suitable
// for net.Dialer.LocalAddr from a previously Acquired port.
func (p *Pool) LocalTCPAddr(port int) *net.TCPAddr {
	return &net.TCPAddr{IP: p.cfg.SourceIP, Port: port}
}

// sweepQuarantineLocked moves any quarantined ports whose grace window has
// expired back to the free list. Callers must hold p.mu.
func (p *Pool) sweepQuarantineLocked() {
	if len(p.quarantine) == 0 {
		return
	}
	now := p.cfg.Now()
	for port, until := range p.quarantine {
		if !now.Before(until) {
			delete(p.quarantine, port)
			p.free = append(p.free, port)
		}
	}
}
