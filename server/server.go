package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hsdfat/diam-gw/commands/base"
	"github.com/hsdfat/diam-gw/models_base"
	"github.com/hsdfat/diam-gw/pkg/connection"
	"github.com/hsdfat/diam-gw/pkg/logger"
)

// Conn interface is used by a handler to send diameter messages.
// This is an alias to the shared connection.Conn interface.
type Conn = connection.Conn

// The CloseNotifier interface is implemented by Conns which
// allow detecting when the underlying connection has gone away.
//
// This mechanism can be used to detect if a peer has disconnected.
type CloseNotifier = connection.CloseNotifier

// Message represents a Diameter message.
// This is an alias to the shared connection.Message type.
type Message = connection.Message

// The HandlerFunc type is an adapter to allow the use of
// ordinary functions as diameter handlers.  If f is a function
// with the appropriate signature, HandlerFunc(f) is a
// Handler object that calls f.
type HandlerFunc func(Conn, *Message)

// ServeDIAM calls f(c, m).
func (f HandlerFunc) ServeDIAM(c Conn, m *Message) {
	f(c, m)
}

// ErrorReporter interface is implemented by Handlers that
// allow reading errors from the underlying connection, like
// parsing diameter messages or connection errors.
type ErrorReporter interface {
	// Error writes an error to the reporter.
	Error(err *ErrorReport)

	// ErrorReports returns a channel that receives
	// errors from the connection.
	ErrorReports() <-chan *ErrorReport
}

// ErrorReport is sent out of the server in case it fails to
// read messages due to a bad dictionary or network errors.
type ErrorReport struct {
	Conn    Conn     // Peer that caused the error
	Message *Message // Message that caused the error
	Error   error    // Error message
}

// String returns an error message. It does not render the Message field.
func (er *ErrorReport) String() string {
	if er.Conn == nil {
		return fmt.Sprintf("diameter error: %s", er.Error)
	}
	return fmt.Sprintf("diameter error on %s: %s", er.Conn.RemoteAddr(), er.Error)
}

// Command represents a Diameter command identifier.
// This is an alias to the shared connection.Command type.
type Command = connection.Command

// Handler is a function that handles a Diameter message
// It receives the Message struct (with Header and Body) and the connection interface
//
// Example usage:
//
//	server := NewServer(config, logger)
//
//	// Register handler for Capabilities-Exchange-Request (CER)
//	server.HandleFunc(Command{Interface: 0, Code: 257}, func(msg *Message, conn Conn) {
//	    // Access message header and body
//	    logger.Info("Received CER from %s, length=%d", conn.RemoteAddr(), msg.Length)
//
//	    // Process AVPs in message body
//	    processAVPs(msg.Body)
//
//	    // Send Capabilities-Exchange-Answer (CEA)
//	    response := buildCEA(msg)
//	    conn.Write(response)
//	})
//
//	// Register handler for Device-Watchdog-Request (DWR)
//	server.HandleFunc(Command{Interface: 0, Code: 280}, func(msg *Message, conn Conn) {
//	    logger.Info("Received DWR from %s", conn.RemoteAddr())
//	    response := buildDWA(msg)
//	    conn.Write(response)
//	})
//
//	server.Start()
type Handler = connection.Handler

// Server represents a Diameter server
type Server struct {
	config      *ServerConfig
	listener    net.Listener
	connections map[string]connection.Conn
	connMu      sync.RWMutex
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	stats       ServerStats
	logger      logger.Logger
	receiveChan chan *MessageContext

	Network      string              // network of the address - empty string defaults to tcp
	Addr         string              // address to listen on, ":3868" if empty
	ReadTimeout  time.Duration       // maximum duration before timing out read of the request
	WriteTimeout time.Duration       // maximum duration before timing out write of the response
	TLSConfig    *tls.Config         // optional TLS config, used by ListenAndServeTLS
	LocalAddr    net.Addr            // optional Local Address to bind dailer's (Dail...) socket to
	HandlerMux   map[Command]Handler // Map of command to handler functions
	handlerMu    sync.RWMutex        // Protects HandlerMux
}

