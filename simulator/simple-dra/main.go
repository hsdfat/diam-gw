package main

import (
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hsdfat/diam-gw/commands/base"
	"github.com/hsdfat/diam-gw/models_base"
	"github.com/hsdfat/diam-gw/pkg/logger"
	"github.com/hsdfat/diam-gw/server"
)

// Config holds DRA configuration
type Config struct {
	ListenAddress string
	OriginHost    string
	OriginRealm   string
	ProductName   string
	VendorID      uint32
	DWRInterval   time.Duration
	LogLevel      string
}

// DefaultConfig returns default DRA configuration
func DefaultConfig() *Config {
	return &Config{
		ListenAddress: "0.0.0.0:3868",
		OriginHost:    "dra-simulator.example.com",
		OriginRealm:   "example.com",
		ProductName:   "DRA-Simulator",
		VendorID:      10415, // 3GPP
		DWRInterval:   30 * time.Second,
		LogLevel:      "info",
	}
}

// DRASimulator represents the DRA simulator
type DRASimulator struct {
	config *Config
	server *server.Server
	logger logger.Logger
}

// NewDRASimulator creates a new DRA simulator
func NewDRASimulator(config *Config) (*DRASimulator, error) {
	log := logger.New("dra-simulator", config.LogLevel)

	// Create server configuration
	serverConfig := &server.ServerConfig{
		ListenAddress:  config.ListenAddress,
		MaxConnections: 100,
		ConnectionConfig: &server.ConnectionConfig{
			ReadTimeout:      60 * time.Second,
			WriteTimeout:     30 * time.Second,
			WatchdogInterval: config.DWRInterval,
			WatchdogTimeout:  10 * time.Second,
			MaxMessageSize:   65535,
			SendChannelSize:  100,
			RecvChannelSize:  100,
			OriginHost:       config.OriginHost,
			OriginRealm:      config.OriginRealm,
			ProductName:      config.ProductName,
			VendorID:         config.VendorID,
			HandleWatchdog:   false, // We'll handle watchdog manually
		},
		RecvChannelSize: 1000,
	}

	// Create server
	srv := server.NewServer(serverConfig, log)

	dra := &DRASimulator{
		config: config,
		server: srv,
		logger: log,
	}

	// Register handlers
	dra.registerHandlers()

	return dra, nil
}

// registerHandlers registers message handlers
func (d *DRASimulator) registerHandlers() {
	// Register CER handler (Command Code 257, Base Protocol Interface 0)
	d.server.HandleFunc(server.Command{Interface: 0, Code: 257}, d.handleCER)

	// Register DWR handler (Command Code 280, Base Protocol Interface 0)
	d.server.HandleFunc(server.Command{Interface: 0, Code: 280}, d.handleDWR)

	// Register DPR handler (Command Code 282, Base Protocol Interface 0)
	d.server.HandleFunc(server.Command{Interface: 0, Code: 282}, d.handleDPR)
}

// handleCER handles Capabilities-Exchange-Request
func (d *DRASimulator) handleCER(msg *server.Message, conn server.Conn) {
	d.logger.Infow("CER received",
		"remote", conn.RemoteAddr().String(),
		"length", msg.Length)

	// Unmarshal CER (need full message: header + body)
	cer := &base.CapabilitiesExchangeRequest{}
	fullMsg := append(msg.Header, msg.Body...)
	if err := cer.Unmarshal(fullMsg); err != nil {
		d.logger.Errorw("Failed to unmarshal CER", "error", err)
		return
	}

	d.logger.Infow("CER details",
		"origin_host", cer.OriginHost,
		"origin_realm", cer.OriginRealm,
		"product_name", cer.ProductName)

	// Create CEA
	cea := base.NewCapabilitiesExchangeAnswer()
	cea.ResultCode = models_base.Unsigned32(2001) // DIAMETER_SUCCESS
	cea.OriginHost = models_base.DiameterIdentity(d.config.OriginHost)
	cea.OriginRealm = models_base.DiameterIdentity(d.config.OriginRealm)
	cea.ProductName = models_base.UTF8String(d.config.ProductName)
	cea.VendorId = models_base.Unsigned32(d.config.VendorID)

	// Set local IP address
	if localAddr, ok := conn.LocalAddr().(*net.TCPAddr); ok {
		cea.HostIpAddress = []models_base.Address{models_base.Address(localAddr.IP)}
	}

	// Copy supported application IDs from CER
	if len(cer.AuthApplicationId) > 0 {
		cea.AuthApplicationId = cer.AuthApplicationId
	}
	if len(cer.AcctApplicationId) > 0 {
		cea.AcctApplicationId = cer.AcctApplicationId
	}

	// Copy identifiers from request to ensure proper correlation
	cea.Header.HopByHopID = cer.Header.HopByHopID
	cea.Header.EndToEndID = cer.Header.EndToEndID

	// Marshal CEA
	ceaData, err := cea.Marshal()
	if err != nil {
		d.logger.Errorw("Failed to marshal CEA", "error", err)
		return
	}

	// Send CEA
	if _, err := conn.Write(ceaData); err != nil {
		d.logger.Errorw("Failed to send CEA", "error", err)
		return
	}

	d.logger.Infow("CEA sent",
		"remote", conn.RemoteAddr().String(),
		"result_code", cea.ResultCode)
}

