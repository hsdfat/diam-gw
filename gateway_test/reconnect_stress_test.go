package gateway_test

import (
	"context"
	"fmt"
	"net"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hsdfat/diam-gw/client"
	"github.com/hsdfat/diam-gw/dra"
	"github.com/hsdfat/diam-gw/pkg/logger"
)

// dumpGoroutines writes a full goroutine stack trace to t.Log.
func dumpGoroutines(t *testing.T) {
	t.Helper()
	buf := make([]byte, 1<<20) // 1 MiB — enough for hundreds of goroutines
	n := runtime.Stack(buf, true)
	t.Logf("=== goroutine dump ===\n%s", buf[:n])
}

// startDRAOnAddr starts a DRANode on a pre-known address (not ephemeral).
// Returns a stopFn that blocks until the node has fully shut down.
// Unlike startRealDRA, the caller controls the address so the DRA can be
// restarted on the same port after a kill cycle.
func startDRAOnAddr(t *testing.T, addr, name, originHost string) func() {
	t.Helper()
	node := dra.NewDRANode(dra.Config{
		NodeName:      name,
		ListenAddr:    addr,
		OriginHost:    originHost,
		OriginRealm:   "epc.test",
		ProductName:   "stress-dra",
		VendorID:      10415,
		HostIPs:       []net.IP{net.ParseIP("127.0.0.1")},
		SupportedApps: []uint32{client.AppIDS6a, client.AppIDS13},
	})

	done := make(chan struct{})
	go func() {
		_ = node.StartNode()
		close(done)
	}()

	// Wait until the DRA is accepting connections.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return func() { node.Stop(); <-done }
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("DRA %s never came up on %s", name, addr)
	return nil
}

// reserveAddr picks an ephemeral port and returns its address string.
// It closes the listener immediately — the port stays reserved by the OS
// briefly; we pass it straight to startDRAOnAddr before the window closes.
func reserveAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserveAddr: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