// ServerConfig holds server configuration
type ServerConfig struct {
	ListenAddress    string
	MaxConnections   int
	ConnectionConfig *ConnectionConfig
	RecvChannelSize  int
}

// InterfaceStats tracks statistics for a specific Diameter interface (Application ID)
type InterfaceStats struct {
	MessagesReceived atomic.Uint64
	MessagesSent     atomic.Uint64
	BytesReceived    atomic.Uint64
	BytesSent        atomic.Uint64
	Errors           atomic.Uint64
	CommandStats     map[int]*CommandStats // Map of command code to stats
	CommandStatsMu   sync.RWMutex
}

// CommandStats tracks statistics for a specific command code
type CommandStats struct {
	MessagesReceived atomic.Uint64
	MessagesSent     atomic.Uint64
	BytesReceived    atomic.Uint64
	BytesSent        atomic.Uint64
	Errors           atomic.Uint64
}

// ServerStats tracks server statistics
type ServerStats struct {
	TotalConnections   atomic.Uint64
	ActiveConnections  atomic.Uint64
	TotalMessages      atomic.Uint64
	MessagesSent       atomic.Uint64
	MessagesReceived   atomic.Uint64
	TotalBytesSent     atomic.Uint64
	TotalBytesReceived atomic.Uint64
	Errors             atomic.Uint64
	InterfaceStats     map[int]*InterfaceStats // Map of application ID to interface stats
	InterfaceStatsMu   sync.RWMutex
}

// ServerStatsSnapshot is a snapshot of server statistics for reading
type ServerStatsSnapshot struct {
	TotalConnections   uint64
	ActiveConnections  uint64
	TotalMessages      uint64
	MessagesSent       uint64
	MessagesReceived   uint64
	TotalBytesSent     uint64
	TotalBytesReceived uint64
	Errors             uint64
	InterfaceStats     map[int]InterfaceStatsSnapshot
}

// InterfaceStatsSnapshot is a snapshot of interface statistics
type InterfaceStatsSnapshot struct {
	ApplicationID    int
	MessagesReceived uint64
	MessagesSent     uint64
	BytesReceived    uint64
	BytesSent        uint64
	Errors           uint64
	CommandStats     map[int]CommandStatsSnapshot
}

// CommandStatsSnapshot is a snapshot of command statistics
type CommandStatsSnapshot struct {
	CommandCode      int
	MessagesReceived uint64
	MessagesSent     uint64
	BytesReceived    uint64
	BytesSent        uint64
	Errors           uint64
}

// MessageContext wraps a message with its connection
type MessageContext struct {
	Message    []byte
	Connection *Connection
}

// DefaultServerConfig returns default server configuration
func DefaultServerConfig() *ServerConfig {
	return &ServerConfig{
		ListenAddress:    "0.0.0.0:3868",
		MaxConnections:   1000,
		ConnectionConfig: DefaultConnectionConfig(),
		RecvChannelSize:  1000,
	}
}

