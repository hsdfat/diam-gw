package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/hsdfat8/diam-gw/client"
	"github.com/hsdfat8/diam-gw/commands/s13"
	"github.com/hsdfat8/diam-gw/commands/s6a"
	"github.com/hsdfat8/diam-gw/models_base"
	"github.com/hsdfat8/diam-gw/pkg/logger"
)

func main() {
	// Command line flags
	gwAddr := flag.String("gateway", getEnv("APP_GATEWAY", "127.0.0.1:3868"), "Gateway address (host:port)")
	gwConns := flag.Int("gw-conns", getEnvInt("APP_CONNECTIONS", 2), "Number of connections to gateway")
	originHost := flag.String("origin-host", getEnv("APP_ORIGIN_HOST", "app.example.com"), "Origin-Host")
	originRealm := flag.String("origin-realm", getEnv("APP_ORIGIN_REALM", "example.com"), "Origin-Realm")
	interfaces := flag.String("interfaces", getEnv("APP_INTERFACES", ""), "Comma-separated list of supported interfaces (S13,S6a,Gx)")
	logLevel := flag.String("log-level", getEnv("APP_LOG_LEVEL", "info"), "Log level")
	flag.Parse()

	log := logger.New("app-simulator", *logLevel)
	log.Info("Starting Application Simulator...")
	log.Info("Gateway: %s, Connections: %d, Origin: %s@%s", *gwAddr, *gwConns, *originHost, *originRealm)
	log.Info("Supported Interfaces: %s", *interfaces)

	// Parse gateway address
	host, port := parseAddress(*gwAddr)

	// Parse supported interfaces to Application IDs
	var authAppIDs []uint32
	if *interfaces != "" {
		for _, iface := range strings.Split(*interfaces, ",") {
			iface = strings.TrimSpace(iface)
			switch iface {
			case "S13":
				authAppIDs = append(authAppIDs, 16777252) // S13
				log.Info("Added S13 interface (App-ID: 16777252)")
			case "S6a":
				authAppIDs = append(authAppIDs, 16777251) // S6a
				log.Info("Added S6a interface (App-ID: 16777251)")
			case "Gx":
				authAppIDs = append(authAppIDs, 16777238) // Gx
				log.Info("Added Gx interface (App-ID: 16777238)")
			}
		}
	}

	// Create DRA config for connecting to gateway
	config := &client.DRAConfig{
		Host:              host,
		Port:              port,
		ConnectionCount:   *gwConns,
		OriginHost:        *originHost,
		OriginRealm:       *originRealm,
		ProductName:       "App-Simulator/1.0",
		VendorID:          10415,
		AuthAppIDs:        authAppIDs,
		AcctAppIDs:        []uint32{},
		RecvBufferSize:    1000,
		ConnectTimeout:    10 * time.Second,
		CERTimeout:        5 * time.Second,
		DWRInterval:       30 * time.Second,
		DWRTimeout:        10 * time.Second,
		MaxDWRFailures:    3,
		ReconnectInterval: 5 * time.Second,
		MaxReconnectDelay: 5 * time.Minute,
		ReconnectBackoff:  1.5,
		SendBufferSize:    100,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create connection pool
	pool, err := client.NewConnectionPool(ctx, config)
	if err != nil {
		log.Error("Failed to create connection pool: %v", err)
		os.Exit(1)
	}

	// Start connections
	if err := pool.Start(); err != nil {
		log.Error("Failed to start connection pool: %v", err)
		os.Exit(1)
	}

	log.Info("Connected to gateway at %s:%d with %d connections", host, port, *gwConns)

	// Start message handler
	go handleMessages(ctx, pool, log, *originHost, *originRealm)

	// Wait for shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Info("Shutdown signal received")
	pool.Close()
	log.Info("Application simulator stopped")
}

func handleMessages(ctx context.Context, pool *client.ConnectionPool, log logger.Logger, originHost, originRealm string) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-pool.Receive():
			if !ok {
				return
			}

			if err := processMessage(pool, msg, log, originHost, originRealm); err != nil {
				log.Error("Failed to process message: %v", err)
			}
		}
	}
}

