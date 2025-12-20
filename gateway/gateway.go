package gateway

import (
	"context"
	"encoding/binary"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hsdfat/diam-gw/client"
	"github.com/hsdfat/diam-gw/pkg/logger"
	"github.com/hsdfat/diam-gw/server"
)

// Gateway acts as a Diameter proxy between Logic Apps and DRA servers
// Flow: Logic App <-> Gateway <-> DRA
type Gateway struct {
	config *GatewayConfig

	// Inbound server for Logic App connections
	server *server.Server

	// Outbound DRA connection pool with priority-based routing
	draPool *client.DRAPool

	// Session tracking: maps Hop-by-Hop ID to session info
	sessions   map[uint32]*Session
	sessionsMu sync.RWMutex

	// Statistics
	stats GatewayStats

	// Lifecycle
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	logger logger.Logger
}

// GatewayConfig holds gateway configuration
type GatewayConfig struct {
	// Server configuration (inbound from Logic Apps)
	ServerConfig *server.ServerConfig

	// DRA pool configuration (outbound to DRA servers)
	DRAPoolConfig *client.DRAPoolConfig

	// Gateway identity
	OriginHost  string
	OriginRealm string
	ProductName string
	VendorID    uint32

	// Session settings
	SessionTimeout time.Duration

	// Enable request/response logging
	EnableRequestLogging  bool
	EnableResponseLogging bool
}

// Session tracks a request-response pair through the gateway
type Session struct {
	// Original request information from Logic App
	LogicAppRemoteAddr string
	LogicAppConnection *server.Connection

	// Request identifiers from Logic App
	OriginalHopByHopID  uint32
	OriginalEndToEndID  uint32
	OriginalCommandCode uint32
	OriginalAppID       uint32

	// New identifiers for DRA request
	DRAHopByHopID uint32

	// Timestamps
	CreatedAt  time.Time
	ResponseAt time.Time

	// DRA that handled the request
	DRAName string
}

// GatewayStats tracks gateway statistics
type GatewayStats struct {
	// Request/Response counters
	TotalRequests      atomic.Uint64
	TotalResponses     atomic.Uint64
	TotalErrors        atomic.Uint64
	ActiveSessions     atomic.Uint64
	TotalForwarded     atomic.Uint64
	TotalFromDRA       atomic.Uint64
	TimeoutErrors      atomic.Uint64
	RoutingErrors      atomic.Uint64
	SessionsCreated    atomic.Uint64
	SessionsCompleted  atomic.Uint64
	SessionsExpired    atomic.Uint64

	// Latency tracking
	TotalLatencyMs     atomic.Uint64
	RequestCount       atomic.Uint64
}

// GatewayStatsSnapshot is a snapshot of gateway statistics
type GatewayStatsSnapshot struct {
	TotalRequests     uint64
	TotalResponses    uint64
	TotalErrors       uint64
	ActiveSessions    uint64
	TotalForwarded    uint64
	TotalFromDRA      uint64
	TimeoutErrors     uint64
	RoutingErrors     uint64
	SessionsCreated   uint64
	SessionsCompleted uint64
	SessionsExpired   uint64
	AverageLatencyMs  float64
}