// NewServer creates a new Diameter server
func NewServer(config *ServerConfig, log logger.Logger) *Server {
	if config == nil {
		config = DefaultServerConfig()
	}
	if log == nil {
		log = logger.New("diameter-server", "info")
	}

	ctx, cancel := context.WithCancel(context.Background())

	s := &Server{
		config:      config,
		connections: make(map[string]connection.Conn),
		ctx:         ctx,
		cancel:      cancel,
		logger:      log,
		receiveChan: make(chan *MessageContext, config.RecvChannelSize),
		HandlerMux:  make(map[Command]Handler),
		stats: ServerStats{
			InterfaceStats: make(map[int]*InterfaceStats),
		},
	}
	s.HandleFunc(connection.Command{Interface: 0, Code: 257, Request: true}, func(msg *connection.Message, conn connection.Conn) {
		cer := &base.CapabilitiesExchangeRequest{}
		fullMsg := append(msg.Header, msg.Body...)
		if err := cer.Unmarshal(fullMsg); err != nil {
			s.logger.Errorf("Failed to unmarshal CER: %v", err)
			return
		}

		cea := base.NewCapabilitiesExchangeAnswer()
		cea.ResultCode = 2001
		cea.OriginHost = models_base.DiameterIdentity(s.config.ConnectionConfig.OriginHost)
		cea.OriginRealm = models_base.DiameterIdentity(s.config.ConnectionConfig.OriginRealm)
		cea.HostIpAddress = []models_base.Address{models_base.Address(net.ParseIP(s.config.ListenAddress))}
		cea.VendorId = models_base.Unsigned32(10415)
		cea.ProductName = models_base.UTF8String(s.config.ConnectionConfig.ProductName)
		cea.Header.HopByHopID = cer.Header.HopByHopID
		cea.Header.EndToEndID = cer.Header.EndToEndID

		ceaBytes, _ := cea.Marshal()
		conn.Write(ceaBytes)
	})

	// DWR/DWA handler
	s.HandleFunc(connection.Command{Interface: 0, Code: 280, Request: true}, func(msg *connection.Message, conn connection.Conn) {
		dwr := &base.DeviceWatchdogRequest{}
		fullMsg := append(msg.Header, msg.Body...)
		if err := dwr.Unmarshal(fullMsg); err != nil {
			s.logger.Errorf("Failed to unmarshal DWR: %v", err)
			return
		}

		dwa := base.NewDeviceWatchdogAnswer()
		dwa.ResultCode = 2001
		dwa.OriginHost = models_base.DiameterIdentity(s.config.ConnectionConfig.OriginHost)
		dwa.OriginRealm = models_base.DiameterIdentity(s.config.ConnectionConfig.OriginRealm)
		dwa.Header.HopByHopID = dwr.Header.HopByHopID
		dwa.Header.EndToEndID = dwr.Header.EndToEndID

		dwaBytes, _ := dwa.Marshal()
		conn.Write(dwaBytes)
	})

	// DPR/DPA handler
	s.HandleFunc(connection.Command{Interface: 0, Code: 282, Request: true}, func(msg *connection.Message, conn connection.Conn) {
		dpr := &base.DisconnectPeerRequest{}
		fullMsg := append(msg.Header, msg.Body...)
		if err := dpr.Unmarshal(fullMsg); err != nil {
			s.logger.Errorf("Failed to unmarshal DPR: %v", err)
			return
		}

		dpa := base.NewDisconnectPeerAnswer()
		dpa.ResultCode = 2001
		dpa.OriginHost = models_base.DiameterIdentity(s.config.ConnectionConfig.OriginHost)
		dpa.OriginRealm = models_base.DiameterIdentity(s.config.ConnectionConfig.OriginRealm)
		dpa.Header.HopByHopID = dpr.Header.HopByHopID
		dpa.Header.EndToEndID = dpr.Header.EndToEndID

		dpaBytes, _ := dpa.Marshal()
		conn.Write(dpaBytes)

		// Close connection after sending DPA
		time.AfterFunc(100*time.Millisecond, func() {
			conn.Close()
		})
	})
	return s
}

// Start starts the server with http.Server-style accept loop
func (s *Server) Start() error {
	listener, err := net.Listen("tcp", s.config.ListenAddress)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", s.config.ListenAddress, err)
	}

	s.listener = listener
	s.logger.Infow("Server listening", "address", s.config.ListenAddress)

	var tempDelay time.Duration // how long to sleep on accept failure
	for {
		rw, e := listener.Accept()
		if e != nil {
			select {
			case <-s.ctx.Done():
				return nil
			default:
			}

			if ne, ok := e.(net.Error); ok && ne.Temporary() {
				if tempDelay == 0 {
					tempDelay = 5 * time.Millisecond
				} else {
					tempDelay *= 2
				}
				if max := 1 * time.Second; tempDelay > max {
					tempDelay = max
				}
				s.logger.Warnw("accept error, retrying", "error", e, "retry_delay", tempDelay)
				time.Sleep(tempDelay)
				continue
			}

			network := "<nil>"
			address := network
			addr := listener.Addr()
			if addr != nil {
				network = addr.Network()
				address = addr.String()
			}
			s.logger.Errorw("accept error", "error", e, "network", network, "address", address)
			return e
		}
		tempDelay = 0

		// Check max connections
		if int(s.stats.ActiveConnections.Load()) >= s.config.MaxConnections {
			s.logger.Warnw("Max connections reached, rejecting", "remote_addr", rw.RemoteAddr().String())
			rw.Close()
			s.stats.Errors.Add(1)
			continue
		}

		s.logger.Infow("Accepted connection", "remote_addr", rw.RemoteAddr().String())
		s.stats.TotalConnections.Add(1)
		s.stats.ActiveConnections.Add(1)

		if c, err := s.newConn(rw); err != nil {
			s.logger.Errorw("newConn error", "error", err)
			s.stats.ActiveConnections.Add(^uint64(0)) // Decrement
			continue
		} else {
			s.connMu.Lock()
			s.connections[rw.RemoteAddr().String()] = c.writer
			s.connMu.Unlock()
			go c.serve()
		}
	}
}

