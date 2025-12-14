package gateway

import (
	"context"
	"encoding/binary"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hsdfat8/diam-gw/client"
	"github.com/hsdfat8/diam-gw/pkg/logger"
	"github.com/hsdfat8/diam-gw/pkg/metrics"
	"github.com/hsdfat8/diam-gw/server"
)

// Gateway handles bidirectional message routing between applications and DRA
type Gateway struct {
	config    *Config
	appServer *server.Server  // Server to accept application connections
	appPool   *client.DRAPool // Client pool to connect to applications (NEW for bidirectional)
	draPool   *client.DRAPool // Client pool to connect to DRAs
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	stats     Stats
	// Message routing maps
	draToAppMap      map[uint32]interface{} // H2H ID -> App Connection (either *server.Connection or app pool)
	appToDraMap      map[uint32]uint32      // App H2H ID -> DRA H2H ID
	routingMu        sync.RWMutex
	// Connection affinity: track which app connection sent which request
	requestToConnMap map[uint32]interface{} // Request H2H -> Originating App Connection (either *server.Connection or app pool)
	// Interface-based routing: map app IDs to app connections
	interfaceRouting map[uint32][]*server.Connection // App ID -> List of connections supporting it (server mode only)
	interfaceRouteMu sync.RWMutex
}

// Config holds gateway configuration
type Config struct {
	Name            string
	AppServerConfig *server.ServerConfig
	AppPoolConfig   *client.DRAPoolConfig // NEW: Client pool to connect TO applications
	DRAPoolConfig   *client.DRAPoolConfig
	MessageTimeout  time.Duration
	EnableMetrics   bool
	MetricsInterval time.Duration
}

// Stats tracks gateway statistics
type Stats struct {
	AppToDRA       atomic.Uint64 // Total messages including protocol
	DRAToApp       atomic.Uint64 // Total messages including protocol
	AppToDRAApp    atomic.Uint64 // Application messages only (excluding CER/CEA, DWR/DWA)
	DRAToAppApp    atomic.Uint64 // Application messages only (excluding CER/CEA, DWR/DWA)
	TotalAppConns  atomic.Uint64
	ActiveAppConns atomic.Uint64
	RoutingErrors  atomic.Uint64
	// Per-message-type metrics
	AppToDRAMetrics *metrics.MessageTypeMetrics
	DRAToAppMetrics *metrics.MessageTypeMetrics
}

// DefaultConfig returns default gateway configuration
func DefaultConfig() *Config {
	return &Config{
		Name:            "diameter-gateway",
		AppServerConfig: server.DefaultServerConfig(),
		DRAPoolConfig:   defaultDRAPoolConfig(),
		MessageTimeout:  10 * time.Second,
		EnableMetrics:   true,
		MetricsInterval: 30 * time.Second,
	}
}

func defaultDRAPoolConfig() *client.DRAPoolConfig {
	return &client.DRAPoolConfig{
		DRAs:                []*client.DRAServerConfig{},
		OriginHost:          "gateway.example.com",
		OriginRealm:         "example.com",
		ProductName:         "Diameter Gateway",
		VendorID:            10415,
		ConnectionsPerDRA:   2,
		ConnectTimeout:      5 * time.Second,
		CERTimeout:          5 * time.Second,
		DWRInterval:         30 * time.Second,
		DWRTimeout:          10 * time.Second,
		MaxDWRFailures:      3,
		HealthCheckInterval: 5 * time.Second,
		ReconnectInterval:   5 * time.Second,
		MaxReconnectDelay:   60 * time.Second,
		ReconnectBackoff:    1.5,
		SendBufferSize:      100,
		RecvBufferSize:      1000,
	}
}