// handleDWR handles Device-Watchdog-Request
func (d *DRASimulator) handleDWR(msg *server.Message, conn server.Conn) {
	d.logger.Debugw("DWR received", "remote", conn.RemoteAddr().String())

	// Unmarshal DWR (need full message: header + body)
	dwr := &base.DeviceWatchdogRequest{}
	fullMsg := append(msg.Header, msg.Body...)
	if err := dwr.Unmarshal(fullMsg); err != nil {
		d.logger.Errorw("Failed to unmarshal DWR", "error", err)
		return
	}

	// Create DWA
	dwa := base.NewDeviceWatchdogAnswer()
	dwa.ResultCode = models_base.Unsigned32(2001) // DIAMETER_SUCCESS
	dwa.OriginHost = models_base.DiameterIdentity(d.config.OriginHost)
	dwa.OriginRealm = models_base.DiameterIdentity(d.config.OriginRealm)

	// Copy identifiers from request to ensure proper correlation
	dwa.Header.HopByHopID = dwr.Header.HopByHopID
	dwa.Header.EndToEndID = dwr.Header.EndToEndID

	// Marshal DWA
	dwaData, err := dwa.Marshal()
	if err != nil {
		d.logger.Errorw("Failed to marshal DWA", "error", err)
		return
	}

	// Send DWA
	if _, err := conn.Write(dwaData); err != nil {
		d.logger.Errorw("Failed to send DWA", "error", err)
		return
	}

	d.logger.Debugw("DWA sent", "remote", conn.RemoteAddr().String())
}

// handleDPR handles Disconnect-Peer-Request
func (d *DRASimulator) handleDPR(msg *server.Message, conn server.Conn) {
	d.logger.Infow("DPR received", "remote", conn.RemoteAddr().String())

	// Unmarshal DPR (need full message: header + body)
	dpr := &base.DisconnectPeerRequest{}
	fullMsg := append(msg.Header, msg.Body...)
	if err := dpr.Unmarshal(fullMsg); err != nil {
		d.logger.Errorw("Failed to unmarshal DPR", "error", err)
		return
	}

	d.logger.Infow("DPR details",
		"origin_host", dpr.OriginHost,
		"origin_realm", dpr.OriginRealm,
		"disconnect_cause", dpr.DisconnectCause)

	// Create DPA
	dpa := base.NewDisconnectPeerAnswer()
	dpa.ResultCode = models_base.Unsigned32(2001) // DIAMETER_SUCCESS
	dpa.OriginHost = models_base.DiameterIdentity(d.config.OriginHost)
	dpa.OriginRealm = models_base.DiameterIdentity(d.config.OriginRealm)

	// Copy identifiers from request
	dpa.Header.HopByHopID = dpr.Header.HopByHopID
	dpa.Header.EndToEndID = dpr.Header.EndToEndID

	// Marshal DPA
	dpaData, err := dpa.Marshal()
	if err != nil {
		d.logger.Errorw("Failed to marshal DPA", "error", err)
		return
	}

	// Send DPA
	if _, err := conn.Write(dpaData); err != nil {
		d.logger.Errorw("Failed to send DPA", "error", err)
		return
	}

	d.logger.Infow("DPA sent", "remote", conn.RemoteAddr().String())

	// Close connection after sending DPA
	go func() {
		time.Sleep(100 * time.Millisecond)
		conn.Close()
	}()
}

// Start starts the DRA simulator
func (d *DRASimulator) Start() error {
	d.logger.Infow("Starting DRA simulator",
		"listen_address", d.config.ListenAddress,
		"origin_host", d.config.OriginHost,
		"origin_realm", d.config.OriginRealm)

	// Start server in a goroutine
	go func() {
		if err := d.server.Start(); err != nil {
			d.logger.Errorw("Server error", "error", err)
		}
	}()

	return nil
}

// Stop stops the DRA simulator
func (d *DRASimulator) Stop() error {
	d.logger.Infow("Stopping DRA simulator")
	return d.server.Stop()
}

