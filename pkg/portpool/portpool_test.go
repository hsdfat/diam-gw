package portpool

import (
	"net"
	"sync"
	"testing"
	"time"
)

func mustNew(t *testing.T, cfg Config) *Pool {
	t.Helper()
	p, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func defaultCfg() Config {
	return Config{
		SourceIP: net.ParseIP("10.204.71.73"),
		MinPort:  56000,
		MaxPort:  56009, // 10 ports — small enough to exhaust quickly
	}
}

// --- construction ----------------------------------------------------------

func TestNew_Validation(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{"nil ip", Config{MinPort: 1, MaxPort: 2}},
		{"min < 1", Config{SourceIP: net.IPv4(1, 2, 3, 4), MinPort: 0, MaxPort: 10}},
		{"max > 65535", Config{SourceIP: net.IPv4(1, 2, 3, 4), MinPort: 1, MaxPort: 70000}},
		{"min > max", Config{SourceIP: net.IPv4(1, 2, 3, 4), MinPort: 100, MaxPort: 50}},
		{"negative quarantine", Config{SourceIP: net.IPv4(1, 2, 3, 4), MinPort: 1, MaxPort: 2, QuarantineDuration: -1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.cfg); err == nil {
				t.Fatalf("expected error, got nil")
			}
		})
	}
}

func TestNew_StartsFull(t *testing.T) {
	p := mustNew(t, defaultCfg())
	s := p.Stats()
	if s.Total != 10 || s.Free != 10 || s.InUse != 0 || s.Quarantined != 0 {
		t.Fatalf("unexpected initial stats: %+v", s)
	}
}

// --- basic acquire / release ----------------------------------------------

func TestAcquireRelease_Basic(t *testing.T) {
	p := mustNew(t, defaultCfg())

	port, err := p.Acquire("conn-1")
	if err != nil {
		t.Fatal(err)
	}
	if port < 56000 || port > 56009 {
		t.Fatalf("port out of range: %d", port)
	}
	if got := p.Stats().InUse; got != 1 {
		t.Fatalf("InUse=%d, want 1", got)
	}

	p.Release(port)
	if got := p.Stats().InUse; got != 0 {
		t.Fatalf("InUse=%d after release, want 0", got)
	}
	if got := p.Stats().Free; got != 10 {
		t.Fatalf("Free=%d after release, want 10", got)
	}
}

func TestRelease_Idempotent(t *testing.T) {
	p := mustNew(t, defaultCfg())
	port, _ := p.Acquire("c")
	p.Release(port)
	// Releasing again is a no-op.
	p.Release(port)
	// Releasing an unknown / out-of-range port is a no-op.
	p.Release(99)
	p.Release(70000)
	if got := p.Stats().Free; got != 10 {
		t.Fatalf("Free=%d, want 10", got)
	}
}

// --- exhaustion ------------------------------------------------------------

func TestExhaustion(t *testing.T) {
	p := mustNew(t, defaultCfg())

	held := make([]int, 0, 10)
	for i := 0; i < 10; i++ {
		port, err := p.Acquire("c")
		if err != nil {
			t.Fatalf("Acquire %d: %v", i, err)
		}
		held = append(held, port)
	}

	// 11th must fail and bump the failure counter.
	if _, err := p.Acquire("c"); err != ErrPoolExhausted {
		t.Fatalf("expected ErrPoolExhausted, got %v", err)
	}
	if got := p.Stats().AllocationFailures; got != 1 {
		t.Fatalf("failures=%d, want 1", got)
	}

	// Release one and we can acquire again.
	p.Release(held[0])
	if _, err := p.Acquire("c"); err != nil {
		t.Fatalf("acquire after release: %v", err)
	}

	// Repeated exhaustion increments the counter monotonically.
	if _, err := p.Acquire("c"); err != ErrPoolExhausted {
		t.Fatalf("expected ErrPoolExhausted second time, got %v", err)
	}
	if got := p.Stats().AllocationFailures; got != 2 {
		t.Fatalf("failures=%d, want 2", got)
	}
}

// --- no duplicates under concurrency --------------------------------------