// NewGateway creates a new Diameter Gateway
func NewGateway(config *GatewayConfig, log logger.Logger) (*Gateway, error) {
	if err := validateGatewayConfig(config); err != nil {
		return nil, fmt.Errorf("invalid gateway config: %w", err)
	}

	if log == nil {
		log = logger.New("diameter-gateway", "info")
	}

	ctx, cancel := context.WithCancel(context.Background())

	gw := &Gateway{
		config:   config,
		sessions: make(map[uint32]*Session),
		ctx:      ctx,
		cancel:   cancel,
		logger:   log,
	}

	// Create inbound server
	serverConfig := config.ServerConfig
	if serverConfig == nil {
		serverConfig = server.DefaultServerConfig()
	}
	// Override connection config to use gateway's identity
	if serverConfig.ConnectionConfig == nil {
		serverConfig.ConnectionConfig = server.DefaultConnectionConfig()
	}
	serverConfig.ConnectionConfig.OriginHost = config.OriginHost
	serverConfig.ConnectionConfig.OriginRealm = config.OriginRealm
	serverConfig.ConnectionConfig.ProductName = config.ProductName
	serverConfig.ConnectionConfig.VendorID = config.VendorID
	serverConfig.ConnectionConfig.HandleWatchdog = true // Gateway handles DWR locally

	gw.server = server.NewServer(serverConfig, log)

	// Create DRA pool
	draPoolConfig := config.DRAPoolConfig
	if draPoolConfig == nil {
		return nil, fmt.Errorf("DRAPoolConfig is required")
	}
	// Use gateway's identity for DRA connections
	draPoolConfig.OriginHost = config.OriginHost
	draPoolConfig.OriginRealm = config.OriginRealm
	draPoolConfig.ProductName = config.ProductName
	draPoolConfig.VendorID = config.VendorID

	draPool, err := client.NewDRAPool(ctx, draPoolConfig)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create DRA pool: %w", err)
	}
	gw.draPool = draPool

	return gw, nil
}

// Start starts the gateway
func (gw *Gateway) Start() error {
	gw.logger.Infow("Starting Diameter Gateway",
		"origin_host", gw.config.OriginHost,
		"origin_realm", gw.config.OriginRealm,
		"server_address", gw.config.ServerConfig.ListenAddress,
		"dra_count", len(gw.config.DRAPoolConfig.DRAs))

	// Start DRA pool first
	if err := gw.draPool.Start(); err != nil {
		return fmt.Errorf("failed to start DRA pool: %w", err)
	}
	gw.logger.Infow("DRA pool started", "active_priority", gw.draPool.GetActivePriority())

	// Start message processor for DRA responses
	gw.wg.Add(1)
	go gw.processDRAResponses()

	// Start session cleanup
	gw.wg.Add(1)
	go gw.sessionCleanup()

	// Start server in a goroutine
	gw.wg.Add(1)
	go func() {
		defer gw.wg.Done()
		if err := gw.server.Start(); err != nil {
			gw.logger.Errorw("Server failed", "error", err)
		}
	}()

	// Wait for server to be ready
	time.Sleep(100 * time.Millisecond)

	// Start monitoring Logic App connections
	gw.wg.Add(1)
	go gw.monitorLogicAppConnections()

	gw.logger.Infow("Gateway started successfully",
		"server_address", gw.config.ServerConfig.ListenAddress)

	return nil
}

// Stop gracefully stops the gateway
func (gw *Gateway) Stop() error {
	gw.logger.Infow("Stopping Gateway")

	gw.cancel()

	// Stop server first (stop accepting new connections)
	if err := gw.server.Stop(); err != nil {
		gw.logger.Errorw("Error stopping server", "error", err)
	}

	// Stop DRA pool
	if err := gw.draPool.Close(); err != nil {
		gw.logger.Errorw("Error closing DRA pool", "error", err)
	}

	// Wait for goroutines to finish
	gw.wg.Wait()

	stats := gw.GetStats()
	gw.logger.Infow("Gateway stopped",
		"total_requests", stats.TotalRequests,
		"total_responses", stats.TotalResponses,
		"total_errors", stats.TotalErrors,
		"avg_latency_ms", stats.AverageLatencyMs)

	return nil
}

// monitorLogicAppConnections monitors Logic App connections and forwards their requests
func (gw *Gateway) monitorLogicAppConnections() {
	defer gw.wg.Done()

	gw.logger.Infow("Starting Logic App connection monitor")

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-gw.ctx.Done():
			return
		case <-ticker.C:
			// Get all active connections from server
			connections := gw.server.GetAllConnections()
			for _, conn := range connections {
				// Start monitoring this connection if not already
				gw.startConnectionHandler(conn)
			}
		}
	}
}