// GetStats returns current statistics
func (d *DRASimulator) GetStats() server.ServerStatsSnapshot {
	return d.server.GetStats()
}

// PrintStats prints current statistics
func (d *DRASimulator) PrintStats() {
	stats := d.GetStats()
	d.logger.Infow("=== DRA Statistics ===")
	d.logger.Infow("Connections",
		"total", stats.TotalConnections,
		"active", stats.ActiveConnections)
	d.logger.Infow("Messages",
		"total", stats.TotalMessages,
		"received", stats.MessagesReceived,
		"sent", stats.MessagesSent)
	d.logger.Infow("Data",
		"bytes_received", stats.TotalBytesReceived,
		"bytes_sent", stats.TotalBytesSent)
	d.logger.Infow("Errors", "count", stats.Errors)

	// Print interface stats
	for appID, ifStats := range stats.InterfaceStats {
		interfaceName := getInterfaceName(appID)
		d.logger.Infow("Interface Stats",
			"interface", interfaceName,
			"app_id", appID,
			"messages_received", ifStats.MessagesReceived,
			"messages_sent", ifStats.MessagesSent)

		// Print command stats
		for cmdCode, cmdStats := range ifStats.CommandStats {
			cmdName := getCommandName(cmdCode)
			d.logger.Infow("  Command Stats",
				"command", cmdName,
				"code", cmdCode,
				"received", cmdStats.MessagesReceived,
				"sent", cmdStats.MessagesSent)
		}
	}
	d.logger.Infow("=====================")
}

// getInterfaceName returns a human-readable interface name
func getInterfaceName(appID int) string {
	switch appID {
	case 0:
		return "Base Protocol"
	case 16777251:
		return "S6a"
	case 16777252:
		return "S13"
	case 16777238:
		return "Gx"
	case 16777216:
		return "Cx"
	case 16777217:
		return "Sh"
	default:
		return fmt.Sprintf("Unknown(%d)", appID)
	}
}

// getCommandName returns a human-readable command name
func getCommandName(code int) string {
	switch code {
	case 257:
		return "CER/CEA"
	case 280:
		return "DWR/DWA"
	case 282:
		return "DPR/DPA"
	case 324:
		return "MICR/MICA (S13)"
	case 316:
		return "ULR/ULA (S6a)"
	case 318:
		return "AIR/AIA (S6a)"
	case 272:
		return "CCR/CCA (Gx)"
	default:
		return fmt.Sprintf("Unknown(%d)", code)
	}
}

func main() {
	// Parse command-line flags
	listenAddr := flag.String("listen", "0.0.0.0:3868", "Listen address (host:port)")
	originHost := flag.String("origin-host", "dra-simulator.example.com", "Origin-Host identity")
	originRealm := flag.String("origin-realm", "example.com", "Origin-Realm identity")
	productName := flag.String("product-name", "DRA-Simulator", "Product name")
	vendorID := flag.Uint("vendor-id", 10415, "Vendor ID (10415 for 3GPP)")
	dwrInterval := flag.Duration("dwr-interval", 30*time.Second, "Device Watchdog Request interval")
	logLevel := flag.String("log-level", "info", "Log level (debug, info, warn, error)")
	statsInterval := flag.Duration("stats-interval", 60*time.Second, "Statistics reporting interval (0 to disable)")

	flag.Parse()

	// Create configuration
	config := &Config{
		ListenAddress: *listenAddr,
		OriginHost:    *originHost,
		OriginRealm:   *originRealm,
		ProductName:   *productName,
		VendorID:      uint32(*vendorID),
		DWRInterval:   *dwrInterval,
		LogLevel:      *logLevel,
	}

	// Create DRA simulator
	dra, err := NewDRASimulator(config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create DRA simulator: %v\n", err)
		os.Exit(1)
	}

	// Start DRA
	if err := dra.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start DRA simulator: %v\n", err)
		os.Exit(1)
	}

	// Start statistics reporting if enabled
	if *statsInterval > 0 {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go func() {
			ticker := time.NewTicker(*statsInterval)
			defer ticker.Stop()

			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					dra.PrintStats()
				}
			}
		}()
	}

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	dra.logger.Infow("Shutdown signal received")

	// Stop DRA gracefully
	if err := dra.Stop(); err != nil {
		dra.logger.Errorw("Error during shutdown", "error", err)
	}

	// Print final statistics
	dra.PrintStats()
	dra.logger.Infow("DRA simulator stopped")
}

// Helper function to extract command code from message
func extractCommandCode(header []byte) uint32 {
	if len(header) < 8 {
		return 0
	}
	return binary.BigEndian.Uint32(header[4:8]) & 0x00FFFFFF
}
