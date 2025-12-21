package gateway_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hsdfat/diam-gw/client"
	"github.com/hsdfat/diam-gw/commands/s13"
	"github.com/hsdfat/diam-gw/gateway"
	"github.com/hsdfat/diam-gw/models_base"
	"github.com/hsdfat/diam-gw/pkg/connection"
	"github.com/hsdfat/diam-gw/pkg/logger"
	"github.com/hsdfat/diam-gw/server"
)

// PerformanceMetrics holds performance test metrics
type PerformanceMetrics struct {
	TPS               int
	TotalRequests     uint64
	TotalResponses    uint64
	TotalErrors       uint64
	TotalTimeouts     uint64
	AvgLatencyMs      float64
	MinLatencyMs      float64
	MaxLatencyMs      float64
	P50LatencyMs      float64
	P95LatencyMs      float64
	P99LatencyMs      float64
	SuccessRate       float64
	Duration          time.Duration
	Stable            bool
	ErrorRate         float64
	TimeoutRate       float64
}

// TPSController manages request rate
type TPSController struct {
	targetTPS     int
	interval      time.Duration
	requestsSent  atomic.Uint64
	responseRecv  atomic.Uint64
	errors        atomic.Uint64
	timeouts      atomic.Uint64
	latencies     []time.Duration
	latenciesMu   sync.Mutex
	ctx           context.Context
	cancel        context.CancelFunc
}

// NewTPSController creates a new TPS controller
func NewTPSController(targetTPS int) *TPSController {
	ctx, cancel := context.WithCancel(context.Background())
	return &TPSController{
		targetTPS: targetTPS,
		interval:  time.Second / time.Duration(targetTPS),
		latencies: make([]time.Duration, 0, targetTPS*10),
		ctx:       ctx,
		cancel:    cancel,
	}
}

// RecordLatency records a request latency
func (tc *TPSController) RecordLatency(latency time.Duration) {
	tc.latenciesMu.Lock()
	tc.latencies = append(tc.latencies, latency)
	tc.latenciesMu.Unlock()
}

// GetMetrics returns current metrics
func (tc *TPSController) GetMetrics(duration time.Duration) *PerformanceMetrics {
	tc.latenciesMu.Lock()
	defer tc.latenciesMu.Unlock()

	metrics := &PerformanceMetrics{
		TPS:            tc.targetTPS,
		TotalRequests:  tc.requestsSent.Load(),
		TotalResponses: tc.responseRecv.Load(),
		TotalErrors:    tc.errors.Load(),
		TotalTimeouts:  tc.timeouts.Load(),
		Duration:       duration,
	}

	if metrics.TotalRequests > 0 {
		metrics.SuccessRate = float64(metrics.TotalResponses) / float64(metrics.TotalRequests) * 100
		metrics.ErrorRate = float64(metrics.TotalErrors) / float64(metrics.TotalRequests) * 100
		metrics.TimeoutRate = float64(metrics.TotalTimeouts) / float64(metrics.TotalRequests) * 100
	}

	// Calculate latency statistics
	if len(tc.latencies) > 0 {
		// Sort latencies for percentile calculation
		sortedLatencies := make([]time.Duration, len(tc.latencies))
		copy(sortedLatencies, tc.latencies)
		sortLatencies(sortedLatencies)

		var sum time.Duration
		metrics.MinLatencyMs = float64(sortedLatencies[0].Microseconds()) / 1000.0
		metrics.MaxLatencyMs = float64(sortedLatencies[len(sortedLatencies)-1].Microseconds()) / 1000.0

		for _, lat := range sortedLatencies {
			sum += lat
		}
		metrics.AvgLatencyMs = float64(sum.Microseconds()) / float64(len(sortedLatencies)) / 1000.0

		// Calculate percentiles
		metrics.P50LatencyMs = float64(sortedLatencies[len(sortedLatencies)*50/100].Microseconds()) / 1000.0
		metrics.P95LatencyMs = float64(sortedLatencies[len(sortedLatencies)*95/100].Microseconds()) / 1000.0
		metrics.P99LatencyMs = float64(sortedLatencies[len(sortedLatencies)*99/100].Microseconds()) / 1000.0
	}

	// Check stability: success rate > 99% and error rate < 1%
	metrics.Stable = metrics.SuccessRate >= 99.0 && metrics.ErrorRate < 1.0

	return metrics
}

