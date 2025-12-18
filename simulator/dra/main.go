package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hsdfat/diam-gw/pkg/logger"
)

var (
	host            = flag.String("host", "0.0.0.0", "DRA listening host")
	port            = flag.Int("port", 3868, "DRA listening port")
	originHost      = flag.String("origin-host", "dra.example.com", "DRA Origin-Host")
	originRealm     = flag.String("origin-realm", "example.com", "DRA Origin-Realm")
	productName     = flag.String("product-name", "DRA-Simulator/1.0", "Product name")
	vendorID        = flag.Uint("vendor-id", 10415, "Vendor ID (3GPP=10415)")
	enableMetrics   = flag.Bool("metrics", true, "Enable metrics collection")
	metricsInterval = flag.Duration("metrics-interval", 10*time.Second, "Metrics reporting interval")
	enableRouting   = flag.Bool("routing", false, "Enable message routing to backend servers")
	backendServers  = flag.String("backends", "", "Comma-separated list of backend servers (host:port)")
	maxConnections  = flag.Int("max-connections", 1000, "Maximum concurrent connections")
	readTimeout     = flag.Duration("read-timeout", 30*time.Second, "Connection read timeout")
	writeTimeout    = flag.Duration("write-timeout", 30*time.Second, "Connection write timeout")
	dwrInterval     = flag.Duration("dwr-interval", 30*time.Second, "Device Watchdog Request interval")
	verbose         = flag.Bool("verbose", false, "Enable verbose logging")
	sendMICR        = flag.Bool("send-micr", false, "Send MICR test message after client connects")
	micrIMEI        = flag.String("micr-imei", "123456789012345", "IMEI to use in test MICR")
	micrDelay       = flag.Duration("micr-delay", 5*time.Second, "Delay before sending MICR after client connects")
)

func main() {
	flag.Parse()

	fmt.Println("╔════════════════════════════════════════════════════════════╗")
	fmt.Println("║         Diameter Routing Agent (DRA) Simulator            ║")
	fmt.Println("╚════════════════════════════════════════════════════════════╝")
	fmt.Println()

	// Initialize logger

	// Create DRA configuration
	config := &Config{
		Host:            *host,
		Port:            *port,
		OriginHost:      *originHost,
		OriginRealm:     *originRealm,
		ProductName:     *productName,
		VendorID:        uint32(*vendorID),
		MaxConnections:  *maxConnections,
		ReadTimeout:     *readTimeout,
		WriteTimeout:    *writeTimeout,
		DWRInterval:     *dwrInterval,
		EnableMetrics:   *enableMetrics,
		MetricsInterval: *metricsInterval,
		EnableRouting:   *enableRouting,
		BackendServers:  *backendServers,
	}

	// Validate configuration
	if err := config.Validate(); err != nil {
		logger.Log.Errorw("Invalid configuration", "error", err)
		os.Exit(1)
	}

	// Display configuration
	displayConfig(config)

	// Create context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create DRA server
	dra, err := NewDRA(ctx, config)
	if err != nil {
		logger.Log.Errorw("Failed to create DRA", "error", err)
		os.Exit(1)
	}

	// Start DRA server
	if err := dra.Start(); err != nil {
		logger.Log.Errorw("Failed to start DRA", "error", err)
		os.Exit(1)
	}

	logger.Log.Infow("DRA simulator started successfully",
		"host", config.Host,
		"port", config.Port,
		"origin_host", config.OriginHost,
		"origin_realm", config.OriginRealm)

	// Check for performance test mode (environment variables)
	var loadGen *LoadGenerator
	if testDuration := os.Getenv("TEST_DURATION"); testDuration != "" {
		logger.Log.Infow("Performance test mode detected", "duration", testDuration)

		// Parse configuration from environment
		duration, err := time.ParseDuration(testDuration + "s")
		if err != nil {
			duration = 60 * time.Second
		}

		rampUpTime, _ := time.ParseDuration(os.Getenv("RAMP_UP_TIME") + "s")
		if rampUpTime == 0 {
			rampUpTime = 10 * time.Second
		}

		s13Rate := 0
		if s13Str := os.Getenv("S13_RATE"); s13Str != "" {
			fmt.Sscanf(s13Str, "%d", &s13Rate)
		}

		s6aRate := 0
		if s6aStr := os.Getenv("S6A_RATE"); s6aStr != "" {
			fmt.Sscanf(s6aStr, "%d", &s6aRate)
		}

		gxRate := 0
		if gxStr := os.Getenv("GX_RATE"); gxStr != "" {
			fmt.Sscanf(gxStr, "%d", &gxRate)
		}

		lgConfig := LoadGeneratorConfig{
			Duration:      duration,
			RampUpTime:    rampUpTime,
			S13RatePerSec: s13Rate,
			S6aRatePerSec: s6aRate,
			GxRatePerSec:  gxRate,
		}

		// Wait for client connection
		logger.Log.Infow("Waiting for gateway connection before starting load test...")
		time.Sleep(*micrDelay) // Use existing delay

		if dra.GetFirstConnection() == nil {
			logger.Log.Errorw("No gateway connection available for load test")
		} else {
			loadGen = NewLoadGenerator(dra, lgConfig)
			if err := loadGen.Start(); err != nil {
				logger.Log.Errorw("Failed to start load generator", "error", err)
			}
		}
	} else if *sendMICR {
		// Send MICR test if enabled (backward compatibility)
		go func() {
			time.Sleep(*micrDelay)
			logger.Log.Infow("Attempting to send test MICR", "imei", *micrIMEI)

			conn := dra.GetFirstConnection()
			if conn == nil {
				logger.Log.Warnw("No client connections available for MICR test")
				return
			}

			if err := conn.SendMICR(*micrIMEI); err != nil {
				logger.Log.Errorw("Failed to send test MICR", "error", err)
			} else {
				logger.Log.Infow("Test MICR sent successfully", "imei", *micrIMEI)
			}
		}()
	}

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	<-sigChan
	logger.Log.Infow("Shutdown signal received, stopping DRA...")

	// Stop load generator if running
	if loadGen != nil {
		loadGen.Stop()
		loadGen.Wait()
	}

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := dra.Shutdown(shutdownCtx); err != nil {
		logger.Log.Errorw("Error during shutdown", "error", err)
		os.Exit(1)
	}

	logger.Log.Infow("DRA simulator stopped gracefully")
}

func displayConfig(config *Config) {
	fmt.Println("Configuration:")
	fmt.Printf("  Listen Address:      %s:%d\n", config.Host, config.Port)
	fmt.Printf("  Origin-Host:         %s\n", config.OriginHost)
	fmt.Printf("  Origin-Realm:        %s\n", config.OriginRealm)
	fmt.Printf("  Product-Name:        %s\n", config.ProductName)
	fmt.Printf("  Vendor-ID:           %d\n", config.VendorID)
	fmt.Printf("  Max Connections:     %d\n", config.MaxConnections)
	fmt.Printf("  Read Timeout:        %s\n", config.ReadTimeout)
	fmt.Printf("  Write Timeout:       %s\n", config.WriteTimeout)
	fmt.Printf("  DWR Interval:        %s\n", config.DWRInterval)
	fmt.Printf("  Metrics Enabled:     %v\n", config.EnableMetrics)
	if config.EnableMetrics {
		fmt.Printf("  Metrics Interval:    %s\n", config.MetricsInterval)
	}
	fmt.Printf("  Routing Enabled:     %v\n", config.EnableRouting)
	if config.EnableRouting && config.BackendServers != "" {
		fmt.Printf("  Backend Servers:     %s\n", config.BackendServers)
	}
	fmt.Println()
}
