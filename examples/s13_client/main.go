package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hsdfat8/diam-gw/client"
	"github.com/hsdfat8/diam-gw/commands/s13"
	"github.com/hsdfat8/diam-gw/models_base"
	"github.com/hsdfat8/diam-gw/pkg/logger"
)

func main() {
	// Create configuration
	config := client.DefaultConfig()
	config.Host = "127.0.0.1" // DRA hostname or IP
	config.Port = 3868        // Standard Diameter port
	config.OriginHost = "gateway.example.com"
	config.OriginRealm = "example.com"
	config.ProductName = "S13-Gateway"
	config.ConnectionCount = 5 // 5 connections per DRA

	// Create context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create connection pool
	pool, err := client.NewConnectionPool(ctx, config)
	if err != nil {
		logger.Log.Fatalf("Failed to create connection pool: %v", err)
	}

	// Start connection pool
	logger.Log.Infow("Starting connection pool...")
	if err := pool.Start(); err != nil {
		logger.Log.Fatalf("Failed to start connection pool: %v", err)
	}

	// Wait for at least one connection to be ready
	logger.Log.Infow("Waiting for connections to be ready...")
	if err := pool.WaitForConnection(30 * time.Second); err != nil {
		logger.Log.Fatalf("No connections available: %v", err)
	}

	logger.Log.Infow("Connection pool is ready!")

	// Start message receiver
	go receiveMessages(pool)

	// Start periodic stats reporter
	go reportStats(pool)

	// Example: Send S13 ME-Identity-Check-Request
	go sendExampleECR(pool)

	// Wait for interrupt signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	<-sigCh
	logger.Log.Infow("Received shutdown signal")

	// Graceful shutdown
	logger.Log.Infow("Closing connection pool...")
	if err := pool.Close(); err != nil {
		logger.Log.Errorf("Error closing pool: %v", err)
	}

	logger.Log.Infow("Shutdown complete")
}

// receiveMessages handles incoming messages
func receiveMessages(pool *client.ConnectionPool) {
	logger.Log.Infow("Starting message receiver...")

	for msg := range pool.Receive() {
		// Parse message header
		info, err := client.ParseMessageHeader(msg)
		if err != nil {
			logger.Log.Errorf("Failed to parse message: %v", err)
			continue
		}

		logger.Log.Infof("Received message: %s (%d bytes)", info.String(), len(msg))

		// Handle S13 messages
		if info.ApplicationID == s13.S13_APPLICATION_ID {
			handleS13Message(msg, info)
		}
	}

	logger.Log.Infow("Message receiver stopped")
}

// handleS13Message handles S13-specific messages
func handleS13Message(data []byte, info *client.MessageInfo) {
	switch info.CommandCode {
	case s13.CommandCodeMEIDENTITYCHECKANSWER:
		if !info.IsRequest {
			handleECA(data)
		}
	default:
		logger.Log.Infof("Unhandled S13 command code: %d", info.CommandCode)
	}
}

// handleECA handles ME-Identity-Check-Answer
func handleECA(data []byte) {
	eca := &s13.MEIdentityCheckAnswer{}
	if err := eca.Unmarshal(data); err != nil {
		logger.Log.Errorf("Failed to unmarshal ECA: %v", err)
		return
	}

	if eca.ResultCode != nil {
		resultCode := client.ResultCode(uint32(*eca.ResultCode))
		logger.Log.Infof("ECA Result: %s (code=%d)", resultCode.String(), uint32(*eca.ResultCode))
	}

	if eca.EquipmentStatus != nil {
		logger.Log.Infof("Equipment Status: %v", *eca.EquipmentStatus)
	}
}

// sendExampleECR sends an example ME-Identity-Check-Request
func sendExampleECR(pool *client.ConnectionPool) {
	// Wait a bit for connections to stabilize
	time.Sleep(2 * time.Second)

	logger.Log.Infow("Sending example ME-Identity-Check-Request...")

	// Create ECR message
	ecr := s13.NewMEIdentityCheckRequest()

	// Set required fields
	ecr.SessionId = models_base.UTF8String("session-12345")
	ecr.OriginHost = models_base.DiameterIdentity("gateway.example.com")
	ecr.OriginRealm = models_base.DiameterIdentity("example.com")
	ecr.DestinationRealm = models_base.DiameterIdentity("eir.example.com")

	// Set S13-specific fields
	imei := models_base.UTF8String("123456789012345")
	ecr.TerminalInformation = &s13.TerminalInformation{
		Imei: &imei,
	}

	// Set hop-by-hop and end-to-end IDs (you should manage these properly)
	ecr.Header.HopByHopID = 1
	ecr.Header.EndToEndID = 1

	// Marshal message
	data, err := ecr.Marshal()
	if err != nil {
		logger.Log.Errorf("Failed to marshal ECR: %v", err)
		return
	}

	// Send message
	if err := pool.Send(data); err != nil {
		logger.Log.Errorf("Failed to send ECR: %v", err)
		return
	}

	logger.Log.Infof("ECR sent successfully (%d bytes)", len(data))
}

// reportStats periodically reports connection pool statistics
func reportStats(pool *client.ConnectionPool) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		stats := pool.GetStats()
		states := pool.GetConnectionStates()

		logger.Log.Infow("========== Connection Pool Statistics ==========")
		logger.Log.Infof("Total Connections: %d", stats.TotalConnections)
		logger.Log.Infof("Active Connections: %d", stats.ActiveConnections)
		logger.Log.Infof("Messages Sent: %d", stats.TotalMessagesSent)
		logger.Log.Infof("Messages Received: %d", stats.TotalMessagesRecv)
		logger.Log.Infof("Bytes Sent: %d", stats.TotalBytesSent)
		logger.Log.Infof("Bytes Received: %d", stats.TotalBytesRecv)
		logger.Log.Infof("Total Reconnects: %d", stats.TotalReconnects)

		logger.Log.Infow("Connection States:")
		for connID, state := range states {
			logger.Log.Infof("  %s: %s", connID, state.String())
		}
		logger.Log.Infow("===============================================")
	}
}