// Stop stops the server
func (s *Server) Stop() error {
	s.logger.Infow("Stopping server")
	s.cancel()

	if s.listener != nil {
		if err := s.listener.Close(); err != nil {
			s.logger.Errorw("Failed to close listener", "error", err)
		}
	}

	// Close all connections
	s.connMu.Lock()
	for _, conn := range s.connections {
		conn.Close()
	}
	s.connMu.Unlock()

	s.wg.Wait()
	close(s.receiveChan)

	stats := s.GetStats()
	s.logger.Infow("Server stopped",
		"total_conn", stats.TotalConnections,
		"total_msg", stats.TotalMessages,
		"errors", stats.Errors)

	return nil
}

// Receive returns the receive channel for all connections
func (s *Server) Receive() <-chan *MessageContext {
	return s.receiveChan
}

// GetConnection returns a connection by remote address
func (s *Server) GetConnection(addr string) (connection.Conn, bool) {
	s.connMu.RLock()
	defer s.connMu.RUnlock()
	conn, exists := s.connections[addr]
	return conn, exists
}

// GetAllConnections returns all active connections
func (s *Server) GetAllConnections() []connection.Conn {
	s.connMu.RLock()
	defer s.connMu.RUnlock()

	conns := make([]connection.Conn, 0, len(s.connections))
	for _, conn := range s.connections {
		conns = append(conns, conn)
	}
	return conns
}

// getOrCreateInterfaceStats gets or creates stats for an interface
func (s *Server) getOrCreateInterfaceStats(appID int) *InterfaceStats {
	s.stats.InterfaceStatsMu.Lock()
	defer s.stats.InterfaceStatsMu.Unlock()

	if stats, exists := s.stats.InterfaceStats[appID]; exists {
		return stats
	}

	stats := &InterfaceStats{
		CommandStats: make(map[int]*CommandStats),
	}
	s.stats.InterfaceStats[appID] = stats
	return stats
}

// getOrCreateCommandStats gets or creates stats for a command within an interface
func (ifStats *InterfaceStats) getOrCreateCommandStats(cmdCode int) *CommandStats {
	ifStats.CommandStatsMu.Lock()
	defer ifStats.CommandStatsMu.Unlock()

	if stats, exists := ifStats.CommandStats[cmdCode]; exists {
		return stats
	}

	stats := &CommandStats{}
	ifStats.CommandStats[cmdCode] = stats
	return stats
}

