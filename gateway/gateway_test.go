package gateway

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/hsdfat/diam-gw/client"
	"github.com/hsdfat/diam-gw/pkg/logger"
	"github.com/hsdfat/diam-gw/server"
)

func TestGatewayConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  *GatewayConfig
		wantErr bool
	}{
		{
			name:    "nil config",
			config:  nil,
			wantErr: true,
		},
		{
			name: "missing OriginHost",
			config: &GatewayConfig{
				OriginRealm:   "example.com",
				DRAPoolConfig: &client.DRAPoolConfig{},
			},
			wantErr: true,
		},
		{
			name: "missing OriginRealm",
			config: &GatewayConfig{
				OriginHost:    "gw.example.com",
				DRAPoolConfig: &client.DRAPoolConfig{},
			},
			wantErr: true,
		},
		{
			name: "missing DRAPoolConfig",
			config: &GatewayConfig{
				OriginHost:  "gw.example.com",
				OriginRealm: "example.com",
			},
			wantErr: true,
		},
		{
			name: "valid config",
			config: &GatewayConfig{
				OriginHost:  "gw.example.com",
				OriginRealm: "example.com",
				ProductName: "Test-Gateway",
				VendorID:    10415,
				DRAPoolConfig: &client.DRAPoolConfig{
					DRAs: []*client.DRAServerConfig{
						{
							Name:     "DRA-1",
							Host:     "127.0.0.1",
							Port:     3868,
							Priority: 1,
						},
					},
					ConnectionsPerDRA: 1,
				},
				ServerConfig:   server.DefaultServerConfig(),
				SessionTimeout: 30 * time.Second,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateGatewayConfig(tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateGatewayConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestDefaultGatewayConfig(t *testing.T) {
	config := DefaultGatewayConfig()

	if config.OriginHost == "" {
		t.Error("Expected OriginHost to be set")
	}
	if config.OriginRealm == "" {
		t.Error("Expected OriginRealm to be set")
	}
	if config.ProductName == "" {
		t.Error("Expected ProductName to be set")
	}
	if config.VendorID == 0 {
		t.Error("Expected VendorID to be set")
	}
	if config.SessionTimeout == 0 {
		t.Error("Expected SessionTimeout to be set")
	}
	if config.ServerConfig == nil {
		t.Error("Expected ServerConfig to be set")
	}
}

func TestHopByHopIDGeneration(t *testing.T) {
	// Reset global counter
	globalHopByHopID.Store(0)

	config := &GatewayConfig{
		OriginHost:  "gw.example.com",
		OriginRealm: "example.com",
		ProductName: "Test-Gateway",
		VendorID:    10415,
		DRAPoolConfig: &client.DRAPoolConfig{
			DRAs: []*client.DRAServerConfig{
				{
					Name:     "DRA-1",
					Host:     "127.0.0.1",
					Port:     3868,
					Priority: 1,
				},
			},
			ConnectionsPerDRA: 1,
		},
		ServerConfig: server.DefaultServerConfig(),
	}

	log := logger.New("test", "error")
	gw, err := NewGateway(config, log)
	if err != nil {
		t.Fatalf("Failed to create gateway: %v", err)
	}

	// Generate multiple IDs
	id1 := gw.generateHopByHopID()
	id2 := gw.generateHopByHopID()
	id3 := gw.generateHopByHopID()

	// Should be sequential
	if id2 != id1+1 {
		t.Errorf("Expected sequential IDs, got %d and %d", id1, id2)
	}
	if id3 != id2+1 {
		t.Errorf("Expected sequential IDs, got %d and %d", id2, id3)
	}

	// Should be unique
	ids := map[uint32]bool{id1: true, id2: true, id3: true}
	if len(ids) != 3 {
		t.Error("Expected unique IDs")
	}
}

func TestSessionTracking(t *testing.T) {
	config := &GatewayConfig{
		OriginHost:  "gw.example.com",
		OriginRealm: "example.com",
		ProductName: "Test-Gateway",
		VendorID:    10415,
		DRAPoolConfig: &client.DRAPoolConfig{
			DRAs: []*client.DRAServerConfig{
				{
					Name:     "DRA-1",
					Host:     "127.0.0.1",
					Port:     3868,
					Priority: 1,
				},
			},
			ConnectionsPerDRA: 1,
		},
		ServerConfig:   server.DefaultServerConfig(),
		SessionTimeout: 100 * time.Millisecond,
	}

	log := logger.New("test", "error")
	gw, err := NewGateway(config, log)
	if err != nil {
		t.Fatalf("Failed to create gateway: %v", err)
	}

	// Create test session
	session := &Session{
		LogicAppRemoteAddr:  "127.0.0.1:12345",
		LogicAppConnection:  nil, // Would be real connection in production
		OriginalHopByHopID:  0x12345678,
		OriginalEndToEndID:  0xABCDEF01,
		OriginalCommandCode: 316,      // ULR
		OriginalAppID:       16777251, // S6a
		DRAHopByHopID:       0x00000001,
		CreatedAt:           time.Now(),
	}

	// Store session
	gw.sessionsMu.Lock()
	gw.sessions[session.DRAHopByHopID] = session
	gw.sessionsMu.Unlock()
	gw.stats.ActiveSessions.Add(1)

	// Verify session exists
	gw.sessionsMu.RLock()
	stored, exists := gw.sessions[session.DRAHopByHopID]
	gw.sessionsMu.RUnlock()

	if !exists {
		t.Fatal("Session not found")
	}
	if stored.OriginalHopByHopID != 0x12345678 {
		t.Errorf("Expected H2H 0x12345678, got 0x%x", stored.OriginalHopByHopID)
	}

	// Test session cleanup
	time.Sleep(150 * time.Millisecond)
	gw.cleanupExpiredSessions()

	// Session should be removed
	gw.sessionsMu.RLock()
	_, exists = gw.sessions[session.DRAHopByHopID]
	gw.sessionsMu.RUnlock()

	if exists {
		t.Error("Expected expired session to be removed")
	}
}

func TestMessageHeaderParsing(t *testing.T) {
	// Create a test Diameter message header
	header := make([]byte, 20)

	// Version
	header[0] = 1

	// Message length (20 bytes)
	binary.BigEndian.PutUint32(header[0:4], 20)
	header[0] = 1 // Restore version

	// Command flags (Request bit set)
	header[4] = 0x80 // R=1

	// Command code (316 = ULR)
	binary.BigEndian.PutUint32(header[4:8], 316)
	header[4] = 0x80 // Restore flags

	// Application ID (16777251 = S6a)
	binary.BigEndian.PutUint32(header[8:12], 16777251)

	// Hop-by-Hop ID
	binary.BigEndian.PutUint32(header[12:16], 0x12345678)

	// End-to-End ID
	binary.BigEndian.PutUint32(header[16:20], 0xABCDEF01)

	// Parse message
	msgInfo, err := client.ParseMessageHeader(header)
	if err != nil {
		t.Fatalf("Failed to parse header: %v", err)
	}

	// Verify parsed values
	if msgInfo.CommandCode != 316 {
		t.Errorf("Expected command code 316, got %d", msgInfo.CommandCode)
	}
	if msgInfo.ApplicationID != 16777251 {
		t.Errorf("Expected app ID 16777251, got %d", msgInfo.ApplicationID)
	}
	if msgInfo.HopByHopID != 0x12345678 {
		t.Errorf("Expected H2H 0x12345678, got 0x%x", msgInfo.HopByHopID)
	}
	if msgInfo.EndToEndID != 0xABCDEF01 {
		t.Errorf("Expected E2E 0xABCDEF01, got 0x%x", msgInfo.EndToEndID)
	}
	if !msgInfo.Flags.Request {
		t.Error("Expected Request flag to be set")
	}
}

func TestHopByHopIDReplacement(t *testing.T) {
	// Create a test message
	msg := make([]byte, 20)

	// Set original H2H ID
	binary.BigEndian.PutUint32(msg[12:16], 0x12345678)

	// Set original E2E ID
	binary.BigEndian.PutUint32(msg[16:20], 0xABCDEF01)

	// Replace H2H ID (like gateway does)
	newH2H := uint32(0x00000001)
	newMsg := make([]byte, len(msg))
	copy(newMsg, msg)
	binary.BigEndian.PutUint32(newMsg[12:16], newH2H)

	// Verify replacement
	replacedH2H := binary.BigEndian.Uint32(newMsg[12:16])
	if replacedH2H != newH2H {
		t.Errorf("Expected H2H 0x%x, got 0x%x", newH2H, replacedH2H)
	}

	// Verify E2E is preserved
	e2e := binary.BigEndian.Uint32(newMsg[16:20])
	if e2e != 0xABCDEF01 {
		t.Errorf("Expected E2E to be preserved, got 0x%x", e2e)
	}

	// Restore original H2H ID (like gateway does for response)
	restoredMsg := make([]byte, len(newMsg))
	copy(restoredMsg, newMsg)
	binary.BigEndian.PutUint32(restoredMsg[12:16], 0x12345678)

	// Verify restoration
	restoredH2H := binary.BigEndian.Uint32(restoredMsg[12:16])
	if restoredH2H != 0x12345678 {
		t.Errorf("Expected restored H2H 0x12345678, got 0x%x", restoredH2H)
	}
}

func TestGatewayStats(t *testing.T) {
	config := &GatewayConfig{
		OriginHost:  "gw.example.com",
		OriginRealm: "example.com",
		ProductName: "Test-Gateway",
		VendorID:    10415,
		DRAPoolConfig: &client.DRAPoolConfig{
			DRAs: []*client.DRAServerConfig{
				{
					Name:     "DRA-1",
					Host:     "127.0.0.1",
					Port:     3868,
					Priority: 1,
				},
			},
			ConnectionsPerDRA: 1,
		},
		ServerConfig: server.DefaultServerConfig(),
	}

	log := logger.New("test", "error")
	gw, err := NewGateway(config, log)
	if err != nil {
		t.Fatalf("Failed to create gateway: %v", err)
	}

	// Update statistics
	gw.stats.TotalRequests.Add(100)
	gw.stats.TotalResponses.Add(95)
	gw.stats.TotalErrors.Add(5)
	gw.stats.ActiveSessions.Add(10)
	gw.stats.TotalForwarded.Add(100)
	gw.stats.TotalFromDRA.Add(95)
	gw.stats.TimeoutErrors.Add(3)
	gw.stats.RoutingErrors.Add(2)

	// Add latency samples
	gw.stats.TotalLatencyMs.Add(1000) // 10 requests @ 100ms each
	gw.stats.RequestCount.Add(10)

	// Get stats snapshot
	stats := gw.GetStats()

	// Verify stats
	if stats.TotalRequests != 100 {
		t.Errorf("Expected 100 requests, got %d", stats.TotalRequests)
	}
	if stats.TotalResponses != 95 {
		t.Errorf("Expected 95 responses, got %d", stats.TotalResponses)
	}
	if stats.TotalErrors != 5 {
		t.Errorf("Expected 5 errors, got %d", stats.TotalErrors)
	}
	if stats.ActiveSessions != 10 {
		t.Errorf("Expected 10 active sessions, got %d", stats.ActiveSessions)
	}
	if stats.TotalForwarded != 100 {
		t.Errorf("Expected 100 forwarded, got %d", stats.TotalForwarded)
	}
	if stats.TotalFromDRA != 95 {
		t.Errorf("Expected 95 from DRA, got %d", stats.TotalFromDRA)
	}
	if stats.TimeoutErrors != 3 {
		t.Errorf("Expected 3 timeout errors, got %d", stats.TimeoutErrors)
	}
	if stats.RoutingErrors != 2 {
		t.Errorf("Expected 2 routing errors, got %d", stats.RoutingErrors)
	}

	// Check average latency (1000ms / 10 requests = 100ms)
	expectedAvg := 100.0
	if stats.AverageLatencyMs != expectedAvg {
		t.Errorf("Expected avg latency %.2f, got %.2f", expectedAvg, stats.AverageLatencyMs)
	}
}

func TestConcurrentSessionAccess(t *testing.T) {
	config := &GatewayConfig{
		OriginHost:  "gw.example.com",
		OriginRealm: "example.com",
		ProductName: "Test-Gateway",
		VendorID:    10415,
		DRAPoolConfig: &client.DRAPoolConfig{
			DRAs: []*client.DRAServerConfig{
				{
					Name:     "DRA-1",
					Host:     "127.0.0.1",
					Port:     3868,
					Priority: 1,
				},
			},
			ConnectionsPerDRA: 1,
		},
		ServerConfig: server.DefaultServerConfig(),
	}

	log := logger.New("test", "error")
	gw, err := NewGateway(config, log)
	if err != nil {
		t.Fatalf("Failed to create gateway: %v", err)
	}

	// ctx, cancel := context.WithCancel(context.Background())
	// defer cancel()

	// Concurrent session creation
	const numGoroutines = 10
	const sessionsPerGoroutine = 100

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			for j := 0; j < sessionsPerGoroutine; j++ {
				h2hID := uint32(id*sessionsPerGoroutine + j)
				session := &Session{
					LogicAppRemoteAddr: "127.0.0.1:12345",
					DRAHopByHopID:      h2hID,
					CreatedAt:          time.Now(),
				}

				gw.sessionsMu.Lock()
				gw.sessions[h2hID] = session
				gw.sessionsMu.Unlock()
			}
		}(i)
	}

	// Wait a bit for goroutines to complete
	time.Sleep(100 * time.Millisecond)

	// Verify total sessions
	gw.sessionsMu.RLock()
	totalSessions := len(gw.sessions)
	gw.sessionsMu.RUnlock()

	expectedTotal := numGoroutines * sessionsPerGoroutine
	if totalSessions != expectedTotal {
		t.Errorf("Expected %d sessions, got %d", expectedTotal, totalSessions)
	}
}

func BenchmarkHopByHopIDGeneration(b *testing.B) {
	config := &GatewayConfig{
		OriginHost:  "gw.example.com",
		OriginRealm: "example.com",
		ProductName: "Test-Gateway",
		VendorID:    10415,
		DRAPoolConfig: &client.DRAPoolConfig{
			DRAs: []*client.DRAServerConfig{
				{Name: "DRA-1", Host: "127.0.0.1", Port: 3868, Priority: 1},
			},
			ConnectionsPerDRA: 1,
		},
		ServerConfig: server.DefaultServerConfig(),
	}

	log := logger.New("test", "error")
	gw, _ := NewGateway(config, log)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		gw.generateHopByHopID()
	}
}

func BenchmarkSessionLookup(b *testing.B) {
	config := &GatewayConfig{
		OriginHost:  "gw.example.com",
		OriginRealm: "example.com",
		ProductName: "Test-Gateway",
		VendorID:    10415,
		DRAPoolConfig: &client.DRAPoolConfig{
			DRAs: []*client.DRAServerConfig{
				{Name: "DRA-1", Host: "127.0.0.1", Port: 3868, Priority: 1},
			},
			ConnectionsPerDRA: 1,
		},
		ServerConfig: server.DefaultServerConfig(),
	}

	log := logger.New("test", "error")
	gw, _ := NewGateway(config, log)

	// Create test sessions
	for i := 0; i < 1000; i++ {
		session := &Session{
			DRAHopByHopID: uint32(i),
			CreatedAt:     time.Now(),
		}
		gw.sessions[uint32(i)] = session
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h2hID := uint32(i % 1000)
		gw.sessionsMu.RLock()
		_, _ = gw.sessions[h2hID]
		gw.sessionsMu.RUnlock()
	}
}
