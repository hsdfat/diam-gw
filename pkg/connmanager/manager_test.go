package connmanager

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/hsdfat/diam-gw/commands/s13"
	"github.com/hsdfat/diam-gw/commands/s6a"
	"github.com/hsdfat/diam-gw/pkg/logger"
)

// testLogger silences manager output to keep test runs readable. The
// underlying go-zlog logger doesn't ship a discard sink, so we just lower
// the level — that's enough since these tests don't assert log output.
func testLogger() logger.Logger {
	return logger.New("connmanager-test", "error")
}

// fourDRAs is the canonical telco shape: 4 DRAs × 2 interfaces = 8 slots.
// Tests that just need a valid Config use this so they all exercise the
// production-shaped path.
func fourDRAs() []DRASpec {
	return []DRASpec{
		{Name: "DRA-1", Host: "127.0.0.1", Port: 13868},
		{Name: "DRA-2", Host: "127.0.0.1", Port: 13869},
		{Name: "DRA-3", Host: "127.0.0.1", Port: 13870},
		{Name: "DRA-4", Host: "127.0.0.1", Port: 13871},
	}
}

func baseCfg() Config {
	return Config{
		SourceIP:    net.ParseIP("127.0.0.1"),
		MinPort:     56000,
		MaxPort:     56050, // 51 ports — 8 slots + headroom
		DRAs:        fourDRAs(),
		OriginHost:  "gw.test.local",
		OriginRealm: "test.local",
		ProductName: "diam-gw-test",
		VendorID:    10415,
	}
}

// --- construction & validation --------------------------------------------

func TestNew_Validation(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Config)
	}{
		{"nil source ip", func(c *Config) { c.SourceIP = nil }},
		{"missing origin host", func(c *Config) { c.OriginHost = "" }},
		{"missing origin realm", func(c *Config) { c.OriginRealm = "" }},
		{"no dras", func(c *Config) { c.DRAs = nil }},
		{"duplicate dra name", func(c *Config) {
			c.DRAs = []DRASpec{
				{Name: "X", Host: "127.0.0.1", Port: 1},
				{Name: "X", Host: "127.0.0.1", Port: 2},
			}
		}},
		{"empty dra host", func(c *Config) {
			c.DRAs = []DRASpec{{Name: "Y", Host: "", Port: 1}}
		}},
		{"bad dra port", func(c *Config) {
			c.DRAs = []DRASpec{{Name: "Y", Host: "127.0.0.1", Port: 0}}
		}},
		{"port range too tight", func(c *Config) {
			// 4 DRAs × 2 ifaces = 8 slots, range = exactly 8 ports → must fail
			c.MinPort = 60000
			c.MaxPort = 60007
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := baseCfg()
			tc.mut(&cfg)
			if _, err := New(context.Background(), cfg, testLogger()); err == nil {
				t.Fatalf("expected error, got nil")
			}
		})
	}
}