// connectionHandlers tracks which connections are being monitored
var connectionHandlers sync.Map // key: remoteAddr string, value: bool

// startConnectionHandler starts handling messages from a Logic App connection
func (gw *Gateway) startConnectionHandler(conn *server.Connection) {
	remoteAddr := conn.RemoteAddr().String()

	// Check if already handling
	if _, loaded := connectionHandlers.LoadOrStore(remoteAddr, true); loaded {
		return
	}

	// Start handler goroutine
	gw.wg.Add(1)
	go func() {
		defer gw.wg.Done()
		defer connectionHandlers.Delete(remoteAddr)

		gw.logger.Infow("Started connection handler",
			"remote_addr", remoteAddr,
			"origin_host", conn.GetOriginHost(),
			"interfaces", conn.GetInterfaceNames())

		recvChan := conn.Receive()

		for {
			select {
			case <-gw.ctx.Done():
				return
			case msg, ok := <-recvChan:
				if !ok {
					gw.logger.Infow("Connection closed", "remote_addr", remoteAddr)
					return
				}

				// Handle request from Logic App
				if err := gw.handleLogicAppRequest(conn, msg); err != nil {
					gw.logger.Errorw("Failed to handle request",
						"remote_addr", remoteAddr,
						"error", err)
					gw.stats.TotalErrors.Add(1)
				}
			}
		}
	}()
}

// handleLogicAppRequest handles a request from a Logic App
func (gw *Gateway) handleLogicAppRequest(conn *server.Connection, msg []byte) error {
	if len(msg) < 20 {
		return fmt.Errorf("message too short")
	}

	gw.stats.TotalRequests.Add(1)

	// Parse message header
	msgInfo, err := client.ParseMessageHeader(msg)
	if err != nil {
		gw.stats.TotalErrors.Add(1)
		return fmt.Errorf("failed to parse message header: %w", err)
	}

	// Skip base protocol messages (CER, CEA, DWR, DWA, DPR, DPA)
	// These are already handled by the server connection
	if msgInfo.ApplicationID == 0 && (msgInfo.CommandCode == 257 || msgInfo.CommandCode == 280 || msgInfo.CommandCode == 282) {
		gw.logger.Debugw("Skipping base protocol message",
			"command_code", msgInfo.CommandCode,
			"remote_addr", conn.RemoteAddr().String())
		return nil
	}

	// Only forward requests (not responses)
	if !msgInfo.Flags.Request {
		gw.logger.Warnw("Received response from Logic App (unexpected)",
			"command_code", msgInfo.CommandCode,
			"h2h", msgInfo.HopByHopID)
		return nil
	}

	if gw.config.EnableRequestLogging {
		gw.logger.Infow("Request from Logic App",
			"remote_addr", conn.RemoteAddr().String(),
			"app_id", msgInfo.ApplicationID,
			"cmd_code", msgInfo.CommandCode,
			"h2h", msgInfo.HopByHopID,
			"e2e", msgInfo.EndToEndID)
	}

	// Generate new Hop-by-Hop ID for DRA request
	newHopByHopID := gw.generateHopByHopID()

	// Create session
	session := &Session{
		LogicAppRemoteAddr:  conn.RemoteAddr().String(),
		LogicAppConnection:  conn,
		OriginalHopByHopID:  msgInfo.HopByHopID,
		OriginalEndToEndID:  msgInfo.EndToEndID,
		OriginalCommandCode: msgInfo.CommandCode,
		OriginalAppID:       msgInfo.ApplicationID,
		DRAHopByHopID:       newHopByHopID,
		CreatedAt:           time.Now(),
	}

	// Store session
	gw.sessionsMu.Lock()
	gw.sessions[newHopByHopID] = session
	gw.sessionsMu.Unlock()

	gw.stats.ActiveSessions.Add(1)
	gw.stats.SessionsCreated.Add(1)

	// Modify message: replace Hop-by-Hop ID but preserve End-to-End ID
	newMsg := make([]byte, len(msg))
	copy(newMsg, msg)
	binary.BigEndian.PutUint32(newMsg[12:16], newHopByHopID)

	// Forward to DRA pool
	if err := gw.draPool.Send(newMsg); err != nil {
		gw.sessionsMu.Lock()
		delete(gw.sessions, newHopByHopID)
		gw.sessionsMu.Unlock()
		gw.stats.ActiveSessions.Add(^uint64(0)) // Decrement
		gw.stats.RoutingErrors.Add(1)
		return fmt.Errorf("failed to forward to DRA: %w", err)
	}

	gw.stats.TotalForwarded.Add(1)

	return nil
}

