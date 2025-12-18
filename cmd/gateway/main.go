package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/hsdfat/diam-gw/client"
	"github.com/hsdfat/diam-gw/gateway"
	"github.com/hsdfat/diam-gw/pkg/logger"
	"github.com/hsdfat/diam-gw/server"
)

func main() {
	// Command line flags
	appListen := flag.String("app-listen", "", "Address to listen for application connections (empty to disable server mode)")
	appAddrs := flag.String("app-addrs", "", "Comma-separated application addresses for bidirectional mode (host:port)")
	appPriorities := flag.String("app-priorities", "1", "Comma-separated app priorities (same order as app addresses)")
	appConnsPerServer := flag.Int("app-conns", 2, "Number of connections per application (bidirectional mode)")
	draAddrs := flag.String("dra-addrs", "127.0.0.1:3869", "Comma-separated DRA addresses (host:port)")
	draPriorities := flag.String("dra-priorities", "1", "Comma-separated DRA priorities (same order as addresses)")
	draConnsPerServer := flag.Int("dra-conns", 2, "Number of connections per DRA")
	originHost := flag.String("origin-host", "gateway.example.com", "Gateway Origin-Host")
	originRealm := flag.String("origin-realm", "example.com", "Gateway Origin-Realm")
	logLevel := flag.String("log-level", "info", "Log level (debug, info, warn, error)")
	flag.Parse()

	// Set log level
	logger.SetLevel(*logLevel)

	logger.Log.Infow("Starting Diameter Gateway...")

	// Parse DRA configuration
	draConfigs, err := parseDRAConfig(*draAddrs, *draPriorities, *draConnsPerServer)
	if err != nil {
		logger.Log.Errorw("Failed to parse DRA configuration", "error", err)
		os.Exit(1)
	}

	// Parse App configuration (for bidirectional mode)
	var appConfigs []*client.DRAServerConfig
	if *appAddrs != "" {
		appConfigs, err = parseDRAConfig(*appAddrs, *appPriorities, *appConnsPerServer)
		if err != nil {
			logger.Log.Errorw("Failed to parse App configuration", "error", err)
			os.Exit(1)
		}
	}

	// Create gateway configuration
	config := &gateway.Config{
		Name:            "diameter-gateway",
		AppServerConfig: nil, // Will be set below if appListen is specified
		AppPoolConfig:   nil, // Will be set below if appAddrs is specified
		DRAPoolConfig: &client.DRAPoolConfig{
			DRAs:                draConfigs,
			OriginHost:          *originHost,
			OriginRealm:         *originRealm,
			ProductName:         "Diameter Gateway",
			VendorID:            10415,
			ConnectionsPerDRA:   *draConnsPerServer,
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
		},
		MessageTimeout:  10 * time.Second,
		EnableMetrics:   true,
		MetricsInterval: 30 * time.Second,
	}

	// Configure application server mode (apps connect TO gateway)
	if *appListen != "" {
		config.AppServerConfig = &server.ServerConfig{
			ListenAddress:  *appListen,
			MaxConnections: 1000,
			ConnectionConfig: &server.ConnectionConfig{
				ReadTimeout:      30 * time.Second,
				WriteTimeout:     10 * time.Second,
				WatchdogInterval: 30 * time.Second,
				WatchdogTimeout:  10 * time.Second,
				MaxMessageSize:   65535,
				SendChannelSize:  100,
				RecvChannelSize:  100,
				OriginHost:       *originHost,
				OriginRealm:      *originRealm,
				ProductName:      "Diameter Gateway",
				VendorID:         10415,
				HandleWatchdog:   true,
			},
			RecvChannelSize: 1000,
		}
	}

	// Configure application pool mode (gateway connects TO apps) - bidirectional
	if len(appConfigs) > 0 {
		config.AppPoolConfig = &client.DRAPoolConfig{
			DRAs:                appConfigs,
			OriginHost:          *originHost,
			OriginRealm:         *originRealm,
			ProductName:         "Diameter Gateway",
			VendorID:            10415,
			ConnectionsPerDRA:   *appConnsPerServer,
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

	// Validate configuration
	if config.AppServerConfig == nil && config.AppPoolConfig == nil {
		logger.Log.Errorw("Must specify either --app-listen or --app-addrs (or both)")
		os.Exit(1)
	}

	// Create and start gateway
	gw, err := gateway.New(config)
	if err != nil {
		logger.Log.Errorw("Failed to create gateway", "error", err)
		os.Exit(1)
	}

	if err := gw.Start(); err != nil {
		logger.Log.Errorw("Failed to start gateway", "error", err)
		os.Exit(1)
	}

	logger.Log.Infow("Gateway started successfully")
	if *appListen != "" {
		logger.Log.Infow("Listening for applications (server mode)", "address", *appListen)
	}
	if *appAddrs != "" {
		logger.Log.Infow("Connecting to applications (bidirectional mode)", "addresses", *appAddrs, "priorities", *appPriorities)
	}
	logger.Log.Infow("Connecting to DRAs", "addresses", *draAddrs, "priorities", *draPriorities)

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	logger.Log.Infow("Shutdown signal received, stopping gateway...")
	if err := gw.Stop(); err != nil {
		logger.Log.Errorw("Error during shutdown", "error", err)
		os.Exit(1)
	}

	logger.Log.Infow("Gateway stopped successfully")
}

// parseDRAConfig parses DRA configuration from command line flags
func parseDRAConfig(addrs, priorities string, connsPerServer int) ([]*client.DRAServerConfig, error) {
	if addrs == "" {
		return nil, fmt.Errorf("no DRA addresses specified")
	}

	// Split addresses and priorities
	addrList := strings.Split(addrs, ",")
	prioList := strings.Split(priorities, ",")

	// Default priorities to 1 if not enough provided
	for len(prioList) < len(addrList) {
		prioList = append(prioList, "1")
	}

	configs := make([]*client.DRAServerConfig, 0, len(addrList))
	for i, addr := range addrList {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			continue
		}

		// Parse host:port
		parts := strings.Split(addr, ":")
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid address format: %s (expected host:port)", addr)
		}

		host := strings.TrimSpace(parts[0])
		port, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil {
			return nil, fmt.Errorf("invalid port in address %s: %w", addr, err)
		}

		priority := 1
		if i < len(prioList) {
			if p, err := strconv.Atoi(strings.TrimSpace(prioList[i])); err == nil {
				priority = p
			}
		}

		configs = append(configs, &client.DRAServerConfig{
			Name:     fmt.Sprintf("DRA-%d", i+1),
			Host:     host,
			Port:     port,
			Priority: priority,
			Weight:   1,
		})
	}

	if len(configs) == 0 {
		return nil, fmt.Errorf("no valid DRA configurations")
	}

	return configs, nil
}