// New creates a new gateway
func New(config *Config) (*Gateway, error) {
	if config == nil {
		config = DefaultConfig()
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Create application server (optional - for apps connecting TO gateway)
	var appServer *server.Server
	if config.AppServerConfig != nil {
		appServer = server.NewServer(config.AppServerConfig, logger.Log)
	}

	// Create application client pool (optional - for gateway connecting TO apps)
	var appPool *client.DRAPool
	if config.AppPoolConfig != nil {
		var err error
		appPool, err = client.NewDRAPool(ctx, config.AppPoolConfig)
		if err != nil {
			cancel()
			return nil, fmt.Errorf("failed to create app pool: %w", err)
		}
	}

	// Create DRA pool
	draPool, err := client.NewDRAPool(ctx, config.DRAPoolConfig)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create DRA pool: %w", err)
	}

	gw := &Gateway{
		config:           config,
		appServer:        appServer,
		appPool:          appPool,
		draPool:          draPool,
		ctx:              ctx,
		cancel:           cancel,
		draToAppMap:      make(map[uint32]interface{}),
		appToDraMap:      make(map[uint32]uint32),
		requestToConnMap: make(map[uint32]interface{}),
		interfaceRouting: make(map[uint32][]*server.Connection),
		stats: Stats{
			AppToDRAMetrics: metrics.NewMessageTypeMetrics(),
			DRAToAppMetrics: metrics.NewMessageTypeMetrics(),
		},
	}

	return gw, nil
}

// Start starts the gateway
func (gw *Gateway) Start() error {
	logger.Log.Infow("Starting gateway", "name", gw.config.Name)

	// Start DRA pool
	if err := gw.draPool.Start(); err != nil {
		return fmt.Errorf("failed to start DRA pool: %w", err)
	}
	logger.Log.Infow("DRA pool started")

	// Start application pool (if configured - gateway connecting TO apps)
	if gw.appPool != nil {
		if err := gw.appPool.Start(); err != nil {
			gw.draPool.Close()
			return fmt.Errorf("failed to start app pool: %w", err)
		}
		logger.Log.Infow("Application pool started (bidirectional mode)")
	}

	// Start application server (if configured - apps connecting TO gateway)
	if gw.appServer != nil {
		if err := gw.appServer.Start(); err != nil {
			if gw.appPool != nil {
				gw.appPool.Close()
			}
			gw.draPool.Close()
			return fmt.Errorf("failed to start application server: %w", err)
		}
		logger.Log.Infow("Application server started", "address", gw.config.AppServerConfig.ListenAddress)
	}

	// Start message routers
	routerCount := 2
	if gw.appServer != nil {
		routerCount = 3 // Add monitor connections only if server mode
	}
	if gw.appPool != nil {
		routerCount++ // Add app pool receiver
	}

	gw.wg.Add(routerCount)
	go gw.routeAppToDRA()
	go gw.routeDRAToApp()

	if gw.appServer != nil {
		go gw.monitorConnections() // Monitor app connections for interface updates
	}

	if gw.appPool != nil {
		go gw.routeAppPoolToDRA() // Route messages from app pool to DRA
	}

	// Start metrics reporter
	if gw.config.EnableMetrics {
		gw.wg.Add(1)
		go gw.metricsLoop()
	}

	logger.Log.Infow("Gateway started successfully")
	return nil
}

// monitorConnections monitors application connections and updates interface routing
func (gw *Gateway) monitorConnections() {
	defer gw.wg.Done()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-gw.ctx.Done():
			return
		case <-ticker.C:
			gw.updateInterfaceRouting()
		}
	}
}

// updateInterfaceRouting rebuilds the interface routing map based on current active connections
func (gw *Gateway) updateInterfaceRouting() {
	allConns := gw.appServer.GetAllConnections()

	// Filter for only active connections
	activeConns := make([]*server.Connection, 0, len(allConns))
	for _, conn := range allConns {
		if conn.GetState() == client.StateOpen {
			activeConns = append(activeConns, conn)
		}
	}

	newRouting := make(map[uint32][]*server.Connection)

	for _, conn := range activeConns {
		appIDs := conn.GetSupportedAppIDs()
		for _, appID := range appIDs {
			newRouting[appID] = append(newRouting[appID], conn)
		}
	}

	gw.interfaceRouteMu.Lock()
	gw.interfaceRouting = newRouting
	gw.interfaceRouteMu.Unlock()

	// Log interface summary
	if len(newRouting) > 0 {
		logger.Log.Debugw("Interface routing updated",
			"total_connections", len(allConns),
			"active_connections", len(activeConns))
		for appID, connsForApp := range newRouting {
			interfaceName := getInterfaceName(appID)
			logger.Log.Debugw("Interface routing",
				"interface", interfaceName,
				"app_id", appID,
				"connections", len(connsForApp))
		}
	}
}