// processDRAResponses processes responses from DRA servers
func (gw *Gateway) processDRAResponses() {
	defer gw.wg.Done()

	gw.logger.Infow("Starting DRA response processor")

	draRecvChan := gw.draPool.Receive()

	for {
		select {
		case <-gw.ctx.Done():
			return
		case msg, ok := <-draRecvChan:
			if !ok {
				gw.logger.Warnw("DRA receive channel closed")
				return
			}

			if err := gw.handleDRAResponse(msg); err != nil {
				gw.logger.Errorw("Failed to handle DRA response", "error", err)
				gw.stats.TotalErrors.Add(1)
			}
		}
	}
}

// handleDRAResponse handles a response from DRA
func (gw *Gateway) handleDRAResponse(msg []byte) error {
	if len(msg) < 20 {
		return fmt.Errorf("message too short")
	}

	gw.stats.TotalFromDRA.Add(1)

	// Parse message header
	msgInfo, err := client.ParseMessageHeader(msg)
	if err != nil {
		return fmt.Errorf("failed to parse message header: %w", err)
	}

	// Find session by DRA Hop-by-Hop ID
	gw.sessionsMu.RLock()
	session, exists := gw.sessions[msgInfo.HopByHopID]
	gw.sessionsMu.RUnlock()

	if !exists {
		gw.logger.Warnw("No session found for DRA response",
			"h2h", msgInfo.HopByHopID,
			"cmd_code", msgInfo.CommandCode)
		return fmt.Errorf("no session found for H2H %d", msgInfo.HopByHopID)
	}

	// Calculate latency
	session.ResponseAt = time.Now()
	latencyMs := session.ResponseAt.Sub(session.CreatedAt).Milliseconds()
	gw.stats.TotalLatencyMs.Add(uint64(latencyMs))
	gw.stats.RequestCount.Add(1)

	if gw.config.EnableResponseLogging {
		gw.logger.Infow("Response from DRA",
			"app_id", msgInfo.ApplicationID,
			"cmd_code", msgInfo.CommandCode,
			"h2h", msgInfo.HopByHopID,
			"e2e", msgInfo.EndToEndID,
			"latency_ms", latencyMs)
	}

	// Restore original Hop-by-Hop ID and End-to-End ID
	restoredMsg := make([]byte, len(msg))
	copy(restoredMsg, msg)
	binary.BigEndian.PutUint32(restoredMsg[12:16], session.OriginalHopByHopID)
	binary.BigEndian.PutUint32(restoredMsg[16:20], session.OriginalEndToEndID)

	// Forward response to Logic App
	if err := session.LogicAppConnection.Send(restoredMsg); err != nil {
		gw.logger.Errorw("Failed to forward response to Logic App",
			"remote_addr", session.LogicAppRemoteAddr,
			"error", err)
		return fmt.Errorf("failed to forward to Logic App: %w", err)
	}

	gw.stats.TotalResponses.Add(1)
	gw.stats.SessionsCompleted.Add(1)

	// Clean up session
	gw.sessionsMu.Lock()
	delete(gw.sessions, msgInfo.HopByHopID)
	gw.sessionsMu.Unlock()
	gw.stats.ActiveSessions.Add(^uint64(0)) // Decrement

	return nil
}

