package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	govclient "github.com/chronnie/governance/client"
	"github.com/chronnie/governance/models"
	"github.com/hsdfat/diam-gw/client"
	"github.com/hsdfat/diam-gw/commands/s13"
	"github.com/hsdfat/diam-gw/gateway"
	"github.com/hsdfat/diam-gw/internal/config"
	diamStats "github.com/hsdfat/diam-gw/internal/stats"
	"github.com/hsdfat/diam-gw/models_base"
	"github.com/hsdfat/diam-gw/pkg/connection"
	"github.com/hsdfat/diam-gw/pkg/logger"
	"github.com/hsdfat/diam-gw/server"
	unifiedStats "github.com/hsdfat/telco/stats"
)

func main() {
	// Load configuration
	cfg, err := config.Load("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	// Initialize logger
	log := logger.New("diam-gw", cfg.Logging.Level)

	// Initialize centralized logging if enabled
	if cfg.Logging.Centralized.Enabled {
		centralizedCfg := &logger.CentralizedConfig{
			Enabled:       cfg.Logging.Centralized.Enabled,
			Backend:       cfg.Logging.Centralized.Backend,
			LokiURL:       cfg.Logging.Centralized.LokiURL,
			HTTPURL:       cfg.Logging.Centralized.HTTPURL,
			TenantID:      cfg.Logging.Centralized.TenantID,
			BearerToken:   cfg.Logging.Centralized.BearerToken,
			BufferSize:    cfg.Logging.Centralized.BufferSize,
			FlushInterval: cfg.Logging.Centralized.FlushInterval,
			MaxBatchSize:  cfg.Logging.Centralized.MaxBatchSize,
			Labels:        cfg.Logging.Centralized.Labels,
		}

		if err := logger.InitializeWithCentralizedLogging(centralizedCfg, cfg.Logging.Level); err != nil {
			log.Warnw("Failed to initialize centralized logging, using console only", "error", err)
		} else {
			log.Infow("Centralized logging initialized",
				"backend", cfg.Logging.Centralized.Backend,
				"url", cfg.Logging.Centralized.LokiURL)
		}

		// Recreate logger after initialization
		log = logger.New("diam-gw", cfg.Logging.Level)
	}

	log.Infow("Starting Diameter Gateway",
		"version", "1.0.0",
		"listen", cfg.Server.ListenAddr,
		"origin_host", cfg.Gateway.OriginHost)

	// Create gateway configuration
	gatewayConfig := createGatewayConfig(cfg)

	// Create gateway
	gw, err := gateway.NewGateway(gatewayConfig, log)
	if err != nil {
		log.Fatalw("Failed to create gateway", "error", err)
	}

	// Start gateway
	if err := gw.Start(); err != nil {
		log.Fatalw("Failed to start gateway", "error", err)
	}

	// Start statistics reporter
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go statsReporter(ctx, gw, log, cfg.Gateway.StatsInterval)

	// Start HTTP stats API server
	startTime := time.Now()
	if cfg.Metrics.Enabled {
		go startStatsServer(cfg.Metrics.Port, gw, startTime, log)
	}

	log.Infow("Gateway is running",
		"listen_address", cfg.Server.ListenAddr,
		"dra_count", len(cfg.DRAPool.DRAs),
		"stats_api_port", cfg.Metrics.Port)

	// Register with governance manager if enabled
	var govClient *govclient.Client
	if cfg.Governance.Enabled {
		govClient = registerWithGovernance(cfg, log)
	}

	gw.RegisterDraPoolServer(connection.Command{
		Code:      324,
		Interface: s13.S13_APPLICATION_ID,
		Request:   true,
	}, func(msg *connection.Message, conn connection.Conn) {
		log.Infow("process micr message")

		// Increment total requests counter (message received from DRA)
		gw.IncrementTotalRequests()

		_, err := connection.ParseMessageHeader(msg.Header)
		if err != nil {
			log.Errorw("cannot parse header", "error", err)
			gw.IncrementTotalErrors()
			return
		}
		eirApps := govClient.GetPodInfos(models.ServiceNameEir, string(models.ProviderEIRDiameter))
		if len(eirApps) == 0 {
			log.Errorw("no EIR Logic App instances available")
			mica := s13.NewMEIdentityCheckAnswer()
			code := models_base.Unsigned32(5001) // DIAMETER_UNABLE_TO_COMPLY
			mica.ResultCode = &code
			msgInfo, err := client.ParseMessageHeader(msg.Header)
			if err != nil {
				log.Errorw("cannot parse header", "error", err)
				gw.IncrementTotalErrors()
				return
			}
			mica.Header.HopByHopID = msgInfo.HopByHopID
			mica.Header.EndToEndID = msgInfo.EndToEndID
			rspBytes, err := mica.Marshal()
			if err != nil {
				log.Errorw("cannot marshal MICA", "error", err)
				gw.IncrementTotalErrors()
				return
			}
			conn.Write(rspBytes)
			gw.IncrementTotalResponses()
			gw.IncrementRoutingErrors()
			return
		}
		// get first EIR Logic App instance from map
		var selectedEIRApp govclient.Pod
		for _, eirApp := range eirApps {
			selectedEIRApp = eirApp
			break
		}
		log.Infow("forwarding to EIR Logic App", "pod", selectedEIRApp.Name, "ip", selectedEIRApp.Ip, "port", selectedEIRApp.Port)

		// Increment forwarded counter (forwarding to EIR)
		gw.IncrementTotalForwarded()

		rsp, err := gw.SendInternal(fmt.Sprintf("%s:%d", selectedEIRApp.Ip, selectedEIRApp.Port), append(msg.Header, msg.Body...))
		if err != nil {
			log.Errorw("cannot send to eir", "err", err)
			gw.IncrementTotalErrors()
			return
		}

		// Increment from_dra counter (response received from EIR, sending back to DRA)
		gw.IncrementTotalFromDRA()
		gw.IncrementTotalResponses()

		conn.Write(rsp)
	})

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	log.Infow("Shutting down gracefully...")
	cancel()

	// Unregister from governance
	if govClient != nil {
		govClient.StopHeartbeat()
		if err := govClient.Unregister(); err != nil {
			log.Error("Failed to unregister from governance", "error", err)
		} else {
			log.Infow("✓ Unregistered from governance manager")
		}
	}

	// Stop gateway
	if err := gw.Stop(); err != nil {
		log.Errorw("Error stopping gateway", "error", err)
	}

	log.Infow("Gateway stopped successfully")
}

func createGatewayConfig(cfg *config.Config) *gateway.GatewayConfig {
	// Server configuration (inbound from Logic Apps)
	inServerConfig := &server.ServerConfig{
		ListenAddress:  cfg.Server.ListenAddr,
		MaxConnections: cfg.Server.MaxConnections,
		ConnectionConfig: &server.ConnectionConfig{
			OriginHost:       cfg.Gateway.OriginHost,
			OriginRealm:      cfg.Gateway.OriginRealm,
			ProductName:      cfg.Gateway.ProductName,
			VendorID:         cfg.Gateway.VendorID,
			ReadTimeout:      cfg.Server.ReadTimeout,
			WriteTimeout:     cfg.Server.WriteTimeout,
			WatchdogInterval: cfg.Server.WatchdogInterval,
			WatchdogTimeout:  cfg.Server.WatchdogTimeout,
			MaxMessageSize:   cfg.Server.MaxMessageSize,
			SendChannelSize:  cfg.Server.SendChannelSize,
			RecvChannelSize:  cfg.Server.RecvChannelSize,
			HandleWatchdog:   cfg.Server.HandleWatchdog,
		},
		RecvChannelSize: cfg.Server.RecvChannelSize,
	}

	// DRA pool configuration (outbound to DRA servers)
	draConfigs := make([]*client.DRAServerConfig, len(cfg.DRAPool.DRAs))
	for i, dra := range cfg.DRAPool.DRAs {
		draConfigs[i] = &client.DRAServerConfig{
			Name:     dra.Name,
			Host:     dra.Host,
			Port:     dra.Port,
			Priority: dra.Priority,
			Weight:   dra.Weight,
		}
	}

	draPoolConfig := &client.DRAPoolConfig{
		DRAs:                draConfigs,
		OriginHost:          cfg.Gateway.OriginHost,
		OriginRealm:         cfg.Gateway.OriginRealm,
		ProductName:         cfg.Gateway.ProductName,
		VendorID:            cfg.Gateway.VendorID,
		ConnectionsPerDRA:   cfg.DRAPool.ConnectionsPerDRA,
		ConnectTimeout:      cfg.DRAPool.ConnectTimeout,
		CERTimeout:          cfg.DRAPool.CERTimeout,
		DWRInterval:         cfg.DRAPool.DWRInterval,
		DWRTimeout:          cfg.DRAPool.DWRTimeout,
		MaxDWRFailures:      cfg.DRAPool.MaxDWRFailures,
		HealthCheckInterval: cfg.DRAPool.HealthCheckInterval,
		ReconnectInterval:   cfg.DRAPool.ReconnectInterval,
		MaxReconnectDelay:   cfg.DRAPool.MaxReconnectDelay,
		ReconnectBackoff:    cfg.DRAPool.ReconnectBackoff,
		SendBufferSize:      cfg.DRAPool.SendBufferSize,
		RecvBufferSize:      cfg.DRAPool.RecvBufferSize,
	}

	// Internal client pool configuration (for forwarding to Logic Apps)
	inClientConfig := &client.PoolConfig{
		OriginHost:          cfg.Gateway.OriginHost,
		OriginRealm:         cfg.Gateway.OriginRealm,
		ProductName:         cfg.Gateway.ProductName,
		VendorID:            cfg.Gateway.VendorID,
		DialTimeout:         cfg.Client.DialTimeout,
		SendTimeout:         cfg.Client.SendTimeout,
		CERTimeout:          cfg.Client.CERTimeout,
		DWRInterval:         cfg.Client.DWRInterval,
		DWRTimeout:          cfg.Client.DWRTimeout,
		MaxDWRFailures:      cfg.Client.MaxDWRFailures,
		AuthAppIDs:          cfg.Client.AuthAppIDs,
		SendBufferSize:      cfg.Client.SendBufferSize,
		RecvBufferSize:      cfg.Client.RecvBufferSize,
		ReconnectEnabled:    cfg.Client.ReconnectEnabled,
		ReconnectInterval:   cfg.Client.ReconnectInterval,
		MaxReconnectDelay:   cfg.Client.MaxReconnectDelay,
		ReconnectBackoff:    cfg.Client.ReconnectBackoff,
		HealthCheckInterval: cfg.Client.HealthCheckInterval,
	}

	return &gateway.GatewayConfig{
		InServerConfig:        inServerConfig,
		DRAPoolConfig:         draPoolConfig,
		InClientConfig:        inClientConfig,
		DRASupported:          cfg.DRAPool.Supported,
		OriginHost:            cfg.Gateway.OriginHost,
		OriginRealm:           cfg.Gateway.OriginRealm,
		ProductName:           cfg.Gateway.ProductName,
		VendorID:              cfg.Gateway.VendorID,
		SessionTimeout:        cfg.Gateway.SessionTimeout,
		EnableRequestLogging:  cfg.Gateway.EnableReqLog,
		EnableResponseLogging: cfg.Gateway.EnableRespLog,
	}
}

func registerWithGovernance(cfg *config.Config, log logger.Logger) *govclient.Client {
	// Governance URL is now loaded from environment variables via pkg/config/env
	governanceURL := cfg.Governance.URL

	podName := cfg.Governance.PodName
	if podName == "" {
		podName = os.Getenv("POD_NAME")
	}
	if podName == "" {
		podName, _ = os.Hostname()
	}

	// Create governance client
	govClient := govclient.NewClient(&govclient.ClientConfig{
		ManagerURL:  governanceURL,
		ServiceName: cfg.Governance.ServiceName,
		PodName:     podName,
	})

	// Use GovBackendPort for governance health check and notification endpoints
	// This ensures consistency across all services (default 2345)
	govBackendPort := cfg.Governance.GovBackendPort
	if govBackendPort == 0 {
		govBackendPort = 2345 // Default governance backend port
	}

	go govClient.StartHTTPServerWithClient(govclient.HTTPServerConfig{
		Port: govBackendPort,
	})

	// Wait a bit for server to start
	time.Sleep(200 * time.Millisecond)

	// Use ServiceIP from environment config for health check URL
	// This defaults to local IP address and can be overridden via SERVICE_IP env var
	serviceIP := cfg.Governance.ServiceIP
	if serviceIP == "" || serviceIP == "0.0.0.0" {
		// Fallback: use actual hostname for Docker/K8s environments
		serviceIP, _ = os.Hostname()
	}

	// Use ServicePort from environment config for governance registration
	servicePort := cfg.Governance.ServicePort
	if servicePort == 0 {
		// Fallback: extract from server listen address
		listenParts := strings.Split(cfg.Server.ListenAddr, ":")
		if len(listenParts) > 1 {
			fmt.Sscanf(listenParts[1], "%d", &servicePort)
		}
		if servicePort == 0 {
			servicePort = 3868 // Default diameter port
		}
	}

	// Build subscriptions
	subscriptions := make([]models.Subscription, 0)
	for _, subName := range cfg.Governance.Subscriptions {
		subscriptions = append(subscriptions, models.Subscription{
			ServiceName: subName,
			ProviderIDs: []string{}, // Subscribe to all providers
		})
	}

	// Register diam-gw service and subscribe to configured services
	registration := &models.ServiceRegistration{
		ServiceName: cfg.Governance.ServiceName,
		PodName:     podName,
		Providers: []models.ProviderInfo{
			{
				ProviderID: "diameter",
				Protocol:   models.ProtocolTCP,
				IP:         serviceIP,
				Port:       servicePort,
			},
		},
		HealthCheckURL:  fmt.Sprintf("http://%s:%d/health", serviceIP, govBackendPort),
		NotificationURL: fmt.Sprintf("http://%s:%d/notify", serviceIP, govBackendPort),
		Subscriptions: []models.Subscription{{
			ServiceName: models.ServiceNameEir,
			ProviderIDs: []string{string(models.ProviderEIRDiameter)},
		}},
	}

	resp, err := govClient.Register(registration)
	if err != nil {
		log.Warnw("Failed to register with governance manager", "error", err)
		if cfg.Governance.FailOnError {
			panic(err)
		}
	} else {
		log.Infow("✓ Registered with governance manager",
			"url", governanceURL,
			"service", cfg.Governance.ServiceName,
			"pod", podName,
			"own_pods", len(resp.Pods),
			"subscribed_services", len(resp.SubscribedServices))

		// Log subscription details
		for svcName, pods := range resp.SubscribedServices {
			log.Infow("  Subscription", "service", svcName, "pods", len(pods))
		}
	}

	// Start heartbeat
	govClient.StartHeartbeat()
	log.Infow("✓ Started governance heartbeat")

	return govClient
}

func statsReporter(ctx context.Context, gw *gateway.Gateway, log logger.Logger, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			stats := gw.GetStats()
			draStats := gw.GetDRAPool().GetStats()

			log.Infow("=== Gateway Statistics ===")
			log.Infow("Gateway",
				"total_requests", stats.TotalRequests,
				"total_responses", stats.TotalResponses,
				"active_sessions", stats.ActiveSessions,
				"total_errors", stats.TotalErrors,
				"avg_latency_ms", fmt.Sprintf("%.2f", stats.AverageLatencyMs))

			log.Infow("Forwarding",
				"to_dra", stats.TotalForwarded,
				"from_dra", stats.TotalFromDRA,
				"timeout_errors", stats.TimeoutErrors,
				"routing_errors", stats.RoutingErrors)

			log.Infow("Sessions",
				"created", stats.SessionsCreated,
				"completed", stats.SessionsCompleted,
				"expired", stats.SessionsExpired)

			log.Infow("DRA Pool",
				"active_priority", draStats.CurrentPriority,
				"total_dras", draStats.TotalDRAs,
				"active_dras", draStats.ActiveDRAs,
				"total_connections", draStats.TotalConnections,
				"active_connections", draStats.ActiveConnections,
				"failover_count", draStats.FailoverCount.Load())

			log.Infow("DRA Messages",
				"sent", draStats.TotalMessagesSent,
				"received", draStats.TotalMessagesRecv)

			log.Infow("==========================")
		}
	}
}

