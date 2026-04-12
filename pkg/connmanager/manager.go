// Package connmanager owns the per-interface, source-bound TCP connections
// the diam-gw telco profile requires: exactly one client.Connection per
// (DRA, Diameter interface), each dialed from a port acquired from a
// shared portpool.Pool bound to the operator-assigned source IP.
//
// The package exists to be the boundary between three concerns that the
// telco specification keeps strictly separate:
//
//   - portpool.Pool: knows how to hand out source ports without collisions,
//     with TIME_WAIT quarantine. Knows nothing about Diameter.
//
//   - client.Connection: knows how to run one Diameter session — CER/CEA
//     handshake, DWR/DWA watchdog, write loop, reconnect with full-jitter
//     backoff. Knows nothing about port pools; takes a function hook
//     (DRAConfig.AcquireLocalAddr) to fetch a LocalAddr before each dial.
//
//   - Manager: dials one connection per (DRA, interface), wires the
//     portpool into each connection's hook, and pins each connection's
//     CER to advertise only its own interface's Auth-Application-Id —
//     no multiplexing. This is the strict per-socket separation the
//     operator requires (see project memory: CER per interface).
//
// The Manager does NOT replace client.DRAPool. Per the routing model
// decision (project memory: priority on top of 8 always-on connections),
// dra_pool's priority/failover logic continues to sit on top; Manager's
// job is only to guarantee the 8 underlying sockets exist, are bound to
// the right (IP, port) pair, and never give up reconnecting except on
// shutdown.
package connmanager

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/hsdfat/diam-gw/client"
	"github.com/hsdfat/diam-gw/commands/s13"
	"github.com/hsdfat/diam-gw/commands/s6a"
	"github.com/hsdfat/diam-gw/pkg/logger"
	"github.com/hsdfat/diam-gw/pkg/portpool"
)

// Interface identifies a Diameter application interface a connection is
// dedicated to. The telco profile uses two: S6a and S13. New interfaces
// can be added by extending DefaultInterfaces.
type Interface string

const (
	InterfaceS6a Interface = "s6a"
	InterfaceS13 Interface = "s13"
)

// InterfaceSpec maps an Interface to the Auth-Application-Id its sockets
// must advertise in CER. Each Manager slot uses exactly one of these.
type InterfaceSpec struct {
	Interface     Interface
	ApplicationID uint32
}

// DefaultInterfaces is the standard S6a + S13 pair the telco profile
// requires. Manager.Start dials one connection per DRA per entry here.
func DefaultInterfaces() []InterfaceSpec {
	return []InterfaceSpec{
		{Interface: InterfaceS6a, ApplicationID: s6a.S6A_APPLICATION_ID},
		{Interface: InterfaceS13, ApplicationID: s13.S13_APPLICATION_ID},
	}
}

// Key uniquely identifies one connection slot in the Manager.
type Key struct {
	DRA       string
	Interface Interface
}

// String renders a stable, log-friendly slot identifier such as "DRA-1/s6a".
func (k Key) String() string { return k.DRA + "/" + string(k.Interface) }

// DRASpec is one DRA peer the manager should keep a connection to.
// Priority/Weight live in client.DRAServerConfig and remain owned by
// dra_pool — Manager itself only needs the address.
type DRASpec struct {
	Name string
	Host string
	Port int
}

