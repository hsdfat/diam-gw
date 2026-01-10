package gateway_test

import (
	"context"
	"testing"
	"time"

	"github.com/hsdfat/diam-gw/client"
	"github.com/hsdfat/diam-gw/pkg/logger"
)

// TestConnectionReconnectAfterInitialFailure tests that connections automatically
// reconnect when initial dial fails (Issue 1 from ERRORS.md)
func TestConnectionReconnectAfterInitialFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	log := logger.New("reconnect-test", "debug")

	// Create connection pool that will initially fail because DRA is not running yet
	poolConfig := &client.DRAConfig{
		Host:              "127.0.0.1",
		Port:              13880,
		OriginHost:        "test-client.example.com",
		OriginRealm:       "example.com",
		ProductName:       "Test-Client",
		VendorID:          10415,
		ConnectionCount:   1,
		ConnectTimeout:    2 * time.Second,
		CERTimeout:        2 * time.Second,
		DWRInterval:       10 * time.Second,
		DWRTimeout:        5 * time.Second,
		MaxDWRFailures:    3,
		ReconnectInterval: 2 * time.Second,
		MaxReconnectDelay: 10 * time.Second,
		ReconnectBackoff:  1.5,
		SendBufferSize:    100,
		RecvBufferSize:    100,
	}

	pool, err := client.NewConnectionPool(ctx, poolConfig, log)
	if err != nil {
		t.Fatalf("Failed to create connection pool: %v", err)
	}

	// Start pool - this should NOT fail even though DRA is not running
	// Connections should start reconnecting in the background
	t.Log("Starting connection pool (DRA not yet running)...")
	err = pool.Start()
	if err != nil {
		t.Fatalf("Pool.Start() should not fail even when DRA is down, got error: %v", err)
	}
	defer pool.Close()

	// Verify no active connections initially
	initialStats := pool.GetStats()
	t.Logf("Initial stats: active=%d, total=%d, reconnects=%d",
		initialStats.ActiveConnections, initialStats.TotalConnections, initialStats.TotalReconnects)

	if initialStats.ActiveConnections != 0 {
		t.Errorf("Expected 0 active connections initially, got %d", initialStats.ActiveConnections)
	}

	// Now start the DRA simulator
	t.Log("Starting DRA simulator (3 seconds after pool start)...")
	time.Sleep(3 * time.Second)

	dra := NewDRASimulator(ctx, "127.0.0.1:13880", log)
	if err := dra.Start(); err != nil {
		t.Fatalf("Failed to start DRA simulator: %v", err)
	}
	defer dra.Stop()

	// Wait for automatic reconnection
	t.Log("Waiting for automatic reconnection...")
	reconnected := false
	for i := 0; i < 20; i++ { // Wait up to 20 seconds
		time.Sleep(1 * time.Second)

		if pool.IsHealthy() {
			stats := pool.GetStats()
			t.Logf("Connection successful after %d seconds: active=%d, reconnects=%d",
				i+1, stats.ActiveConnections, stats.TotalReconnects)
			reconnected = true
			break
		}
	}

	if !reconnected {
		stats := pool.GetStats()
		t.Fatalf("Connection did not reconnect automatically within 20 seconds. Stats: active=%d, total=%d, reconnects=%d",
			stats.ActiveConnections, stats.TotalConnections, stats.TotalReconnects)
	}

	// Verify connection state is actually OPEN
	connections := pool.GetAllConnections()
	if len(connections) == 0 {
		t.Fatal("No connections in pool")
	}

	connState := connections[0].GetState()
	if !connState.IsActive() {
		t.Errorf("Connection should be active, got state: %s", connState.String())
	}

	// Verify reconnect counter was incremented
	finalStats := pool.GetStats()
	if finalStats.TotalReconnects == 0 {
		t.Errorf("Expected reconnect count > 0, got %d", finalStats.TotalReconnects)
	}

	t.Logf("✓ Test passed: Connection automatically reconnected after initial dial failure (state=%s, reconnects=%d)",
		connState.String(), finalStats.TotalReconnects)
}