// GetStats returns a snapshot of server statistics
func (s *Server) GetStats() ServerStatsSnapshot {
	s.stats.InterfaceStatsMu.RLock()
	defer s.stats.InterfaceStatsMu.RUnlock()

	snapshot := ServerStatsSnapshot{
		TotalConnections:   s.stats.TotalConnections.Load(),
		ActiveConnections:  s.stats.ActiveConnections.Load(),
		TotalMessages:      s.stats.TotalMessages.Load(),
		MessagesSent:       s.stats.MessagesSent.Load(),
		MessagesReceived:   s.stats.MessagesReceived.Load(),
		TotalBytesSent:     s.stats.TotalBytesSent.Load(),
		TotalBytesReceived: s.stats.TotalBytesReceived.Load(),
		Errors:             s.stats.Errors.Load(),
		InterfaceStats:     make(map[int]InterfaceStatsSnapshot),
	}

	// Copy interface stats
	for appID, ifStats := range s.stats.InterfaceStats {
		ifStats.CommandStatsMu.RLock()
		cmdStatsSnapshot := make(map[int]CommandStatsSnapshot)
		for cmdCode, cmdStats := range ifStats.CommandStats {
			cmdStatsSnapshot[cmdCode] = CommandStatsSnapshot{
				CommandCode:      cmdCode,
				MessagesReceived: cmdStats.MessagesReceived.Load(),
				MessagesSent:     cmdStats.MessagesSent.Load(),
				BytesReceived:    cmdStats.BytesReceived.Load(),
				BytesSent:        cmdStats.BytesSent.Load(),
				Errors:           cmdStats.Errors.Load(),
			}
		}
		ifStats.CommandStatsMu.RUnlock()

		snapshot.InterfaceStats[appID] = InterfaceStatsSnapshot{
			ApplicationID:    appID,
			MessagesReceived: ifStats.MessagesReceived.Load(),
			MessagesSent:     ifStats.MessagesSent.Load(),
			BytesReceived:    ifStats.BytesReceived.Load(),
			BytesSent:        ifStats.BytesSent.Load(),
			Errors:           ifStats.Errors.Load(),
			CommandStats:     cmdStatsSnapshot,
		}
	}

	return snapshot
}

// GetInterfaceStats returns stats for a specific interface (Application ID)
func (s *Server) GetInterfaceStats(appID int) (InterfaceStatsSnapshot, bool) {
	s.stats.InterfaceStatsMu.RLock()
	defer s.stats.InterfaceStatsMu.RUnlock()

	ifStats, exists := s.stats.InterfaceStats[appID]
	if !exists {
		return InterfaceStatsSnapshot{}, false
	}

	ifStats.CommandStatsMu.RLock()
	cmdStatsSnapshot := make(map[int]CommandStatsSnapshot)
	for cmdCode, cmdStats := range ifStats.CommandStats {
		cmdStatsSnapshot[cmdCode] = CommandStatsSnapshot{
			CommandCode:      cmdCode,
			MessagesReceived: cmdStats.MessagesReceived.Load(),
			MessagesSent:     cmdStats.MessagesSent.Load(),
			BytesReceived:    cmdStats.BytesReceived.Load(),
			BytesSent:        cmdStats.BytesSent.Load(),
			Errors:           cmdStats.Errors.Load(),
		}
	}
	ifStats.CommandStatsMu.RUnlock()

	return InterfaceStatsSnapshot{
		ApplicationID:    appID,
		MessagesReceived: ifStats.MessagesReceived.Load(),
		MessagesSent:     ifStats.MessagesSent.Load(),
		BytesReceived:    ifStats.BytesReceived.Load(),
		BytesSent:        ifStats.BytesSent.Load(),
		Errors:           ifStats.Errors.Load(),
		CommandStats:     cmdStatsSnapshot,
	}, true
}

// GetCommandStats returns stats for a specific command within an interface
func (s *Server) GetCommandStats(appID int, cmdCode int) (CommandStatsSnapshot, bool) {
	s.stats.InterfaceStatsMu.RLock()
	ifStats, exists := s.stats.InterfaceStats[appID]
	s.stats.InterfaceStatsMu.RUnlock()

	if !exists {
		return CommandStatsSnapshot{}, false
	}

	ifStats.CommandStatsMu.RLock()
	defer ifStats.CommandStatsMu.RUnlock()

	cmdStats, exists := ifStats.CommandStats[cmdCode]
	if !exists {
		return CommandStatsSnapshot{}, false
	}

	return CommandStatsSnapshot{
		CommandCode:      cmdCode,
		MessagesReceived: cmdStats.MessagesReceived.Load(),
		MessagesSent:     cmdStats.MessagesSent.Load(),
		BytesReceived:    cmdStats.BytesReceived.Load(),
		BytesSent:        cmdStats.BytesSent.Load(),
		Errors:           cmdStats.Errors.Load(),
	}, true
}

// GetListener returns the server listener (for testing)
func (s *Server) GetListener() net.Listener {
	return s.listener
}