// Stop stops the TPS controller
func (tc *TPSController) Stop() {
	tc.cancel()
}

// sortLatencies sorts latencies in ascending order (simple bubble sort for small arrays)
func sortLatencies(latencies []time.Duration) {
	n := len(latencies)
	for i := 0; i < n-1; i++ {
		for j := 0; j < n-i-1; j++ {
			if latencies[j] > latencies[j+1] {
				latencies[j], latencies[j+1] = latencies[j+1], latencies[j]
			}
		}
	}
}

// Enhanced DRA simulator for performance testing
type PerformanceDRASimulator struct {
	*DRASimulator
	responsesHandled atomic.Uint64
	responseTimes    []time.Duration
	responseTimesMu  sync.Mutex
	requestMap       map[uint32]time.Time // H2H ID -> request start time
	requestMapMu     sync.RWMutex
}

// NewPerformanceDRASimulator creates a DRA simulator with performance tracking
func NewPerformanceDRASimulator(ctx context.Context, address string, log logger.Logger) *PerformanceDRASimulator {
	return &PerformanceDRASimulator{
		DRASimulator:  NewDRASimulator(ctx, address, log),
		responseTimes: make([]time.Duration, 0, 10000),
		requestMap:    make(map[uint32]time.Time),
	}
}

// RecordResponse records a response time
func (p *PerformanceDRASimulator) RecordResponse(duration time.Duration) {
	p.responsesHandled.Add(1)
	p.responseTimesMu.Lock()
	p.responseTimes = append(p.responseTimes, duration)
	p.responseTimesMu.Unlock()
}

// TrackRequest tracks when a request is sent
func (p *PerformanceDRASimulator) TrackRequest(h2hID uint32, startTime time.Time) {
	p.requestMapMu.Lock()
	p.requestMap[h2hID] = startTime
	p.requestMapMu.Unlock()
}

// GetRequestStartTime gets the start time for a request
func (p *PerformanceDRASimulator) GetRequestStartTime(h2hID uint32) (time.Time, bool) {
	p.requestMapMu.RLock()
	defer p.requestMapMu.RUnlock()
	t, ok := p.requestMap[h2hID]
	return t, ok
}

// SendMICRWithTracking sends MICR and tracks it for performance measurement
func (p *PerformanceDRASimulator) SendMICRWithTracking(imei string) (uint32, error) {
	// Get active connections
	p.connectionsMu.RLock()
	var conn server.Conn
	for _, c := range p.connections {
		conn = c
		break // Use first connection
	}
	p.connectionsMu.RUnlock()

	if conn == nil {
		return 0, fmt.Errorf("no active gateway connections")
	}

	// Generate unique H2H and E2E IDs
	h2hID := uint32(time.Now().UnixNano() & 0xFFFFFFFF)
	e2eID := h2hID

	// Create MICR message
	tmp := models_base.UTF8String(imei)
	micr := s13.NewMEIdentityCheckRequest()
	micr.Header.HopByHopID = h2hID
	micr.Header.EndToEndID = e2eID
	micr.SessionId = models_base.UTF8String(fmt.Sprintf("perf-test.example.com;%s;1", imei))
	micr.AuthSessionState = models_base.Enumerated(1)
	micr.OriginHost = models_base.DiameterIdentity("perf-test.example.com")
	micr.OriginRealm = models_base.DiameterIdentity("example.com")
	micr.DestinationRealm = models_base.DiameterIdentity("server.example.com")
	micr.TerminalInformation = &s13.TerminalInformation{
		Imei: &tmp,
	}
	data, err := micr.Marshal()
	if err != nil {
		return 0, err
	}

	// Track request start time BEFORE sending
	p.TrackRequest(h2hID, time.Now())

	// Send MICR
	if _, err := conn.Write(data); err != nil {
		return 0, fmt.Errorf("failed to send MICR: %w", err)
	}

	return h2hID, nil
}