// TestConnectionReconnectAfterRuntimeFailure tests that connections automatically
// reconnect when they fail during runtime (Issue 2 from ERRORS.md)
func TestConnectionReconnectAfterRuntimeFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	log := logger.New("runtime-reconnect-test", "debug")

	// Create and start DRA simulator first
	// Note: DRA simulator handles CER/CEA and DWR/DWA automatically
	dra := NewDRASimulator(ctx, "127.0.0.1:13881", log)
	if err := dra.Start(); err != nil {
		t.Fatalf("Failed to start DRA simulator: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	// Create connection pool
	poolConfig := &client.DRAConfig{
		Host:              "127.0.0.1",
		Port:              13881,
		OriginHost:        "test-client.example.com",
		OriginRealm:       "example.com",
		ProductName:       "Test-Client",
		VendorID:          10415,
		ConnectionCount:   1,
		ConnectTimeout:    2 * time.Second,
		CERTimeout:        2 * time.Second,
		DWRInterval:       5 * time.Second,
		DWRTimeout:        2 * time.Second,
		MaxDWRFailures:    2,
		ReconnectInterval: 2 * time.Second,
		MaxReconnectDelay: 10 * time.Second,
		ReconnectBackoff:  1.5,
		SendBufferSize:    100,
		RecvBufferSize:    100,
	}

	pool, err := client.NewConnectionPool(ctx, poolConfig, log)
	if err != nil {
		t.Fatalf("Failed to create connection pool: %v", err)
	}

	// Start pool - should succeed
	err = pool.Start()
	if err != nil {
		t.Fatalf("Pool.Start() failed: %v", err)
	}
	defer pool.Close()

	// Wait for connection to establish
	time.Sleep(2 * time.Second)

	// Verify connection is active
	if !pool.IsHealthy() {
		t.Fatal("Pool should be healthy after initial connection")
	}

	initialStats := pool.GetStats()
	t.Logf("Initial connection established: active=%d, reconnects=%d",
		initialStats.ActiveConnections, initialStats.TotalReconnects)

	// Stop DRA to simulate connection failure
	t.Log("Stopping DRA to simulate connection failure...")
	dra.Stop()

	// Wait for connection failure detection (via DWR timeout)
	time.Sleep(8 * time.Second)

	// Verify connection is down
	if pool.IsHealthy() {
		t.Error("Pool should not be healthy after DRA shutdown")
	}

	failedStats := pool.GetStats()
	t.Logf("After DRA shutdown: active=%d, reconnects=%d",
		failedStats.ActiveConnections, failedStats.TotalReconnects)

	// Restart DRA
	t.Log("Restarting DRA...")
	dra = NewDRASimulator(ctx, "127.0.0.1:13881", log)
	if err := dra.Start(); err != nil {
		t.Fatalf("Failed to restart DRA simulator: %v", err)
	}
	defer dra.Stop()

	// Wait for automatic reconnection
	t.Log("Waiting for automatic reconnection...")
	reconnected := false
	for i := 0; i < 20; i++ { // Wait up to 20 seconds
		time.Sleep(1 * time.Second)

		if pool.IsHealthy() {
			stats := pool.GetStats()
			t.Logf("Connection re-established after %d seconds: active=%d, reconnects=%d",
				i+1, stats.ActiveConnections, stats.TotalReconnects)
			reconnected = true
			break
		}
	}

	if !reconnected {
		stats := pool.GetStats()
		t.Fatalf("Connection did not reconnect automatically within 20 seconds. Stats: active=%d, total=%d, reconnects=%d",
			stats.ActiveConnections, stats.TotalConnections, stats.TotalReconnects)
	}

	// Verify reconnection counter was incremented
	finalStats := pool.GetStats()
	if finalStats.TotalReconnects <= initialStats.TotalReconnects {
		t.Errorf("Expected reconnect count to increase from %d, got %d",
			initialStats.TotalReconnects, finalStats.TotalReconnects)
	}

	t.Log("✓ Test passed: Connection automatically reconnected after runtime failure")
}