// getConnectionForInterface returns an active connection supporting the given application ID
func (gw *Gateway) getConnectionForInterface(appID uint32) *server.Connection {
	gw.interfaceRouteMu.RLock()
	defer gw.interfaceRouteMu.RUnlock()

	conns, exists := gw.interfaceRouting[appID]
	if !exists || len(conns) == 0 {
		return nil
	}

	// Find first active connection
	for _, conn := range conns {
		if conn.GetState() == client.StateOpen {
			return conn
		}
	}

	return nil
}

// getInterfaceName returns human-readable interface name
func getInterfaceName(appID uint32) string {
	switch appID {
	case 16777251:
		return "S6a"
	case 16777252:
		return "S13"
	case 16777238:
		return "Gx"
	case 16777236:
		return "Cx"
	case 16777216:
		return "Sh"
	default:
		return fmt.Sprintf("App-%d", appID)
	}
}

// getApplicationIDForCommand maps Diameter command codes to Application-IDs
func (gw *Gateway) getApplicationIDForCommand(commandCode uint32) uint32 {
	switch commandCode {
	// S13 Interface (Equipment Check)
	case 324: // ME-Identity-Check (MICR/MICA)
		return 16777252

	// S6a Interface (Authentication & Subscription)
	case 318: // Update-Location (ULR/ULA)
		return 16777251
	case 316: // Authentication-Information (AIR/AIA)
		return 16777251
	case 321: // Purge-UE (PUR/PUA)
		return 16777251
	case 323: // Notify (NOR/NOA)
		return 16777251

	// Gx Interface (Policy Control)
	case 272: // Credit-Control (CCR/CCA) when used for Gx
		// Note: CCR is also used by Gy (16777238), would need to check AVPs to distinguish
		return 16777238

	// Cx Interface (HSS to I-CSCF/S-CSCF)
	case 300: // User-Authorization (UAR/UAA)
		return 16777216
	case 301: // Server-Assignment (SAR/SAA)
		return 16777216
	case 302: // Location-Info (LIR/LIA)
		return 16777216
	case 303: // Multimedia-Auth (MAR/MAA)
		return 16777216

	// Sh Interface (HSS to AS)
	case 306: // User-Data (UDR/UDA)
		return 16777217
	case 307: // Profile-Update (PUR/PUA)
		return 16777217
	case 308: // Subscribe-Notifications (SNR/SNA)
		return 16777217
	case 309: // Push-Notification (PNR/PNA)
		return 16777217

	default:
		// Unknown command code - no specific interface
		return 0
	}
}

// Stop stops the gateway
func (gw *Gateway) Stop() error {
	logger.Log.Infow("Stopping gateway...")
	gw.cancel()

	// Stop application server
	if gw.appServer != nil {
		if err := gw.appServer.Stop(); err != nil {
			logger.Log.Errorw("Failed to stop application server", "error", err)
		}
	}

	// Stop application pool
	if gw.appPool != nil {
		if err := gw.appPool.Close(); err != nil {
			logger.Log.Errorw("Failed to stop application pool", "error", err)
		}
	}

	// Stop DRA pool
	if err := gw.draPool.Close(); err != nil {
		logger.Log.Errorw("Failed to stop DRA pool", "error", err)
	}

	gw.wg.Wait()

	logger.Log.Infow("Gateway stopped",
		"app_to_dra", gw.stats.AppToDRA.Load(),
		"dra_to_app", gw.stats.DRAToApp.Load(),
		"errors", gw.stats.RoutingErrors.Load())

	return nil
}

// GetStats returns gateway statistics (without copying atomic values)
func (gw *Gateway) GetStats() map[string]interface{} {
	return map[string]interface{}{
		"app_to_dra":             gw.stats.AppToDRA.Load(),
		"dra_to_app":             gw.stats.DRAToApp.Load(),
		"app_to_dra_app":         gw.stats.AppToDRAApp.Load(),
		"dra_to_app_app":         gw.stats.DRAToAppApp.Load(),
		"total_app_connections":  gw.stats.TotalAppConns.Load(),
		"active_app_connections": gw.stats.ActiveAppConns.Load(),
		"routing_errors":         gw.stats.RoutingErrors.Load(),
	}
}