// TestS13Performance tests S13 interface performance with progressive TPS increase
func TestS13Performance(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	log := logger.New("perf-test", "error")
	draLog := log.With("mod", "dra").(logger.Logger)
	gwLog := log.With("mod", "gw").(logger.Logger)
	appLog := log.With("mod", "eir").(logger.Logger)

	// Start DRA simulator
	t.Log("========================================")
	t.Log("S13 Interface Performance Test")
	t.Log("========================================")
	t.Log("Starting DRA simulator...")
	dra := NewPerformanceDRASimulator(ctx, "127.0.0.1:14900", draLog)
	if err := dra.Start(); err != nil {
		t.Fatalf("Failed to start DRA simulator: %v", err)
	}
	defer dra.Stop()
	time.Sleep(200 * time.Millisecond)

	// Create gateway configuration
	t.Log("Starting gateway...")
	gwConfig := &gateway.GatewayConfig{
		InServerConfig: &server.ServerConfig{
			ListenAddress:  "127.0.0.1:14901",
			MaxConnections: 1000,
			ConnectionConfig: &server.ConnectionConfig{
				OriginHost:       "s13-gw.example.com",
				OriginRealm:      "example.com",
				ProductName:      "S13-Gateway",
				VendorID:         10415,
				ReadTimeout:      10 * time.Second,
				WriteTimeout:     5 * time.Second,
				WatchdogInterval: 30 * time.Second,
				WatchdogTimeout:  10 * time.Second,
				MaxMessageSize:   65535,
				SendChannelSize:  1000,
				RecvChannelSize:  1000,
				HandleWatchdog:   true,
			},
			RecvChannelSize: 1000,
		},
		DRAPoolConfig: &client.DRAPoolConfig{
			DRAs: []*client.DRAServerConfig{
				{
					Name:     "DRA-S13",
					Host:     "127.0.0.1",
					Port:     14900,
					Priority: 1,
					Weight:   100,
				},
			},
			OriginHost:          "s13-gw.example.com",
			OriginRealm:         "example.com",
			ProductName:         "S13-Gateway",
			VendorID:            10415,
			ConnectionsPerDRA:   2,
			ConnectTimeout:      5 * time.Second,
			CERTimeout:          5 * time.Second,
			DWRInterval:         30 * time.Second,
			DWRTimeout:          10 * time.Second,
			MaxDWRFailures:      3,
			HealthCheckInterval: 5 * time.Second,
			ReconnectInterval:   2 * time.Second,
			MaxReconnectDelay:   30 * time.Second,
			ReconnectBackoff:    1.5,
			SendBufferSize:      1000,
			RecvBufferSize:      1000,
		},
		InClientConfig: &client.PoolConfig{
			OriginHost:          "s13-gw.example.com",
			OriginRealm:         "example.com",
			ProductName:         "S13-Gateway",
			VendorID:            10415,
			DialTimeout:         5 * time.Second,
			SendTimeout:         10 * time.Second,
			CERTimeout:          5 * time.Second,
			DWRInterval:         30 * time.Second,
			DWRTimeout:          10 * time.Second,
			MaxDWRFailures:      3,
			AuthAppIDs:          []uint32{16777251, 16777252},
			SendBufferSize:      1000,
			RecvBufferSize:      1000,
			ReconnectEnabled:    false,
			ReconnectInterval:   2 * time.Second,
			MaxReconnectDelay:   30 * time.Second,
			ReconnectBackoff:    1.5,
			HealthCheckInterval: 10 * time.Second,
		},
		DRASupported:   true,
		OriginHost:     "s13-gw.example.com",
		OriginRealm:    "example.com",
		ProductName:    "S13-Gateway",
		VendorID:       10415,
		SessionTimeout: 10 * time.Second,
	}

	// Start gateway
	gw, err := gateway.NewGateway(gwConfig, log)
	if err != nil {
		t.Fatalf("Failed to create gateway: %v", err)
	}
	registerBaseProtocolHandlers(gw, t)

	// Track message counts for validation
	var gwMessageStats struct {
		micr atomic.Uint64 // MICR received from DRA
		mica atomic.Uint64 // MICA sent back to DRA
		errors atomic.Uint64 // Errors forwarding to EIR
	}

	// Register MICR handler
	gw.RegisterDraPoolServer(connection.Command{
		Code:      324,
		Interface: s13.S13_APPLICATION_ID,
		Request:   true,
	}, func(msg *connection.Message, conn connection.Conn) {
		gwMessageStats.micr.Add(1) // Count MICR received from DRA
		gwLog.Debugw("Processing MICR message")

		rsp, err := gw.SendInternal("127.0.0.1:14911", append(msg.Header, msg.Body...))
		if err != nil {
			gwLog.Errorw("Cannot send to EIR", "err", err)
			gwMessageStats.errors.Add(1) // Count errors
			return
		}

		gwMessageStats.mica.Add(1) // Count MICA sent back to DRA
		conn.Write(rsp)
	})

	if err := gw.Start(); err != nil {
		t.Fatalf("Failed to start gateway: %v", err)
	}
	defer gw.Stop()

	// Wait for gateway to establish DRA connection
	time.Sleep(1 * time.Second)

	// Verify DRA connection
	draStats := gw.GetDRAPool().GetStats()
	if draStats.ActiveConnections == 0 {
		t.Fatal("Gateway did not connect to DRA")
	}
	t.Logf("✓ Gateway connected to DRA: %d active connections", draStats.ActiveConnections)

	// Start Logic App (EIR) simulator
	t.Log("Starting Logic App (EIR) simulator...")
	eir := NewS13EIRSimulator(ctx, "127.0.0.1:14901", "127.0.0.1:14911", appLog)
	if err := eir.Start(); err != nil {
		t.Fatalf("Failed to start EIR simulator: %v", err)
	}
	eir.server.HandleFunc(connection.Command{
		Code:      324,
		Interface: s13.S13_APPLICATION_ID,
		Request:   true,
	}, func(msg *connection.Message, conn connection.Conn) {
		appLog.Debugw("Processing MICR message")
		micr := s13.NewMEIdentityCheckRequest()
		err := micr.Unmarshal(append(msg.Header, msg.Body...))
		if err != nil {
			appLog.Errorw("Cannot unmarshal", "error", err)
			return
		}

		mica := s13.NewMEIdentityCheckAnswer()
		mica.SessionId = micr.SessionId
		mica.AuthSessionState = models_base.Enumerated(1)
		mica.OriginHost = models_base.DiameterIdentity("server.example.com")
		mica.OriginRealm = models_base.DiameterIdentity("server.example.com")
		resultCode := models_base.Unsigned32(2001)
		mica.ResultCode = &resultCode

		data, err := mica.Marshal()
		if err != nil {
			appLog.Errorw("Cannot marshal MICA", "error", err)
			return
		}
		conn.Write(data)
	})
	defer eir.Stop()

	// Wait for EIR to connect
	time.Sleep(500 * time.Millisecond)

	t.Log("========================================")
	t.Log("System ready - starting performance test")
	t.Log("========================================")

	// TPS levels to test
	tpsLevels := []int{10, 25, 50, 100, 200, 400, 800, 1600, 3200}
	testDuration := 10 * time.Second

	var allMetrics []*PerformanceMetrics
	var maxStableTPS int

	for _, targetTPS := range tpsLevels {
		t.Logf("\n========================================")
		t.Logf("Testing TPS: %d", targetTPS)
		t.Logf("========================================")

		// Capture stats before test
		gwMICRBefore := gwMessageStats.micr.Load()
		gwMICABefore := gwMessageStats.mica.Load()
		gwErrBefore := gwMessageStats.errors.Load()

		metrics := runTPSTest(t, dra, gw, targetTPS, testDuration)
		allMetrics = append(allMetrics, metrics)

		// Calculate delta for this test round
		gwMICRDelta := gwMessageStats.micr.Load() - gwMICRBefore
		gwMICADelta := gwMessageStats.mica.Load() - gwMICABefore
		gwErrDelta := gwMessageStats.errors.Load() - gwErrBefore

		// Print metrics
		printMetrics(t, metrics)

		// Print detailed stats for validation
		gwStats := gw.GetStats()
		eirStats := eir.GetStats()
		draStatsServer := dra.server.GetStats()

		t.Logf("\n=== Component Stats Validation ===")
		t.Logf("DRA Server:")
		t.Logf("  Messages Sent: %d, Received: %d, Errors: %d",
			draStatsServer.MessagesSent,
			draStatsServer.MessagesReceived,
			draStatsServer.Errors)

		t.Logf("Gateway InServer (DRA-facing):")
		t.Logf("  Messages Received: %d, Sent: %d, Errors: %d",
			gwStats.InServer.MessagesReceived,
			gwStats.InServer.MessagesSent,
			gwStats.InServer.Errors)

		t.Logf("Gateway DraPool (client to DRA):")
		t.Logf("  Messages Sent: %d, Received: %d, Dropped: %d",
			gwStats.DraPool.TotalMessagesSent,
			gwStats.DraPool.TotalMessagesRecv,
			gwStats.DraPool.TotalMessagesDrop)

		t.Logf("Gateway InClient (client to EIR):")
		t.Logf("  Requests: %d, Responses: %d, Errors: %d, Timeouts: %d",
			gwStats.InClient.TotalRequests,
			gwStats.InClient.TotalResponses,
			gwStats.InClient.TotalErrors,
			gwStats.InClient.TotalTimeouts)

		t.Logf("EIR Server:")
		t.Logf("  Requests Received: %d, Responses Sent: %d, Errors: %d",
			eirStats.RequestsReceived,
			eirStats.ResponsesSent,
			eirStats.Errors)

		t.Logf("\n=== Message Flow Validation (This Round) ===")
		t.Logf("Test: Expected %d requests", metrics.TotalRequests)
		t.Logf("Gateway Handler: Received %d MICR, Sent %d MICA, Errors %d",
			gwMICRDelta, gwMICADelta, gwErrDelta)
		t.Logf("EIR: Received %d MICR (cumulative), Sent %d MICA (cumulative)",
			eirStats.RequestsReceived, eirStats.ResponsesSent)

		t.Logf("\n=== End-to-End Validation ===")
		successfulForwards := gwMICRDelta - gwErrDelta
		if gwMICADelta != successfulForwards {
			t.Logf("⚠ WARNING: Gateway sent %d MICA but successfully forwarded %d to EIR",
				gwMICADelta, successfulForwards)
		}
		if gwErrDelta > 0 {
			t.Logf("⚠ WARNING: %d requests failed at Gateway→EIR (%.2f%% error rate)",
				gwErrDelta, float64(gwErrDelta)/float64(gwMICRDelta)*100)
		}
		if gwMICRDelta == gwMICADelta {
			t.Logf("✓ Gateway: All %d MICR successfully converted to MICA", gwMICRDelta)
		}

		// Check if system is stable
		if metrics.Stable {
			maxStableTPS = targetTPS
			t.Logf("✓ System STABLE at %d TPS", targetTPS)
		} else {
			t.Logf("⚠ System UNSTABLE at %d TPS", targetTPS)
			t.Logf("Breaking test - max stable TPS found: %d", maxStableTPS)
			break
		}

		// Cool down between tests
		time.Sleep(2 * time.Second)
	}

	// Print summary
	t.Log("\n========================================")
	t.Log("Performance Test Summary")
	t.Log("========================================")
	printSummary(t, allMetrics, maxStableTPS)
}