// HandleFunc registers a handler function for a specific command
func (s *Server) HandleFunc(cmd Command, handler Handler) {
	s.handlerMu.Lock()
	defer s.handlerMu.Unlock()
	s.HandlerMux[cmd] = handler
	s.logger.Infow("Registered handler for command", "interface", cmd.Interface, "code", cmd.Code, "request", cmd.Request)
}

// getHandler retrieves a handler for a given command
func (s *Server) getHandler(cmd Command) (Handler, bool) {
	s.handlerMu.RLock()
	defer s.handlerMu.RUnlock()
	handler, exists := s.HandlerMux[cmd]
	return handler, exists
}

// parseCommand extracts the command information from a message header.
// This is a wrapper around the shared connection.ParseCommand function.
func parseCommand(header []byte) (Command, error) {
	return connection.ParseCommand(header)
}

// Broadcast sends a message to all connections
func (s *Server) Broadcast(msg []byte) error {
	s.connMu.RLock()
	defer s.connMu.RUnlock()

	var lastErr error
	for addr, conn := range s.connections {
		if _, err := conn.Write(msg); err != nil {
			s.logger.Errorw("Failed to send message", "address", addr, "error", err)
			lastErr = err
			s.stats.Errors.Add(1)
		} else {
			s.stats.MessagesSent.Add(1)
		}
	}

	return lastErr
}

// ReadMessage reads a binary stream from the reader and parses it into a Message.
// This is a wrapper around the shared connection.ReadMessage function.
func ReadMessage(reader io.Reader) (*Message, error) {
	return connection.ReadMessage(reader)
}

// conn represents the server side of a diameter connection
type conn struct {
	server   *Server
	rwc      net.Conn
	connImpl connection.Conn // Use shared connection implementation
	writer   *response
}

// newConn creates a new connection from rwc
func (srv *Server) newConn(rwc net.Conn) (*conn, error) {
	config := &connection.ConnectionConfig{
		ReadTimeout:  srv.ReadTimeout,
		WriteTimeout: srv.WriteTimeout,
	}

	connImpl := connection.NewConn(rwc, config)

	c := &conn{
		server:   srv,
		rwc:      rwc,
		connImpl: connImpl,
	}
	c.writer = &response{conn: c}
	return c, nil
}

// readMessage reads the next message from connection
func (c *conn) readMessage() (*Message, error) {
	if c.server.ReadTimeout > 0 {
		c.rwc.SetReadDeadline(time.Now().Add(c.server.ReadTimeout))
	}

	return connection.ReadMessage(c.rwc)
}

// serve handles a connection
func (c *conn) serve() {
	defer func() {
		if err := recover(); err != nil {
			buf := make([]byte, 4096)
			buf = buf[:runtime.Stack(buf, false)]
			c.server.logger.Errorw("panic serving connection",
				"remote_addr", c.rwc.RemoteAddr().String(),
				"error", err,
				"stack", string(buf))
			c.server.stats.Errors.Add(1)
		}
		c.rwc.Close()
		// Decrement active connections when connection closes
		c.server.stats.ActiveConnections.Add(^uint64(0)) // Decrement
	}()

	if tlsConn, ok := c.rwc.(*tls.Conn); ok {
		if err := tlsConn.Handshake(); err != nil {
			c.server.logger.Errorw("TLS handshake failed", "error", err)
			return
		}
	}

	for {
		m, err := c.readMessage()
		if err != nil {
			c.rwc.Close()
			if err != io.EOF && err != io.ErrUnexpectedEOF {
				c.server.logger.Errorw("Failed to read message", "error", err)
				c.server.stats.Errors.Add(1)
			}
			c.server.logger.Errorw("connection closed", "err", err, "remote", c.rwc.RemoteAddr().String())
			break
		}

		// Update stats for received message
		c.server.stats.TotalMessages.Add(1)
		c.server.stats.MessagesReceived.Add(1)
		c.server.stats.TotalBytesReceived.Add(uint64(m.Length))

		// Parse command from message header
		cmd, err := parseCommand(m.Header)
		if err != nil {
			c.server.logger.Errorw("Failed to parse command", "error", err)
			c.server.stats.Errors.Add(1)
			continue
		}

		// Update interface and command stats for received message
		ifStats := c.server.getOrCreateInterfaceStats(cmd.Interface)
		ifStats.MessagesReceived.Add(1)
		ifStats.BytesReceived.Add(uint64(m.Length))
		cmdStats := ifStats.getOrCreateCommandStats(cmd.Code)
		cmdStats.MessagesReceived.Add(1)
		cmdStats.BytesReceived.Add(uint64(m.Length))

		// Get handler for this command
		handler, exists := c.server.getHandler(cmd)
		if !exists {
			c.server.logger.Warnw("No handler registered for command",
				"interface", cmd.Interface,
				"code", cmd.Code, "request", cmd.Request)
			continue
		}

		// Call handler in a goroutine with Message struct and connection interface
		// For base protocol messages (CER, DWR, DPR), we need to ensure response is sent
		// before continuing to read next message
		if cmd.Interface == 0 && (cmd.Code == 257 || cmd.Code == 280 || cmd.Code == 282) {
			// Base protocol messages - call handler synchronously to ensure response is sent
			go handler(m, c.writer)
		} else {
			// Application messages - call handler in goroutine
			go func(msg *Message, conn Conn) {
				defer func() {
					if r := recover(); r != nil {
						buf := make([]byte, 4096)
						buf = buf[:runtime.Stack(buf, false)]
						c.server.logger.Errorw("panic in handler",
							"error", r,
							"stack", string(buf))
					}
				}()
				go handler(msg, conn)
			}(m, c.writer)
		}
	}
}