func TestNew_BuildsExactly8Slots(t *testing.T) {
	m, err := New(context.Background(), baseCfg(), testLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	if got := len(m.Keys()); got != 8 {
		t.Fatalf("slots=%d, want 8", got)
	}

	// Every (DRA, interface) pair must be present.
	want := map[Key]bool{}
	for _, dra := range fourDRAs() {
		want[Key{DRA: dra.Name, Interface: InterfaceS6a}] = true
		want[Key{DRA: dra.Name, Interface: InterfaceS13}] = true
	}
	for _, k := range m.Keys() {
		if !want[k] {
			t.Errorf("unexpected key %s", k)
		}
		delete(want, k)
	}
	if len(want) != 0 {
		t.Fatalf("missing keys: %v", want)
	}
}

// TestPerInterfaceAuthAppID enforces project memory "CER per interface":
// each slot's CER must advertise exactly one Auth-Application-Id, and that
// id must match the slot's interface.
func TestPerInterfaceAuthAppID(t *testing.T) {
	m, err := New(context.Background(), baseCfg(), testLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	for _, s := range m.Slots() {
		if len(s.Config.AuthAppIDs) != 1 {
			t.Errorf("slot %s: AuthAppIDs=%v, want exactly one entry",
				s.Key, s.Config.AuthAppIDs)
			continue
		}
		got := s.Config.AuthAppIDs[0]
		var want uint32
		switch s.Key.Interface {
		case InterfaceS6a:
			want = s6a.S6A_APPLICATION_ID
		case InterfaceS13:
			want = s13.S13_APPLICATION_ID
		default:
			t.Errorf("slot %s: unknown interface", s.Key)
			continue
		}
		if got != want {
			t.Errorf("slot %s: AppID=%d, want %d", s.Key, got, want)
		}
	}
}

// TestPortHooksWired exercises the Acquire/Release closures the manager
// installed into each slot's DRAConfig and asserts they round-trip through
// the underlying portpool. This proves the per-slot config really would
// bind to a pool port at dial time, without doing any networking.
func TestPortHooksWired(t *testing.T) {
	cfg := baseCfg()
	m, err := New(context.Background(), cfg, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	// Acquire one address through every slot's hook.
	got := make(map[int]Key)
	for _, s := range m.Slots() {
		if s.Config.AcquireLocalAddr == nil {
			t.Fatalf("slot %s: AcquireLocalAddr hook is nil", s.Key)
		}
		addr, err := s.Config.AcquireLocalAddr(s.Key.String())
		if err != nil {
			t.Fatalf("slot %s: acquire: %v", s.Key, err)
		}
		if !addr.IP.Equal(cfg.SourceIP) {
			t.Errorf("slot %s: ip=%v, want %v", s.Key, addr.IP, cfg.SourceIP)
		}
		if addr.Port < cfg.MinPort || addr.Port > cfg.MaxPort {
			t.Errorf("slot %s: port=%d, out of range [%d,%d]",
				s.Key, addr.Port, cfg.MinPort, cfg.MaxPort)
		}
		if other, dup := got[addr.Port]; dup {
			t.Errorf("port %d acquired twice: %s and %s", addr.Port, other, s.Key)
		}
		got[addr.Port] = s.Key
	}

	if used := m.PortStats().InUse; used != 8 {
		t.Fatalf("PortStats.InUse=%d, want 8", used)
	}

	// Now release them all back through the hook and confirm the pool empties.
	for _, s := range m.Slots() {
		// Find the port we just acquired for this slot.
		var port int
		for p, k := range got {
			if k == s.Key {
				port = p
				break
			}
		}
		s.Config.ReleaseLocalAddr(&net.TCPAddr{IP: cfg.SourceIP, Port: port})
	}
	if used := m.PortStats().InUse; used != 0 {
		t.Fatalf("after release: InUse=%d, want 0", used)
	}
}

// TestStart_BindsLocalAddr is the one network-y test in the file. It
// stands up a tiny TCP listener on loopback (acting as the DRA), wires the
// 4 DRA specs at it, runs Manager.Start, and verifies that every accepted
// connection's remote address (which is the gateway's local address) is
// inside the configured (SourceIP, MinPort..MaxPort) range. The handshake
// will fail because the listener doesn't speak Diameter — that's fine,
// the dialer's LocalAddr is observable on the accept side regardless.
func TestStart_BindsLocalAddr(t *testing.T) {
	if testing.Short() {
		t.Skip("network test")
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	draPort := ln.Addr().(*net.TCPAddr).Port

	const wantSlots = 4 // one DRA × 2 ifaces × ... wait, see below
	// Use a single DRA with 2 interfaces = 2 slots so the listener sees
	// exactly 2 connections. Keeps the test deterministic without race
	// conditions on accept count.
	cfg := Config{
		SourceIP:    net.ParseIP("127.0.0.1"),
		MinPort:     56100,
		MaxPort:     56120,
		DRAs:        []DRASpec{{Name: "DRA-X", Host: "127.0.0.1", Port: draPort}},
		OriginHost:  "gw.test.local",
		OriginRealm: "test.local",
		ProductName: "diam-gw-test",
		VendorID:    10415,
		// Ridiculously short timeouts so the test doesn't sit waiting on
		// CER/CEA exchanges that will never come from a dumb listener.
		ConnectTimeout:    500 * time.Millisecond,
		CERTimeout:        200 * time.Millisecond,
		ReconnectInterval: 50 * time.Millisecond,
		MaxReconnectDelay: 100 * time.Millisecond,
	}

	// Accept side: capture the remote port of every incoming connection.
	type observed struct {
		ip   string
		port int
	}
	obsCh := make(chan observed, 16)
	var ln_wg sync.WaitGroup
	ln_wg.Add(1)
	go func() {
		defer ln_wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			ra := c.RemoteAddr().(*net.TCPAddr)
			obsCh <- observed{ip: ra.IP.String(), port: ra.Port}
			c.Close() // hang up so the manager retries quickly
		}
	}()

	m, err := New(context.Background(), cfg, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Start(); err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	// Wait for at least 2 distinct accepts (one per slot). The test
	// allows a few retries in case of dial races.
	deadline := time.After(3 * time.Second)
	seenPorts := map[int]bool{}
	for len(seenPorts) < 2 {
		select {
		case o := <-obsCh:
			if o.ip != "127.0.0.1" {
				t.Errorf("source ip=%s, want 127.0.0.1", o.ip)
			}
			if o.port < cfg.MinPort || o.port > cfg.MaxPort {
				t.Errorf("source port=%d, out of range [%d,%d]",
					o.port, cfg.MinPort, cfg.MaxPort)
			}
			seenPorts[o.port] = true
		case <-deadline:
			t.Fatalf("only saw %d distinct source ports, want >=2; pool stats=%+v",
				len(seenPorts), m.PortStats())
		}
	}

	_ = wantSlots // (silences unused-const lint if anyone changes the test)
}