// Config configures a Manager. Most fields mirror DRAConfig because the
// per-slot DRAConfigs the manager builds inherit them; the additions are
// the source IP / port-pool range and the list of DRAs.
type Config struct {
	// SourceIP is the operator-assigned local IP every slot must dial
	// from. Required.
	SourceIP net.IP

	// MinPort, MaxPort define the inclusive port range the manager's
	// portpool draws from. Must be wide enough to cover at least one
	// port per slot plus headroom for quarantined ports during fast
	// reconnect — for 8 slots, 32+ ports is comfortable.
	MinPort int
	MaxPort int

	// QuarantineDuration is how long a released port is held before it
	// becomes acquirable again. Defaults to 60s when zero, matching the
	// typical Linux TIME_WAIT window.
	QuarantineDuration time.Duration

	// DRAs is the list of DRA peers. The Manager dials one connection
	// per DRA per entry in Interfaces, so 4 DRAs × 2 interfaces = 8
	// connections is the canonical telco shape.
	DRAs []DRASpec

	// Interfaces is the set of Diameter interfaces each DRA must have a
	// dedicated connection for. Defaults to DefaultInterfaces() when nil.
	Interfaces []InterfaceSpec

	// Diameter identity advertised in every CER.
	OriginHost  string
	OriginRealm string
	ProductName string
	VendorID    uint32

	// Per-connection timing & buffer settings — mirrored into each slot's
	// DRAConfig. Defaults are filled in by New when zero.
	ConnectTimeout    time.Duration
	CERTimeout        time.Duration
	DWRInterval       time.Duration
	DWRTimeout        time.Duration
	MaxDWRFailures    int
	ReconnectInterval time.Duration
	MaxReconnectDelay time.Duration
	ReconnectBackoff  float64
	SendBufferSize    int
	RecvBufferSize    int
}

// SlotInfo is a read-only view of one connection slot, returned by
// Manager.Slots() for diagnostics and tests. The DRAConfig is a pointer
// into the live slot — callers should treat it as read-only.
type SlotInfo struct {
	Key           Key
	ApplicationID uint32
	Connection    *client.Connection
	Config        *client.DRAConfig
}

// Manager owns one *portpool.Pool and one *client.Connection per
// (DRA, Interface) slot. It is safe for concurrent use after New returns.
type Manager struct {
	cfg    Config
	pool   *portpool.Pool
	logger logger.Logger

	ctx    context.Context
	cancel context.CancelFunc

	mu    sync.RWMutex
	slots map[Key]*slot
	keys  []Key // stable insertion order, for deterministic iteration
}

type slot struct {
	key  Key
	app  uint32
	cfg  *client.DRAConfig
	conn *client.Connection
}

// New constructs a Manager. It validates the configuration, builds the
// portpool, and creates one *client.Connection per (DRA, Interface). No
// network activity happens until Start is called.
func New(ctx context.Context, cfg Config, log logger.Logger) (*Manager, error) {
	cfg = cfg.withDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	pool, err := portpool.New(portpool.Config{
		SourceIP:           cfg.SourceIP,
		MinPort:            cfg.MinPort,
		MaxPort:            cfg.MaxPort,
		QuarantineDuration: cfg.QuarantineDuration,
	})
	if err != nil {
		return nil, fmt.Errorf("connmanager: portpool: %w", err)
	}

	mctx, cancel := context.WithCancel(ctx)
	m := &Manager{
		cfg:    cfg,
		pool:   pool,
		logger: log,
		ctx:    mctx,
		cancel: cancel,
		slots:  make(map[Key]*slot, len(cfg.DRAs)*len(cfg.Interfaces)),
	}

	// The portpool acquire/release functions are bridged through closures
	// so client.Connection has zero knowledge of portpool. The closure
	// also adapts the int port to a *net.TCPAddr that net.Dialer.LocalAddr
	// will accept directly.
	acquireFn := func(owner string) (*net.TCPAddr, error) {
		port, err := pool.Acquire(owner)
		if err != nil {
			return nil, err
		}
		return pool.LocalTCPAddr(port), nil
	}
	releaseFn := func(addr *net.TCPAddr) {
		if addr == nil {
			return
		}
		pool.Release(addr.Port)
	}

	for _, dra := range cfg.DRAs {
		for _, ispec := range cfg.Interfaces {
			key := Key{DRA: dra.Name, Interface: ispec.Interface}
			slotCfg := m.buildSlotConfig(dra, ispec, acquireFn, releaseFn)

			if err := slotCfg.Validate(); err != nil {
				cancel()
				return nil, fmt.Errorf("connmanager: slot %s: %w", key, err)
			}

			conn := client.NewConnection(mctx, key.String(), slotCfg, log)
			m.slots[key] = &slot{
				key:  key,
				app:  ispec.ApplicationID,
				cfg:  slotCfg,
				conn: conn,
			}
			m.keys = append(m.keys, key)
		}
	}

	return m, nil
}