// response represents the server side of a diameter response
// It wraps the shared connection implementation and adds server-specific statistics tracking
type response struct {
	mu   sync.Mutex
	conn *conn
}

// Write writes the message to the connection
func (w *response) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Use the shared connection implementation for writing
	n, err := w.conn.connImpl.Write(b)
	if err != nil {
		w.conn.server.logger.Errorw("Failed to write message", "error", err)
		w.conn.server.stats.Errors.Add(1)
		return 0, err
	}

	// Update stats for sent message
	w.conn.server.stats.MessagesSent.Add(1)
	w.conn.server.stats.TotalBytesSent.Add(uint64(n))

	// Update interface and command stats for sent message
	// Parse command from response message header (first 20 bytes)
	if len(b) >= 20 {
		cmd, err := parseCommand(b[:20])
		if err == nil {
			ifStats := w.conn.server.getOrCreateInterfaceStats(cmd.Interface)
			ifStats.MessagesSent.Add(1)
			ifStats.BytesSent.Add(uint64(n))
			cmdStats := ifStats.getOrCreateCommandStats(cmd.Code)
			cmdStats.MessagesSent.Add(1)
			cmdStats.BytesSent.Add(uint64(n))
		}
	}

	return n, nil
}

// WriteStream implements the Conn interface
func (w *response) WriteStream(b []byte, stream uint) (int, error) {
	return w.Write(b)
}

// Close closes the connection
func (w *response) Close() error {
	return w.conn.connImpl.Close()
}

// LocalAddr returns the local address of the connection
func (w *response) LocalAddr() net.Addr {
	return w.conn.connImpl.LocalAddr()
}

// RemoteAddr returns the peer address of the connection
func (w *response) RemoteAddr() net.Addr {
	return w.conn.connImpl.RemoteAddr()
}

// TLS returns the TLS connection state, or nil
func (w *response) TLS() *tls.ConnectionState {
	return w.conn.connImpl.TLS()
}

// CloseNotify implements the CloseNotifier interface
func (w *response) CloseNotify() <-chan struct{} {
	if cn, ok := w.conn.connImpl.(CloseNotifier); ok {
		return cn.CloseNotify()
	}
	// Return a channel that never closes if CloseNotify is not supported
	ch := make(chan struct{})
	return ch
}

// Context returns the internal context or a new context.Background
func (w *response) Context() context.Context {
	return w.conn.connImpl.Context()
}

// SetContext replaces the internal context with the given one
func (w *response) SetContext(ctx context.Context) {
	w.conn.connImpl.SetContext(ctx)
}

// Connection returns the underlying network connection
func (w *response) Connection() net.Conn {
	return w.conn.connImpl.Connection()
}