// routeAppToDRA routes messages from application server to DRA
func (gw *Gateway) routeAppToDRA() {
	defer gw.wg.Done()
	defer logger.Log.Infow("App->DRA router exited")

	if gw.appServer == nil {
		return
	}

	for {
		select {
		case <-gw.ctx.Done():
			return
		case msgCtx, ok := <-gw.appServer.Receive():
			if !ok {
				return
			}

			if err := gw.forwardAppToDRA(msgCtx); err != nil {
				logger.Log.Errorw("Failed to forward App->DRA", "error", err)
				gw.stats.RoutingErrors.Add(1)
			} else {
				gw.stats.AppToDRA.Add(1)
				// Count application messages only (exclude protocol messages)
				if len(msgCtx.Message) >= 20 {
					cmdCode := binary.BigEndian.Uint32(msgCtx.Message[4:8]) & 0x00FFFFFF
					if cmdCode != 257 && cmdCode != 280 && cmdCode != 282 { // Not CER/CEA, DWR/DWA, DPR/DPA
						gw.stats.AppToDRAApp.Add(1)
					}
				}
			}
		}
	}
}

// routeAppPoolToDRA routes messages from application pool to DRA
func (gw *Gateway) routeAppPoolToDRA() {
	defer gw.wg.Done()
	defer logger.Log.Infow("AppPool->DRA router exited")

	if gw.appPool == nil {
		return
	}

	for {
		select {
		case <-gw.ctx.Done():
			return
		case msg, ok := <-gw.appPool.Receive():
			if !ok {
				return
			}

			if err := gw.forwardAppPoolToDRA(msg); err != nil {
				logger.Log.Errorw("Failed to forward AppPool->DRA", "error", err)
				gw.stats.RoutingErrors.Add(1)
			} else {
				gw.stats.AppToDRA.Add(1)
				// Count application messages only (exclude protocol messages)
				if len(msg) >= 20 {
					cmdCode := binary.BigEndian.Uint32(msg[4:8]) & 0x00FFFFFF
					if cmdCode != 257 && cmdCode != 280 && cmdCode != 282 { // Not CER/CEA, DWR/DWA, DPR/DPA
						gw.stats.AppToDRAApp.Add(1)
					}
				}
			}
		}
	}
}

// forwardAppToDRA forwards a message from application to DRA
func (gw *Gateway) forwardAppToDRA(msgCtx *server.MessageContext) error {
	msg := msgCtx.Message
	appConn := msgCtx.Connection

	if len(msg) < 20 {
		return fmt.Errorf("message too short")
	}

	// Parse message header
	msgInfo, err := client.ParseMessageHeader(msg)
	if err != nil {
		return fmt.Errorf("failed to parse header: %w", err)
	}

	// Track per-message-type metric
	gw.stats.AppToDRAMetrics.Increment(msgInfo.CommandCode)

	logger.Log.Debugw("App->DRA",
		"command_code", msgInfo.CommandCode,
		"command_name", metrics.CommandCodeToName(msgInfo.CommandCode),
		"is_request", msgInfo.Flags.Request,
		"h2h", msgInfo.HopByHopID,
		"origin_host", appConn.GetOriginHost())

	// Check if this is a request or response
	if msgInfo.Flags.Request {
		// Store mapping: App H2H -> App Connection for routing response back
		origH2H := msgInfo.HopByHopID

		// Modify H2H ID to gateway's own ID to track the request
		newH2H := gw.getNextHopByHopID()
		binary.BigEndian.PutUint32(msg[12:16], newH2H)

		// Store routing info with CONNECTION AFFINITY
		// This ensures the DRA response comes back to the same app connection
		gw.routingMu.Lock()
		gw.draToAppMap[newH2H] = appConn           // Map gateway H2H -> App Connection
		gw.appToDraMap[origH2H] = newH2H           // Map app H2H -> Gateway H2H
		gw.requestToConnMap[newH2H] = appConn      // Connection affinity tracking
		gw.routingMu.Unlock()

		logger.Log.Debugw("Request routing with connection affinity",
			"app_h2h", origH2H,
			"gw_h2h", newH2H,
			"app_conn", appConn.GetOriginHost())
	}

	// Forward to DRA pool
	if err := gw.draPool.Send(msg); err != nil {
		// Restore original H2H if send failed
		if msgInfo.Flags.Request {
			gw.routingMu.Lock()
			origH2H := uint32(0)
			for appH2H, draH2H := range gw.appToDraMap {
				if draH2H == msgInfo.HopByHopID {
					origH2H = appH2H
					break
				}
			}
			if origH2H != 0 {
				binary.BigEndian.PutUint32(msg[12:16], origH2H)
				delete(gw.appToDraMap, origH2H)
				delete(gw.draToAppMap, msgInfo.HopByHopID)
				delete(gw.requestToConnMap, msgInfo.HopByHopID)
			}
			gw.routingMu.Unlock()
		}
		return fmt.Errorf("failed to send to DRA: %w", err)
	}

	return nil
}