// sessionCleanup periodically cleans up expired sessions
func (gw *Gateway) sessionCleanup() {
	defer gw.wg.Done()

	ticker := time.NewTicker(gw.config.SessionTimeout / 2)
	defer ticker.Stop()

	for {
		select {
		case <-gw.ctx.Done():
			return
		case <-ticker.C:
			gw.cleanupExpiredSessions()
		}
	}
}

// cleanupExpiredSessions removes sessions that have timed out
func (gw *Gateway) cleanupExpiredSessions() {
	now := time.Now()
	expiredCount := 0

	gw.sessionsMu.Lock()
	for h2hID, session := range gw.sessions {
		if now.Sub(session.CreatedAt) > gw.config.SessionTimeout {
			delete(gw.sessions, h2hID)
			expiredCount++
			gw.stats.ActiveSessions.Add(^uint64(0)) // Decrement
			gw.stats.TimeoutErrors.Add(1)
			gw.stats.SessionsExpired.Add(1)
		}
	}
	gw.sessionsMu.Unlock()

	if expiredCount > 0 {
		gw.logger.Warnw("Cleaned up expired sessions", "count", expiredCount)
	}
}

// generateHopByHopID generates a unique Hop-by-Hop ID for DRA requests
var globalHopByHopID atomic.Uint32

func (gw *Gateway) generateHopByHopID() uint32 {
	return globalHopByHopID.Add(1)
}

// GetStats returns a snapshot of gateway statistics
func (gw *Gateway) GetStats() GatewayStatsSnapshot {
	reqCount := gw.stats.RequestCount.Load()
	avgLatency := 0.0
	if reqCount > 0 {
		avgLatency = float64(gw.stats.TotalLatencyMs.Load()) / float64(reqCount)
	}

	return GatewayStatsSnapshot{
		TotalRequests:     gw.stats.TotalRequests.Load(),
		TotalResponses:    gw.stats.TotalResponses.Load(),
		TotalErrors:       gw.stats.TotalErrors.Load(),
		ActiveSessions:    gw.stats.ActiveSessions.Load(),
		TotalForwarded:    gw.stats.TotalForwarded.Load(),
		TotalFromDRA:      gw.stats.TotalFromDRA.Load(),
		TimeoutErrors:     gw.stats.TimeoutErrors.Load(),
		RoutingErrors:     gw.stats.RoutingErrors.Load(),
		SessionsCreated:   gw.stats.SessionsCreated.Load(),
		SessionsCompleted: gw.stats.SessionsCompleted.Load(),
		SessionsExpired:   gw.stats.SessionsExpired.Load(),
		AverageLatencyMs:  avgLatency,
	}
}

// GetServer returns the inbound server
func (gw *Gateway) GetServer() *server.Server {
	return gw.server
}

// GetDRAPool returns the DRA pool
func (gw *Gateway) GetDRAPool() *client.DRAPool {
	return gw.draPool
}

// validateGatewayConfig validates gateway configuration
func validateGatewayConfig(config *GatewayConfig) error {
	if config == nil {
		return fmt.Errorf("config cannot be nil")
	}
	if config.OriginHost == "" {
		return fmt.Errorf("OriginHost is required")
	}
	if config.OriginRealm == "" {
		return fmt.Errorf("OriginRealm is required")
	}
	if config.DRAPoolConfig == nil {
		return fmt.Errorf("DRAPoolConfig is required")
	}
	if config.SessionTimeout <= 0 {
		config.SessionTimeout = 30 * time.Second
	}
	return nil
}

// DefaultGatewayConfig returns default gateway configuration
func DefaultGatewayConfig() *GatewayConfig {
	return &GatewayConfig{
		ServerConfig:          server.DefaultServerConfig(),
		OriginHost:            "diameter-gw.example.com",
		OriginRealm:           "example.com",
		ProductName:           "Diameter-Gateway",
		VendorID:              10415,
		SessionTimeout:        30 * time.Second,
		EnableRequestLogging:  false,
		EnableResponseLogging: false,
	}
}