// buildSlotConfig produces the per-(DRA, interface) DRAConfig, with the
// CER pinned to the single Auth-Application-Id required by the project
// memory "CER per interface": no multiplexing — S6a-only on S6a sockets,
// S13-only on S13 sockets.
func (m *Manager) buildSlotConfig(
	dra DRASpec,
	ispec InterfaceSpec,
	acquire func(string) (*net.TCPAddr, error),
	release func(*net.TCPAddr),
) *client.DRAConfig {
	return &client.DRAConfig{
		Host:              dra.Host,
		Port:              dra.Port,
		OriginHost:        m.cfg.OriginHost,
		OriginRealm:       m.cfg.OriginRealm,
		ProductName:       m.cfg.ProductName,
		VendorID:          m.cfg.VendorID,
		ConnectionCount:   1, // one socket per slot — telco profile invariant
		ConnectTimeout:    m.cfg.ConnectTimeout,
		CERTimeout:        m.cfg.CERTimeout,
		DWRInterval:       m.cfg.DWRInterval,
		DWRTimeout:        m.cfg.DWRTimeout,
		MaxDWRFailures:    m.cfg.MaxDWRFailures,
		ReconnectInterval: m.cfg.ReconnectInterval,
		MaxReconnectDelay: m.cfg.MaxReconnectDelay,
		ReconnectBackoff:  m.cfg.ReconnectBackoff,
		SendBufferSize:    m.cfg.SendBufferSize,
		RecvBufferSize:    m.cfg.RecvBufferSize,
		AuthAppIDs:        []uint32{ispec.ApplicationID}, // strict per-interface
		AcquireLocalAddr:  acquire,
		ReleaseLocalAddr:  release,
	}
}

// Start dials every slot. It does not block waiting for handshakes to
// complete — each Connection.Start kicks off its own reconnect loop, so
// initial dial failures are tolerated and retried automatically. The
// returned error is non-nil only when no slot could even begin starting,
// which today only happens if the manager has been Closed.
func (m *Manager) Start() error {
	if err := m.ctx.Err(); err != nil {
		return fmt.Errorf("connmanager: cannot start a closed manager: %w", err)
	}

	m.logger.Infow("Starting connmanager",
		"slots", len(m.slots),
		"source_ip", m.cfg.SourceIP.String(),
		"port_range", fmt.Sprintf("%d-%d", m.cfg.MinPort, m.cfg.MaxPort))

	var wg sync.WaitGroup
	for _, k := range m.keys {
		s := m.slots[k]
		wg.Add(1)
		go func(s *slot) {
			defer wg.Done()
			if err := s.conn.Start(); err != nil {
				// Connection.Start already triggered handleFailure(), which
				// kicks off the reconnect loop, so we just log here. Not
				// returning the error is intentional: one slot failing on
				// the very first dial must not stop the other 7.
				m.logger.Warnw("Initial slot dial failed; reconnect loop engaged",
					"slot", s.key, "error", err)
			}
		}(s)
	}
	wg.Wait()

	return nil
}

// Close cancels every slot, waits for goroutines to drain, and tears
// down the manager. After Close, all accessor methods continue to work
// but return zero/empty values for the connection state.
func (m *Manager) Close() error {
	m.logger.Infow("Closing connmanager")
	m.cancel()

	var wg sync.WaitGroup
	for _, k := range m.keys {
		s := m.slots[k]
		wg.Add(1)
		go func(s *slot) {
			defer wg.Done()
			if err := s.conn.Close(); err != nil {
				m.logger.Errorw("error closing slot", "slot", s.key, "error", err)
			}
		}(s)
	}
	wg.Wait()
	return nil
}

// Get returns the *client.Connection for one slot, or nil if the key is
// unknown. Callers should not retain the pointer across Close.
func (m *Manager) Get(k Key) *client.Connection {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if s, ok := m.slots[k]; ok {
		return s.conn
	}
	return nil
}

// Keys returns the manager's slot keys in stable insertion order.
func (m *Manager) Keys() []Key {
	out := make([]Key, len(m.keys))
	copy(out, m.keys)
	return out
}

