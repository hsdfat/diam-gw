package gateway

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hsdfat/diam-gw/client"
	"github.com/hsdfat/diam-gw/pkg/connection"
	"github.com/hsdfat/diam-gw/pkg/logger"
	"github.com/hsdfat/diam-gw/server"
)

// Gateway acts as a Diameter proxy between Logic Apps and DRA servers
// Flow: Logic App <-> Gateway <-> DRA
type Gateway struct {
	config *GatewayConfig

	// Inbound server for Logic App connections
	inServer *server.Server
	inClient *client.AddressClient

	outServer *server.Server

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

func (g *Gateway) GetInServer() *server.Server {
	return g.inServer
}

func (g *Gateway) GetOutServer() *server.Server {
	return g.outServer
}

func (g *Gateway) SetInHandler(h map[connection.Command]connection.Handler) {
	g.inServer.HandlerMux = h
}

func (g *Gateway) RegisterInServer(cmd connection.Command, handler connection.Handler) {
	if g.inServer.HandlerMux == nil {
		g.inServer.HandlerMux = map[server.Command]server.Handler{}
	}
	g.inServer.HandlerMux[cmd] = handler
}
func (g *Gateway) RegisterOutServer(cmd connection.Command, handler connection.Handler) {
	if g.outServer.HandlerMux == nil {
		g.outServer.HandlerMux = map[server.Command]server.Handler{}
	}
	g.outServer.HandlerMux[cmd] = handler
}

func (g *Gateway) SetOutHandler(h map[connection.Command]connection.Handler) {
	g.outServer.HandlerMux = h
}

func (g *Gateway) SetDraPoolHanlder(h map[connection.Command]connection.Handler) {
	g.draPool.SetHandler(h)
}

func (g *Gateway) RegisterDraPoolServer(cmd connection.Command, handler connection.Handler) {
	if g.draPool.GetHandler() == nil {
		g.draPool.SetHandler(map[server.Command]server.Handler{})
	}
	g.draPool.GetHandler()[cmd] = handler
}

// GatewayConfig holds gateway configuration
type GatewayConfig struct {
	// Server configuration (inbound from Logic Apps)
	InServerConfig     *server.ServerConfig
	InClientConfig     *client.PoolConfig
	OutServerConfig    *server.ServerConfig
	OutServerSupported bool
	// DRA pool configuration (outbound to DRA servers)
	DRASupported  bool
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
	Conn connection.Conn

	// Timestamps
	CreatedAt  time.Time
	ResponseAt time.Time

	ResponseChan chan *connection.Message
}

// GatewayStats tracks gateway statistics
type GatewayStats struct {
	// Request/Response counters
	TotalRequests     atomic.Uint64
	TotalResponses    atomic.Uint64
	TotalErrors       atomic.Uint64
	ActiveSessions    atomic.Uint64
	TotalForwarded    atomic.Uint64
	TotalFromDRA      atomic.Uint64
	TimeoutErrors     atomic.Uint64
	RoutingErrors     atomic.Uint64
	SessionsCreated   atomic.Uint64
	SessionsCompleted atomic.Uint64
	SessionsExpired   atomic.Uint64

	// Latency tracking
	TotalLatencyMs atomic.Uint64
	RequestCount   atomic.Uint64
}

// GatewayStatsSnapshot is a snapshot of gateway statistics
type GatewayStatsSnapshot struct {
	InServer  server.ServerStatsSnapshot
	InClient  client.AddressClientStatsSnapshot
	OutServer server.ServerStatsSnapshot
	DraPool   client.DRAPoolStats

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

func (g *Gateway) newServer(config *GatewayConfig, serverCfg *server.ServerConfig, log logger.Logger) *server.Server {
	if serverCfg == nil {
		serverCfg = server.DefaultServerConfig()
	}
	// Override connection config to use gateway's identity
	if serverCfg.ConnectionConfig == nil {
		serverCfg.ConnectionConfig = server.DefaultConnectionConfig()
	}
	serverCfg.ConnectionConfig.OriginHost = config.OriginHost
	serverCfg.ConnectionConfig.OriginRealm = config.OriginRealm
	serverCfg.ConnectionConfig.ProductName = config.ProductName
	serverCfg.ConnectionConfig.VendorID = config.VendorID
	serverCfg.ConnectionConfig.HandleWatchdog = true // Gateway handles DWR locally

	return server.NewServer(serverCfg, log)
}

// NewGateway creates a new Diameter Gateway
func NewGateway(config *GatewayConfig, log logger.Logger) (*Gateway, error) {
	var err error
	if err := ValidateGatewayConfig(config); err != nil {
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

	inServerLog := log.With("components", "inbound-server").(logger.Logger)
	inClientLog := log.With("components", "inbound-client").(logger.Logger)
	outServerLog := log.With("components", "outbound-server").(logger.Logger)
	draPool := log.With("components", "dra-pool").(logger.Logger)
	// Create servers
	gw.inServer = gw.newServer(config, config.InServerConfig, inServerLog)
	gw.inClient, err = client.NewAddressClient(ctx, config.InClientConfig, inClientLog)
	if err != nil {
		panic(err)
	}
	if config.OutServerSupported {
		gw.outServer = gw.newServer(config, config.OutServerConfig, outServerLog)
	}
	// Create DRA pool
	if gw.config.DRASupported {
		draPoolConfig := config.DRAPoolConfig
		if draPoolConfig == nil {
			return nil, fmt.Errorf("DRAPoolConfig is required")
		}
		// Use gateway's identity for DRA connections
		draPoolConfig.OriginHost = config.OriginHost
		draPoolConfig.OriginRealm = config.OriginRealm
		draPoolConfig.ProductName = config.ProductName
		draPoolConfig.VendorID = config.VendorID

		draPool, err := client.NewDRAPool(ctx, draPoolConfig, draPool)
		draPool.SetHandler(make(map[connection.Command]client.Handler))
		if err != nil {
			cancel()
			return nil, fmt.Errorf("failed to create DRA pool: %w", err)
		}
		gw.draPool = draPool
	}

	return gw, nil
}

// Start starts the gateway
func (gw *Gateway) Start() error {
	gw.logger.Infow("Starting Diameter Gateway",
		"origin_host", gw.config.OriginHost,
		"origin_realm", gw.config.OriginRealm)

	// Start DRA pool first
	if gw.config.DRASupported {
		if err := gw.draPool.Start(); err != nil {
			return fmt.Errorf("failed to start DRA pool: %w", err)
		}
		gw.logger.Infow("DRA pool started", "active_priority", gw.draPool.GetActivePriority())
		// Start message processor for DRA responses
		gw.wg.Add(1)
	}

	// Start session cleanup
	gw.wg.Add(1)
	go gw.sessionCleanup()

	// Start server in a goroutine
	gw.wg.Add(1)
	go func() {
		defer gw.wg.Done()
		if err := gw.inServer.Start(); err != nil {
			gw.logger.Errorw("Server in server failed", "error", err)
		}
	}()
	if gw.config.OutServerSupported {
		gw.wg.Add(1)
		go func() {
			defer gw.wg.Done()
			if err := gw.outServer.Start(); err != nil {
				gw.logger.Errorw("Server out server failed", "error", err)
			}
		}()
	}

	// Wait for server to be ready
	time.Sleep(100 * time.Millisecond)

	// Start monitoring Logic App connections
	gw.logger.Infow("Gateway started successfully")

	return nil
}

// Stop gracefully stops the gateway
func (gw *Gateway) Stop() error {
	gw.logger.Infow("Stopping Gateway")

	gw.cancel()

	// Stop server first (stop accepting new connections)
	if err := gw.inServer.Stop(); err != nil {
		gw.logger.Errorw("Error stopping inbound server", "error", err)
	}

	if gw.config.OutServerSupported {
		// Stop server first (stop accepting new connections)
		if err := gw.outServer.Stop(); err != nil {
			gw.logger.Errorw("Error stopping outbound server", "error", err)
		}
	}
	if gw.config.DRASupported {
		// Stop DRA pool
		if err := gw.draPool.Close(); err != nil {
			gw.logger.Errorw("Error closing DRA pool", "error", err)
		}
		gw.wg.Done()
	}
	// Wait for goroutines to finish
	gw.wg.Wait()

	stats := gw.GetStats()
	gw.logger.Infow("Gateway stopped",
		"avg_latency_ms", stats.AverageLatencyMs)

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

func (gw *Gateway) SendInternal(remote string, req []byte) ([]byte, error) {
	if gw.inClient == nil {
		return nil, fmt.Errorf("not found client pool")
	}
	return gw.inClient.SendWithTimeout(remote, req, 3*time.Second)
}

func (gw *Gateway) StoreSession(hopbyhop uint32, s *Session) {
	gw.sessionsMu.Lock()
	gw.sessions[hopbyhop] = s
	gw.sessionsMu.Unlock()
}

func (gw *Gateway) FindSession(hopbyhop uint32) (*Session, error) {
	gw.sessionsMu.RLock()
	s, ok := gw.sessions[hopbyhop]
	gw.sessionsMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("not found session")
	}
	return s, nil
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

// GetStats returns a snapshot of gateway statistics
func (gw *Gateway) GetStats() GatewayStatsSnapshot {
	reqCount := gw.stats.RequestCount.Load()
	avgLatency := 0.0
	if reqCount > 0 {
		avgLatency = float64(gw.stats.TotalLatencyMs.Load()) / float64(reqCount)
	}

	snapshot := GatewayStatsSnapshot{
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
	snapshot.InServer = gw.inServer.GetStats()
	if gw.inClient != nil {
		snapshot.InClient = gw.inClient.GetStats()
	}
	if gw.config.OutServerSupported {
		snapshot.OutServer = gw.outServer.GetStats()
	}
	if gw.config.DRASupported {
		snapshot.DraPool = gw.draPool.GetStats()
	}
	return snapshot
}

// GetDRAPool returns the DRA pool
func (gw *Gateway) GetDRAPool() *client.DRAPool {
	return gw.draPool
}

// ValidateGatewayConfig validates gateway configuration
func ValidateGatewayConfig(config *GatewayConfig) error {
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
		InServerConfig:        server.DefaultServerConfig(),
		OutServerConfig:       server.DefaultServerConfig(),
		OriginHost:            "diameter-gw.example.com",
		OriginRealm:           "example.com",
		ProductName:           "Diameter-Gateway",
		VendorID:              10415,
		SessionTimeout:        30 * time.Second,
		EnableRequestLogging:  false,
		EnableResponseLogging: false,
	}
}