// forwardAppPoolToDRA forwards a message from application pool to DRA
func (gw *Gateway) forwardAppPoolToDRA(msg []byte) error {
	if len(msg) < 20 {
		return fmt.Errorf("message too short")
	}

	// Parse message header
	msgInfo, err := client.ParseMessageHeader(msg)
	if err != nil {
		return fmt.Errorf("failed to parse header: %w", err)
	}

	// Track per-message-type metric
	gw.stats.AppToDRAMetrics.Increment(msgInfo.CommandCode)

	logger.Log.Debugw("AppPool->DRA",
		"command_code", msgInfo.CommandCode,
		"command_name", metrics.CommandCodeToName(msgInfo.CommandCode),
		"is_request", msgInfo.Flags.Request,
		"h2h", msgInfo.HopByHopID)

	// Check if this is a request or response
	if msgInfo.Flags.Request {
		// Store mapping for routing response back to app pool
		origH2H := msgInfo.HopByHopID

		// Modify H2H ID to gateway's own ID to track the request
		newH2H := gw.getNextHopByHopID()
		binary.BigEndian.PutUint32(msg[12:16], newH2H)

		// Store routing info - use app pool marker (nil connection)
		gw.routingMu.Lock()
		gw.draToAppMap[newH2H] = gw.appPool        // Map gateway H2H -> App Pool
		gw.appToDraMap[origH2H] = newH2H           // Map app H2H -> Gateway H2H
		gw.requestToConnMap[newH2H] = gw.appPool   // Connection affinity to pool
		gw.routingMu.Unlock()

		logger.Log.Debugw("Request routing from app pool",
			"app_h2h", origH2H,
			"gw_h2h", newH2H)
	}

	// Forward to DRA pool
	if err := gw.draPool.Send(msg); err != nil {
		// Restore original H2H if send failed
		if msgInfo.Flags.Request {
			gw.routingMu.Lock()
			origH2H := uint32(0)
			for appH2H, draH2H := range gw.appToDraMap {
				if draH2H == msgInfo.HopByHopID {
					origH2H = appH2H
					break
				}
			}
			if origH2H != 0 {
				binary.BigEndian.PutUint32(msg[12:16], origH2H)
				delete(gw.appToDraMap, origH2H)
				delete(gw.draToAppMap, msgInfo.HopByHopID)
				delete(gw.requestToConnMap, msgInfo.HopByHopID)
			}
			gw.routingMu.Unlock()
		}
		return fmt.Errorf("failed to send to DRA: %w", err)
	}

	return nil
}

// routeDRAToApp routes messages from DRA to applications
func (gw *Gateway) routeDRAToApp() {
	defer gw.wg.Done()
	defer logger.Log.Infow("DRA->App router exited")

	for {
		select {
		case <-gw.ctx.Done():
			return
		case msg, ok := <-gw.draPool.Receive():
			if !ok {
				return
			}

			if err := gw.forwardDRAToApp(msg); err != nil {
				logger.Log.Errorw("Failed to forward DRA->App", "error", err)
				gw.stats.RoutingErrors.Add(1)
			} else {
				gw.stats.DRAToApp.Add(1)
				// Count application messages only (exclude protocol messages)
				if len(msg) >= 20 {
					cmdCode := binary.BigEndian.Uint32(msg[4:8]) & 0x00FFFFFF
					if cmdCode != 257 && cmdCode != 280 && cmdCode != 282 { // Not CER/CEA, DWR/DWA, DPR/DPA
						gw.stats.DRAToAppApp.Add(1)
					}
				}
			}
		}
	}
}