// Slots returns a snapshot of every slot, suitable for diagnostics and
// tests. The returned SlotInfo.Config pointer is the live config — read
// only.
func (m *Manager) Slots() []SlotInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]SlotInfo, 0, len(m.keys))
	for _, k := range m.keys {
		s := m.slots[k]
		out = append(out, SlotInfo{
			Key:           s.key,
			ApplicationID: s.app,
			Connection:    s.conn,
			Config:        s.cfg,
		})
	}
	return out
}

// States returns the current ConnectionState of every slot. This is the
// data PR 5's GetConnectionState API will sit on top of.
func (m *Manager) States() map[Key]client.ConnectionState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[Key]client.ConnectionState, len(m.slots))
	for k, s := range m.slots {
		out[k] = s.conn.GetState()
	}
	return out
}

// ActiveCount returns how many slots are currently in an active state
// (StateOpen or any other IsActive() state).
func (m *Manager) ActiveCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n := 0
	for _, s := range m.slots {
		if s.conn.IsActive() {
			n++
		}
	}
	return n
}

// PortStats exposes the underlying portpool snapshot.
func (m *Manager) PortStats() portpool.Stats {
	return m.pool.Stats()
}

// --- config defaulting & validation ---------------------------------------

func (c Config) withDefaults() Config {
	if len(c.Interfaces) == 0 {
		c.Interfaces = DefaultInterfaces()
	}
	if c.QuarantineDuration == 0 {
		c.QuarantineDuration = 60 * time.Second
	}
	if c.ConnectTimeout == 0 {
		c.ConnectTimeout = 10 * time.Second
	}
	if c.CERTimeout == 0 {
		c.CERTimeout = 5 * time.Second
	}
	if c.DWRInterval == 0 {
		c.DWRInterval = 30 * time.Second
	}
	if c.DWRTimeout == 0 {
		c.DWRTimeout = 10 * time.Second
	}
	if c.MaxDWRFailures == 0 {
		c.MaxDWRFailures = 3
	}
	if c.ReconnectInterval == 0 {
		c.ReconnectInterval = 1 * time.Second
	}
	if c.MaxReconnectDelay == 0 {
		c.MaxReconnectDelay = 30 * time.Second
	}
	if c.ReconnectBackoff < 1.0 {
		c.ReconnectBackoff = 2.0
	}
	if c.SendBufferSize == 0 {
		c.SendBufferSize = 1000
	}
	if c.RecvBufferSize == 0 {
		c.RecvBufferSize = 1000
	}
	if c.ProductName == "" {
		c.ProductName = "Diameter-GW"
	}
	if c.VendorID == 0 {
		c.VendorID = 10415 // 3GPP
	}
	return c
}

func (c Config) validate() error {
	if c.SourceIP == nil {
		return fmt.Errorf("connmanager: SourceIP is required")
	}
	if c.OriginHost == "" {
		return fmt.Errorf("connmanager: OriginHost is required")
	}
	if c.OriginRealm == "" {
		return fmt.Errorf("connmanager: OriginRealm is required")
	}
	if len(c.DRAs) == 0 {
		return fmt.Errorf("connmanager: at least one DRA is required")
	}
	seen := make(map[string]bool, len(c.DRAs))
	for _, d := range c.DRAs {
		if d.Name == "" {
			return fmt.Errorf("connmanager: DRA name is required")
		}
		if seen[d.Name] {
			return fmt.Errorf("connmanager: duplicate DRA name %q", d.Name)
		}
		seen[d.Name] = true
		if d.Host == "" {
			return fmt.Errorf("connmanager: DRA %q: host is required", d.Name)
		}
		if d.Port <= 0 || d.Port > 65535 {
			return fmt.Errorf("connmanager: DRA %q: port %d out of range", d.Name, d.Port)
		}
	}

	// The pool needs at least as many ports as slots — otherwise the
	// first reconnect after a TIME_WAIT-induced quarantine immediately
	// exhausts the pool. Require headroom of at least one extra port.
	totalSlots := len(c.DRAs) * len(c.Interfaces)
	totalPorts := c.MaxPort - c.MinPort + 1
	if totalPorts <= totalSlots {
		return fmt.Errorf("connmanager: port range [%d,%d] holds %d ports but %d slots need at least %d (one per slot + headroom)",
			c.MinPort, c.MaxPort, totalPorts, totalSlots, totalSlots+1)
	}

	return nil
}