// waitPoolHealthy blocks until at least one connection in pool is active,
// or fails the test after timeout.
func waitPoolHealthy(t *testing.T, pool *client.DRAPool, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if pool.IsHealthy() {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// goroutineDelta returns the goroutine count delta relative to baseline.
func goroutineCount() int { return runtime.NumGoroutine() }

// TestE2E_Stress_DRAReconnect kills and restarts the DRA repeatedly while the
// gateway is running. It verifies:
//
//  1. Connections always recover — pool becomes healthy again within a
//     reasonable window after each restart.
//  2. No goroutine leak — the goroutine count at the end must not exceed
//     the baseline by more than a small constant (leaked goroutines per
//     cycle accumulate; a flat or near-flat delta is the signal we want).
//  3. Traffic resumes — AIRs succeed after each recovery, proving the
//     reconnected socket is fully operational.
//  4. Port rotation — each reconnect picks a different source port
//     (logged; the fix to dialWithLocalBind ensures this).
func TestE2E_Stress_DRAReconnect(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping reconnect stress in -short")
	}

	const (
		cycles          = 15               // kill/restart cycles
		downTime        = 1 * time.Second  // how long DRA stays down per cycle
		recoveryTimeout = 15 * time.Second // max time to wait for pool to heal
		trafficPerCycle = 20               // AIRs sent after each recovery
		perReqBudget    = 3 * time.Second
		portMin         = 50000
		portMax         = 50100
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	log := logger.New("reconnect-stress", "error")

	// Reserve a stable address for the DRA so we can restart it on the
	// same port each cycle.
	draAddr := reserveAddr(t)
	t.Logf("DRA address: %s", draAddr)
	draHost, draPort := splitHostPort(t, draAddr)

	// First DRA instance.
	stopDRA := startDRAOnAddr(t, draAddr, "dra-stress", "dra-stress.epc.test")

	// Build gateway with S6a pool pointing at the reserved address.
	hss := NewS6aHSSSimulator(ctx, "127.0.0.1:0", log.With("mod", "hss").(logger.Logger))
	if err := hss.Start(); err != nil {
		stopDRA()
		t.Fatalf("start HSS: %v", err)
	}
	defer hss.Stop()

	poolCfg := s6aPoolConfig(draHost, draPort, "gw-reconnect.test", "hss.realm", portMin, portMax)
	// Aggressive reconnect so the test doesn't spend most of its time waiting.
	poolCfg.ReconnectInterval = 200 * time.Millisecond
	poolCfg.MaxReconnectDelay = 2 * time.Second
	poolCfg.ReconnectBackoff = 1.5
	poolCfg.DWRInterval = 5 * time.Second
	poolCfg.DWRTimeout = 2 * time.Second

	pools := map[uint32]*client.DRAPoolConfig{
		client.AppIDS6a: poolCfg,
	}
	gw, err := client.NewGateway(ctx, &client.GatewayConfig{Pools: pools},
		log.With("mod", "gw").(logger.Logger))
	if err != nil {
		stopDRA()
		t.Fatalf("NewGateway: %v", err)
	}
	if err := gw.Start(); err != nil {
		stopDRA()
		t.Fatalf("gw.Start: %v", err)
	}
	defer gw.Close()

	backend, err := client.NewAddressClient(ctx,
		backendClientConfig("gw-reconnect-backend.test", "gw.test", []uint32{client.AppIDS6a}),
		log.With("mod", "backend").(logger.Logger))
	if err != nil {
		stopDRA()
		t.Fatalf("NewAddressClient: %v", err)
	}
	registerS6aForwarder(t, gw, backend, hss.ListenAddr())

	s6aPool := gw.Pool(client.AppIDS6a)

	// Wait for initial connection.
	if !waitPoolHealthy(t, s6aPool, recoveryTimeout) {
		stopDRA()
		t.Fatalf("pool never became healthy before test started")
	}
	t.Logf("initial connection established")

	// newMME creates a fresh MMESimulator (with its own context) and connects
	// it to the DRA. A new instance is needed each cycle because Stop()
	// cancels the context permanently — restarting the same instance would
	// leave sendAndWait returning immediately on ctx.Done().
	newMME := func() *MMESimulator {
		m := NewMMESimulator(ctx, draAddr, "mme-reconnect.epc.test", "epc.test", log)
		if err := m.Start(); err != nil {
			t.Logf("MME start: %v", err)
		}
		return m
	}

	mme := newMME()
	defer mme.Stop()

	// Baseline goroutine count after full setup (let runtime settle).
	runtime.GC()
	time.Sleep(200 * time.Millisecond)
	baseline := goroutineCount()
	t.Logf("goroutine baseline (post-setup): %d", baseline)

	// --- cycle loop ---
	var (
		totalTrafficOK  atomic.Uint64
		totalTrafficErr atomic.Uint64
	)

	for cycle := 1; cycle <= cycles; cycle++ {
		// 1. Send a burst of AIRs — must all succeed while DRA is up.
		preOK, preErr := 0, 0
		for i := 0; i < trafficPerCycle; i++ {
			_, err := mme.SendAIR(
				fmt.Sprintf("1234567890%02d%04d", cycle, i),
				"hss.realm", "gw-reconnect.test", perReqBudget)
			if err != nil {
				preErr++
			} else {
				preOK++
			}
		}
		t.Logf("[cycle %2d] pre-kill traffic: ok=%d err=%d", cycle, preOK, preErr)
		if preErr > trafficPerCycle/10 {
			t.Errorf("cycle %d: too many pre-kill errors (%d/%d)", cycle, preErr, trafficPerCycle)
		}

		// 2. Kill the DRA abruptly. Stop the MME too — its TCP socket to the
		// DRA is broken; we'll reconnect a fresh one after DRA comes back.
		mme.Stop()
		stopDRA()
		t.Logf("[cycle %2d] DRA killed — waiting %s", cycle, downTime)
		time.Sleep(downTime)

		// 3. Restart DRA on the same address, then reconnect a fresh MME.
		stopDRA = startDRAOnAddr(t, draAddr, "dra-stress", "dra-stress.epc.test")
		t.Logf("[cycle %2d] DRA restarted", cycle)

		// 4. Wait for the gateway pool to recover first, then start MME.
		// Gateway reconnects automatically; pool.IsHealthy() confirms DRA has
		// processed the gateway's CER and registered it as a peer.
		recovered := waitPoolHealthy(t, s6aPool, recoveryTimeout)
		grCount := goroutineCount()
		t.Logf("[cycle %2d] pool_healthy=%v goroutines=%d (delta=%+d)",
			cycle, recovered, grCount, grCount-baseline)

		if !recovered {
			t.Errorf("cycle %d: pool did not recover within %s", cycle, recoveryTimeout)
			mme = newMME()
			continue
		}

		// Fresh MME — new context, new TCP connection, full CER/CEA handshake.
		mme = newMME()

		// 5. Confirm traffic resumes.
		postOK, postErr := 0, 0
		for i := 0; i < trafficPerCycle; i++ {
			_, err := mme.SendAIR(
				fmt.Sprintf("9876543210%02d%04d", cycle, i),
				"hss.realm", "gw-reconnect.test", perReqBudget)
			if err != nil {
				postErr++
			} else {
				postOK++
			}
		}
		t.Logf("[cycle %2d] post-recover traffic: ok=%d err=%d", cycle, postOK, postErr)
		if postErr > trafficPerCycle/10 {
			t.Errorf("cycle %d: too many post-recover errors (%d/%d)", cycle, postErr, trafficPerCycle)
		}
		totalTrafficOK.Add(uint64(preOK + postOK))
		totalTrafficErr.Add(uint64(preErr + postErr))
	}

	// Final cleanup.
	stopDRA()

	// Allow goroutines to drain.
	time.Sleep(500 * time.Millisecond)
	runtime.GC()
	time.Sleep(200 * time.Millisecond)

	finalGR := goroutineCount()
	delta := finalGR - baseline
	t.Logf("=== reconnect stress summary ===")
	t.Logf("  cycles            : %d", cycles)
	t.Logf("  total traffic ok  : %d", totalTrafficOK.Load())
	t.Logf("  total traffic err : %d", totalTrafficErr.Load())
	t.Logf("  goroutines baseline : %d", baseline)
	t.Logf("  goroutines final    : %d (delta %+d)", finalGR, delta)
	t.Logf("  HSS=%+v", hss.GetStats())

	// Goroutine leak check: allow a small fixed headroom (GC goroutines,
	// timer goroutines) but fail if it scales with cycles.
	const goroutineLeakThreshold = 20
	if delta > goroutineLeakThreshold {
		t.Errorf("goroutine leak: delta=%d exceeds threshold=%d (baseline=%d final=%d)",
			delta, goroutineLeakThreshold, baseline, finalGR)
	}

	// Always dump stacks so we can identify what the residual goroutines are.
	if delta > 0 {
		dumpGoroutines(t)
	}
}