func startStatsServer(port int, gw *gateway.Gateway, startTime time.Time, log logger.Logger) {
	mux := http.NewServeMux()

	// Unified stats endpoint
	mux.HandleFunc("/api/stats", func(w http.ResponseWriter, r *http.Request) {
		handleUnifiedStats(w, r, gw, startTime)
	})

	// Health check endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		handleHealth(w, r, gw)
	})

	addr := fmt.Sprintf(":%d", port)
	log.Infow("Starting stats API server", "address", addr)

	server := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Errorw("Stats server error", "error", err)
	}
}

func handleUnifiedStats(w http.ResponseWriter, r *http.Request, gw *gateway.Gateway, startTime time.Time) {
	// Get Diam-GW stats
	gwStats := gw.GetStats()

	// Convert to unified format
	serviceStats := diamStats.ConvertToUnifiedStats(&gwStats, startTime)

	// Create response
	response := unifiedStats.StatsResponse{
		Status: "success",
		Data:   *serviceStats,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

func handleHealth(w http.ResponseWriter, r *http.Request, gw *gateway.Gateway) {
	gwStats := gw.GetStats()
	draStats := gw.GetDRAPool().GetStats()

	health := unifiedStats.HealthStatus{
		Status:    "healthy",
		Timestamp: time.Now(),
		Checks: map[string]unifiedStats.Check{
			"gateway": {
				Status:  "pass",
				Message: fmt.Sprintf("Active sessions: %d, Total requests: %d", gwStats.ActiveSessions, gwStats.TotalRequests),
			},
			"dra_pool": {
				Status:  "pass",
				Message: fmt.Sprintf("Active DRAs: %d/%d, Active connections: %d", draStats.ActiveDRAs, draStats.TotalDRAs, draStats.ActiveConnections),
			},
		},
	}

	// Check if DRA pool has issues
	if draStats.ActiveDRAs == 0 {
		health.Status = "degraded"
		health.Checks["dra_pool"] = unifiedStats.Check{
			Status:  "warn",
			Message: "No active DRA connections",
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(health)
}