func TestConcurrent_NoDuplicates(t *testing.T) {
	cfg := defaultCfg()
	cfg.MinPort = 56000
	cfg.MaxPort = 56499 // 500 ports
	p := mustNew(t, cfg)

	const goroutines = 50
	const iterations = 1000
	var wg sync.WaitGroup

	// Each goroutine acquires + releases in a tight loop. The invariant
	// we check is that, while a port is held, no other goroutine ever
	// observes it as free / acquires it. We enforce this with a shared
	// owner map guarded by an atomic-style flag per port.
	var muOwner sync.Mutex
	owners := make(map[int]int) // port -> goroutine id (when held)

	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				port, err := p.Acquire("g")
				if err == ErrPoolExhausted {
					continue // pool is small relative to fan-in; that's OK
				}
				if err != nil {
					t.Errorf("acquire: %v", err)
					return
				}
				muOwner.Lock()
				if prev, dup := owners[port]; dup {
					muOwner.Unlock()
					t.Errorf("port %d acquired by g%d while held by g%d", port, id, prev)
					return
				}
				owners[port] = id
				muOwner.Unlock()

				muOwner.Lock()
				delete(owners, port)
				muOwner.Unlock()
				p.Release(port)
			}
		}(g)
	}
	wg.Wait()

	// After the storm, every port must be back in the free list.
	if got := p.Stats().InUse; got != 0 {
		t.Fatalf("InUse=%d after storm, want 0 — port leak", got)
	}
	if got := p.Stats().Free; got != 500 {
		t.Fatalf("Free=%d after storm, want 500", got)
	}
}

// --- leak resistance on dial-failure path ---------------------------------

func TestNoLeak_OnDeferredRelease(t *testing.T) {
	p := mustNew(t, defaultCfg())

	// Simulate a connect-then-fail flow 1000 times. After every iteration
	// the pool must be back to full because Release runs from defer.
	for i := 0; i < 1000; i++ {
		func() {
			port, err := p.Acquire("c")
			if err != nil {
				t.Fatal(err)
			}
			defer p.Release(port)
			// pretend the dial failed here
		}()
	}
	if got := p.Stats().Free; got != 10 {
		t.Fatalf("Free=%d, want 10 — port leaked", got)
	}
}

// --- quarantine ------------------------------------------------------------

func TestQuarantine_HoldsAndReleases(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := now
	cfg := defaultCfg()
	cfg.QuarantineDuration = 60 * time.Second
	cfg.Now = func() time.Time { return clock }
	p := mustNew(t, cfg)

	port, _ := p.Acquire("c")
	p.Release(port)

	// Immediately after release, the port is quarantined, not free.
	s := p.Stats()
	if s.Quarantined != 1 || s.Free != 9 {
		t.Fatalf("after release: %+v", s)
	}

	// Acquiring 9 ports should drain the free list without ever returning
	// the quarantined one.
	held := make(map[int]bool)
	for i := 0; i < 9; i++ {
		got, err := p.Acquire("c")
		if err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
		if got == port {
			t.Fatalf("quarantined port %d returned early", port)
		}
		held[got] = true
	}
	// 10th acquire must fail — quarantine is still active.
	if _, err := p.Acquire("c"); err != ErrPoolExhausted {
		t.Fatalf("expected exhaustion while quarantine active, got %v", err)
	}

	// Advance the fake clock past the grace window. Next Acquire must
	// return the previously-quarantined port.
	clock = now.Add(61 * time.Second)
	got, err := p.Acquire("c")
	if err != nil {
		t.Fatalf("acquire after quarantine: %v", err)
	}
	if got != port {
		t.Fatalf("expected quarantined port %d, got %d", port, got)
	}
}

// --- LocalTCPAddr helper ---------------------------------------------------

func TestLocalTCPAddr(t *testing.T) {
	p := mustNew(t, defaultCfg())
	addr := p.LocalTCPAddr(56005)
	if !addr.IP.Equal(net.ParseIP("10.204.71.73")) {
		t.Fatalf("ip = %v", addr.IP)
	}
	if addr.Port != 56005 {
		t.Fatalf("port = %d", addr.Port)
	}
}

// --- Contains --------------------------------------------------------------

func TestContains(t *testing.T) {
	p := mustNew(t, defaultCfg())
	if !p.Contains(56000) || !p.Contains(56009) || !p.Contains(56005) {
		t.Fatalf("in-range port reported missing")
	}
	if p.Contains(55999) || p.Contains(56010) || p.Contains(0) {
		t.Fatalf("out-of-range port reported present")
	}
}

// --- InUseOwners -----------------------------------------------------------

func TestInUseOwners(t *testing.T) {
	p := mustNew(t, defaultCfg())
	port1, _ := p.Acquire("conn-A")
	port2, _ := p.Acquire("conn-B")

	owners := p.InUseOwners()
	if owners[port1] != "conn-A" || owners[port2] != "conn-B" {
		t.Fatalf("owners=%v", owners)
	}
	if len(owners) != 2 {
		t.Fatalf("len=%d, want 2", len(owners))
	}

	p.Release(port1)
	owners = p.InUseOwners()
	if _, ok := owners[port1]; ok {
		t.Fatalf("released port still in owners")
	}
}