// runTPSTest runs a performance test at a specific TPS level
func runTPSTest(t *testing.T, dra *PerformanceDRASimulator, gw *gateway.Gateway, targetTPS int, duration time.Duration) *PerformanceMetrics {
	controller := NewTPSController(targetTPS)
	defer controller.Stop()

	startTime := time.Now()
	endTime := startTime.Add(duration)
	ticker := time.NewTicker(controller.interval)
	defer ticker.Stop()

	requestNum := 0
	for time.Now().Before(endTime) {
		select {
		case <-ticker.C:
			requestNum++
			imei := fmt.Sprintf("123456789%06d", requestNum)

			// Send request
			reqStart := time.Now()
			controller.requestsSent.Add(1)

			go func(imei string, reqStart time.Time) {
				initialReqCount := dra.GetRequestCount()

				if err := dra.SendMICR(imei); err != nil {
					controller.errors.Add(1)
					return
				}

				// Wait for DRA to process the MICA response (request count increases when DRA receives MICA)
				timeout := time.After(3 * time.Second)
				checkTicker := time.NewTicker(2 * time.Millisecond)
				defer checkTicker.Stop()

				for {
					select {
					case <-timeout:
						controller.timeouts.Add(1)
						return
					case <-checkTicker.C:
						// DRA increments request count when it receives messages (including MICA responses)
						if dra.GetRequestCount() > initialReqCount {
							latency := time.Since(reqStart)
							controller.responseRecv.Add(1)
							controller.RecordLatency(latency)
							return
						}
					}
				}
			}(imei, reqStart)

		case <-controller.ctx.Done():
			return controller.GetMetrics(time.Since(startTime))
		}
	}

	// Wait a bit for pending responses
	time.Sleep(500 * time.Millisecond)

	return controller.GetMetrics(time.Since(startTime))
}