// forwardDRAToApp forwards a message from DRA to application
func (gw *Gateway) forwardDRAToApp(msg []byte) error {
	if len(msg) < 20 {
		return fmt.Errorf("message too short")
	}

	// Parse message header
	msgInfo, err := client.ParseMessageHeader(msg)
	if err != nil {
		return fmt.Errorf("failed to parse header: %w", err)
	}

	// Track per-message-type metric
	gw.stats.DRAToAppMetrics.Increment(msgInfo.CommandCode)

	logger.Log.Debugw("DRA->App",
		"command_code", msgInfo.CommandCode,
		"command_name", metrics.CommandCodeToName(msgInfo.CommandCode),
		"is_request", msgInfo.Flags.Request,
		"h2h", msgInfo.HopByHopID)

	// Check if this is a request from DRA (e.g., MICR, AIR, etc.)
	if msgInfo.Flags.Request {
		// DRA initiating request - try app pool first, then server connections
		logger.Log.Infow("DRA initiated request",
			"command_code", msgInfo.CommandCode,
			"command_name", metrics.CommandCodeToName(msgInfo.CommandCode))

		// Store the DRA's H2H for routing response back
		draH2H := msgInfo.HopByHopID
		newH2H := gw.getNextHopByHopID()
		binary.BigEndian.PutUint32(msg[12:16], newH2H)

		// Determine interface (Application-ID) based on command code
		appID := gw.getApplicationIDForCommand(msgInfo.CommandCode)
		interfaceName := getInterfaceName(appID)

		// Strategy: Prefer app pool (bidirectional) over server connections
		if gw.appPool != nil {
			// Use app pool for bidirectional architecture
			logger.Log.Infow("Routing to app pool (bidirectional mode)",
				"interface", interfaceName,
				"app_id", appID)

			// Store routing with app pool marker
			gw.routingMu.Lock()
			gw.draToAppMap[newH2H] = gw.appPool
			gw.appToDraMap[newH2H] = draH2H
			gw.requestToConnMap[newH2H] = gw.appPool
			gw.routingMu.Unlock()

			// Send to application pool
			if err := gw.appPool.Send(msg); err != nil {
				gw.routingMu.Lock()
				delete(gw.draToAppMap, newH2H)
				delete(gw.appToDraMap, newH2H)
				delete(gw.requestToConnMap, newH2H)
				gw.routingMu.Unlock()
				return fmt.Errorf("failed to send to app pool: %w", err)
			}

			return nil
		}

		// Fallback to server mode: Try interface-based routing
		var appConn *server.Connection
		if gw.appServer != nil {
			if appID != 0 {
				appConn = gw.getConnectionForInterface(appID)
				if appConn != nil {
					logger.Log.Infow("Interface-based routing (server mode)",
						"interface", interfaceName,
						"app_id", appID,
						"target_app", appConn.GetOriginHost())
				}
			}

			// Fallback to first available active connection if no interface match
			if appConn == nil {
				conns := gw.appServer.GetAllConnections()
				// Find first active connection
				for _, conn := range conns {
					if conn.GetState() == client.StateOpen {
						appConn = conn
						break
					}
				}
				if appConn != nil {
					logger.Log.Infow("Fallback routing (no interface match)",
						"interface", interfaceName,
						"target_app", appConn.GetOriginHost())
				}
			}
		}

		if appConn == nil {
			return fmt.Errorf("no active application connections available (no app pool or server connections)")
		}

		// Store routing with CONNECTION AFFINITY
		gw.routingMu.Lock()
		gw.draToAppMap[newH2H] = appConn
		gw.appToDraMap[newH2H] = draH2H
		gw.requestToConnMap[newH2H] = appConn
		gw.routingMu.Unlock()

		logger.Log.Debugw("DRA request routing with affinity",
			"dra_h2h", draH2H,
			"app_h2h", newH2H,
			"app_conn", appConn.GetOriginHost(),
			"interface", interfaceName)

		// Send to application
		if err := appConn.Send(msg); err != nil {
			gw.routingMu.Lock()
			delete(gw.draToAppMap, newH2H)
			delete(gw.appToDraMap, newH2H)
			delete(gw.requestToConnMap, newH2H)
			gw.routingMu.Unlock()
			return fmt.Errorf("failed to send to application: %w", err)
		}

		return nil
	}

	// This is a response from DRA to an app-initiated request
	// Use CONNECTION AFFINITY - route back to the SAME connection/pool that sent the request
	gw.routingMu.Lock()
	appTarget, exists := gw.requestToConnMap[msgInfo.HopByHopID]

	// Find original app H2H
	var origAppH2H uint32
	for appH2H, gwH2H := range gw.appToDraMap {
		if gwH2H == msgInfo.HopByHopID {
			origAppH2H = appH2H
			break
		}
	}

	// Clean up routing maps
	if origAppH2H != 0 {
		delete(gw.appToDraMap, origAppH2H)
	}
	delete(gw.draToAppMap, msgInfo.HopByHopID)
	delete(gw.requestToConnMap, msgInfo.HopByHopID)
	gw.routingMu.Unlock()

	if !exists || appTarget == nil {
		return fmt.Errorf("no application connection found for H2H %d (connection affinity)", msgInfo.HopByHopID)
	}

	// Restore original application's H2H ID
	if origAppH2H != 0 {
		binary.BigEndian.PutUint32(msg[12:16], origAppH2H)
	}

	// Check if target is app pool or server connection
	if pool, ok := appTarget.(*client.DRAPool); ok {
		// Send to app pool
		logger.Log.Debugw("Response routing to app pool",
			"gw_h2h", msgInfo.HopByHopID,
			"app_h2h", origAppH2H)

		if err := pool.Send(msg); err != nil {
			return fmt.Errorf("failed to send to app pool: %w", err)
		}
	} else if conn, ok := appTarget.(*server.Connection); ok {
		// Send to server connection
		logger.Log.Debugw("Response routing with connection affinity",
			"gw_h2h", msgInfo.HopByHopID,
			"app_h2h", origAppH2H,
			"app_conn", conn.GetOriginHost())

		if err := conn.Send(msg); err != nil {
			return fmt.Errorf("failed to send to application: %w", err)
		}
	} else {
		return fmt.Errorf("unknown application target type")
	}

	return nil
}