func processMessage(pool *client.ConnectionPool, msg []byte, log logger.Logger, originHost, originRealm string) error {
	if len(msg) < 20 {
		return fmt.Errorf("message too short")
	}

	msgInfo, err := client.ParseMessageHeader(msg)
	if err != nil {
		return err
	}

	log.Debug("Received message: code=%d, request=%v, H2H=%d", msgInfo.CommandCode, msgInfo.Flags.Request, msgInfo.HopByHopID)

	if !msgInfo.Flags.Request {
		// Response - just log it
		log.Info("Received response for command code %d", msgInfo.CommandCode)
		return nil
	}

	// Handle requests
	switch msgInfo.CommandCode {
	case 324: // MICR (ME-Identity-Check-Request)
		return handleMICR(pool, msg, msgInfo, log, originHost, originRealm)
	case 318: // AIR (Authentication-Information-Request)
		return handleAIR(pool, msg, msgInfo, log, originHost, originRealm)
	case 272: // CCR (Credit-Control-Request)
		log.Info("Received CCR (Credit-Control-Request) - not implemented")
		return nil
	default:
		log.Warn("Unhandled request command code: %d", msgInfo.CommandCode)
	}

	return nil
}

func handleMICR(pool *client.ConnectionPool, msg []byte, msgInfo *client.MessageInfo, log logger.Logger, originHost, originRealm string) error {
	log.Info("Handling MICR (ME-Identity-Check-Request)")

	// Parse MICR
	micr := &s13.MEIdentityCheckRequest{}
	if err := micr.Unmarshal(msg); err != nil {
		log.Error("Failed to unmarshal MICR: %v", err)
		return err
	}

	log.Info("MICR from %s, SessionId=%s", string(micr.OriginHost), string(micr.SessionId))

	// Create MICA response
	mica := s13.NewMEIdentityCheckAnswer()
	mica.Header.HopByHopID = msgInfo.HopByHopID
	mica.Header.EndToEndID = msgInfo.EndToEndID
	mica.SessionId = micr.SessionId
	mica.AuthSessionState = 1 // NO_STATE_MAINTAINED
	mica.OriginHost = models_base.DiameterIdentity(originHost)
	mica.OriginRealm = models_base.DiameterIdentity(originRealm)
	resultCode := models_base.Unsigned32(2001) // DIAMETER_SUCCESS
	mica.ResultCode = &resultCode

	// Set Equipment-Status (example: WHITELISTED = 0)
	equipStatus := models_base.Enumerated(0)
	mica.EquipmentStatus = &equipStatus

	micaBytes, err := mica.Marshal()
	if err != nil {
		log.Error("Failed to marshal MICA: %v", err)
		return err
	}

	// Send response
	if err := pool.Send(micaBytes); err != nil {
		return err
	}

	log.Info("Sent MICA response with result code 2001")
	return nil
}

func handleAIR(pool *client.ConnectionPool, msg []byte, msgInfo *client.MessageInfo, log logger.Logger, originHost, originRealm string) error {
	log.Info("Handling AIR (Authentication-Information-Request)")

	// Parse AIR
	air := &s6a.AuthenticationInformationRequest{}
	if err := air.Unmarshal(msg); err != nil {
		log.Error("Failed to unmarshal AIR: %v", err)
		return err
	}

	log.Info("AIR from %s, SessionId=%s, IMSI=%s", string(air.OriginHost), string(air.SessionId), string(air.UserName))

	// Create AIA response
	aia := s6a.NewAuthenticationInformationAnswer()
	aia.Header.HopByHopID = msgInfo.HopByHopID
	aia.Header.EndToEndID = msgInfo.EndToEndID
	aia.SessionId = air.SessionId
	aia.AuthSessionState = 1 // NO_STATE_MAINTAINED
	aia.OriginHost = models_base.DiameterIdentity(originHost)
	aia.OriginRealm = models_base.DiameterIdentity(originRealm)
	resultCode := models_base.Unsigned32(2001) // DIAMETER_SUCCESS
	aia.ResultCode = &resultCode

	// Optionally add authentication vectors (simplified example)
	// In a real implementation, you would generate proper RAND, AUTN, XRES, KASME values
	// For now, we just send a successful response without auth info

	aiaBytes, err := aia.Marshal()
	if err != nil {
		log.Error("Failed to marshal AIA: %v", err)
		return err
	}

	// Send response
	if err := pool.Send(aiaBytes); err != nil {
		return err
	}

	log.Info("Sent AIA response with result code 2001")
	return nil
}

func parseAddress(addr string) (string, int) {
	parts := strings.Split(addr, ":")
	if len(parts) != 2 {
		return "127.0.0.1", 3868
	}
	port := 3868
	fmt.Sscanf(parts[1], "%d", &port)
	return parts[0], port
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func getEnvInt(key string, defaultVal int) int {
	if val := os.Getenv(key); val != "" {
		var i int
		if _, err := fmt.Sscanf(val, "%d", &i); err == nil {
			return i
		}
	}
	return defaultVal
}