// printMetrics prints performance metrics
func printMetrics(t *testing.T, m *PerformanceMetrics) {
	t.Logf("Duration: %v", m.Duration)
	t.Logf("Requests:  %d", m.TotalRequests)
	t.Logf("Responses: %d", m.TotalResponses)
	t.Logf("Errors:    %d", m.TotalErrors)
	t.Logf("Timeouts:  %d", m.TotalTimeouts)
	t.Logf("Success Rate:  %.2f%%", m.SuccessRate)
	t.Logf("Error Rate:    %.2f%%", m.ErrorRate)
	t.Logf("Timeout Rate:  %.2f%%", m.TimeoutRate)

	if m.TotalResponses > 0 {
		t.Logf("Latency (ms):")
		t.Logf("  Min: %.2f", m.MinLatencyMs)
		t.Logf("  Avg: %.2f", m.AvgLatencyMs)
		t.Logf("  Max: %.2f", m.MaxLatencyMs)
		t.Logf("  P50: %.2f", m.P50LatencyMs)
		t.Logf("  P95: %.2f", m.P95LatencyMs)
		t.Logf("  P99: %.2f", m.P99LatencyMs)
	}
}

// printSummary prints overall test summary
func printSummary(t *testing.T, allMetrics []*PerformanceMetrics, maxStableTPS int) {
	t.Logf("Maximum Stable TPS: %d", maxStableTPS)
	t.Log("\nTPS Progression:")
	t.Log("TPS\tSuccess%%\tError%%\tTimeout%%\tAvg Latency(ms)\tStable")
	t.Log("---\t--------\t------\t--------\t---------------\t------")

	for _, m := range allMetrics {
		stable := "✓"
		if !m.Stable {
			stable = "✗"
		}
		t.Logf("%d\t%.2f%%\t\t%.2f%%\t%.2f%%\t\t%.2f\t\t%s",
			m.TPS,
			m.SuccessRate,
			m.ErrorRate,
			m.TimeoutRate,
			m.AvgLatencyMs,
			stable,
		)
	}

	t.Log("\n========================================")
	t.Logf("✅ Performance test completed")
	t.Logf("🎯 Maximum stable TPS: %d", maxStableTPS)
	t.Log("========================================")
}