// getNextHopByHopID generates a unique hop-by-hop ID
var globalH2HCounter atomic.Uint32

func (gw *Gateway) getNextHopByHopID() uint32 {
	return globalH2HCounter.Add(1)
}

// metricsLoop periodically logs metrics
func (gw *Gateway) metricsLoop() {
	defer gw.wg.Done()

	ticker := time.NewTicker(gw.config.MetricsInterval)
	defer ticker.Stop()

	for {
		select {
		case <-gw.ctx.Done():
			return
		case <-ticker.C:
			gw.logMetrics()
		}
	}
}

// logMetrics logs current metrics
func (gw *Gateway) logMetrics() {
	appConns := gw.appServer.GetAllConnections()
	draStats := gw.draPool.GetStats()

	logger.Log.Infow("=== Gateway Metrics ===")
	logger.Log.Infow("Application Connections", "count", len(appConns))
	logger.Log.Infow("DRA Status",
		"active", draStats.ActiveDRAs,
		"total", draStats.TotalDRAs,
		"priority", draStats.CurrentPriority)
	logger.Log.Infow("Messages (Total)",
		"app_to_dra", gw.stats.AppToDRA.Load(),
		"dra_to_app", gw.stats.DRAToApp.Load())
	logger.Log.Infow("Messages (Application Only)",
		"app_to_dra", gw.stats.AppToDRAApp.Load(),
		"dra_to_app", gw.stats.DRAToAppApp.Load())
	logger.Log.Infow("Routing Errors", "count", gw.stats.RoutingErrors.Load())

	// Log routing map sizes
	gw.routingMu.RLock()
	logger.Log.Infow("Pending Routes",
		"dra_to_app", len(gw.draToAppMap),
		"app_to_dra", len(gw.appToDraMap))
	gw.routingMu.RUnlock()

	// Log per-message-type metrics
	logger.Log.Infow(metrics.CompactMetrics("App->DRA", gw.stats.AppToDRAMetrics))
	logger.Log.Infow(metrics.CompactMetrics("DRA->App", gw.stats.DRAToAppMetrics))
}
