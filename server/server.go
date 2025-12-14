package server

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"

	"github.com/hsdfat8/diam-gw/pkg/logger"
)

// Server represents a Diameter server
type Server struct {
	config      *ServerConfig
	listener    net.Listener
	connections map[string]*Connection
	connMu      sync.RWMutex
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	stats       ServerStats
	logger      logger.Logger
	receiveChan chan *MessageContext
}

// ServerConfig holds server configuration
type ServerConfig struct {
	ListenAddress    string
	MaxConnections   int
	ConnectionConfig *ConnectionConfig
	RecvChannelSize  int
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

	return &Server{
		config:      config,
		connections: make(map[string]*Connection),
		ctx:         ctx,
		cancel:      cancel,
		logger:      log,
		receiveChan: make(chan *MessageContext, config.RecvChannelSize),
	}
}

// Start starts the server
func (s *Server) Start() error {
	listener, err := net.Listen("tcp", s.config.ListenAddress)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", s.config.ListenAddress, err)
	}

	s.listener = listener
	s.logger.Info("Server listening on %s", s.config.ListenAddress)

	s.wg.Add(1)
	go s.acceptLoop()

	return nil
}

// Stop stops the server
func (s *Server) Stop() error {
	s.logger.Info("Stopping server...")
	s.cancel()

	if s.listener != nil {
		if err := s.listener.Close(); err != nil {
			s.logger.Error("Failed to close listener: %v", err)
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

	s.logger.Info("Server stopped. Stats: total_conn=%d, total_msg=%d, errors=%d",
		s.stats.TotalConnections.Load(),
		s.stats.TotalMessages.Load(),
		s.stats.Errors.Load())

	return nil
}

// Receive returns the receive channel for all connections
func (s *Server) Receive() <-chan *MessageContext {
	return s.receiveChan
}

// GetConnection returns a connection by remote address
func (s *Server) GetConnection(addr string) (*Connection, bool) {
	s.connMu.RLock()
	defer s.connMu.RUnlock()
	conn, exists := s.connections[addr]
	return conn, exists
}

// GetAllConnections returns all active connections
func (s *Server) GetAllConnections() []*Connection {
	s.connMu.RLock()
	defer s.connMu.RUnlock()

	conns := make([]*Connection, 0, len(s.connections))
	for _, conn := range s.connections {
		conns = append(conns, conn)
	}
	return conns
}

// GetStats returns server statistics
func (s *Server) GetStats() ServerStats {
	return s.stats
}

// acceptLoop accepts incoming connections
func (s *Server) acceptLoop() {
	defer s.wg.Done()
	defer s.logger.Info("Accept loop exited")

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.ctx.Done():
				return
			default:
				s.logger.Error("Failed to accept connection: %v", err)
				s.stats.Errors.Add(1)
				continue
			}
		}

		// Check max connections
		if int(s.stats.ActiveConnections.Load()) >= s.config.MaxConnections {
			s.logger.Warn("Max connections reached, rejecting %s", conn.RemoteAddr().String())
			conn.Close()
			s.stats.Errors.Add(1)
			continue
		}

		s.logger.Info("Accepted connection from %s", conn.RemoteAddr().String())
		s.stats.TotalConnections.Add(1)
		s.stats.ActiveConnections.Add(1)

		// Create and start connection handler
		srvConn := NewConnection(conn, s.config.ConnectionConfig, s.logger)
		if err := srvConn.Start(); err != nil {
			s.logger.Error("Failed to start connection: %v", err)
			conn.Close()
			s.stats.Errors.Add(1)
			s.stats.ActiveConnections.Add(^uint64(0)) // Decrement
			continue
		}

		// Store connection
		addr := conn.RemoteAddr().String()
		s.connMu.Lock()
		s.connections[addr] = srvConn
		s.connMu.Unlock()

		// Start message receiver for this connection
		s.wg.Add(1)
		go s.receiveFromConnection(srvConn, addr)
	}
}

// receiveFromConnection receives messages from a connection
func (s *Server) receiveFromConnection(conn *Connection, addr string) {
	defer s.wg.Done()
	defer func() {
		s.connMu.Lock()
		delete(s.connections, addr)
		s.connMu.Unlock()
		s.stats.ActiveConnections.Add(^uint64(0)) // Decrement
		s.logger.Info("Connection %s removed from pool", addr)
	}()

	for {
		select {
		case <-s.ctx.Done():
			return
		case msg, ok := <-conn.Receive():
			if !ok {
				return
			}

			s.stats.TotalMessages.Add(1)
			s.stats.MessagesReceived.Add(1)

			// Send to main receive channel
			msgCtx := &MessageContext{
				Message:    msg,
				Connection: conn,
			}

			select {
			case s.receiveChan <- msgCtx:
			case <-s.ctx.Done():
				return
			}
		}
	}
}

// Broadcast sends a message to all connections
func (s *Server) Broadcast(msg []byte) error {
	s.connMu.RLock()
	defer s.connMu.RUnlock()

	var lastErr error
	for addr, conn := range s.connections {
		if err := conn.Send(msg); err != nil {
			s.logger.Error("Failed to send to %s: %v", addr, err)
			lastErr = err
			s.stats.Errors.Add(1)
		} else {
			s.stats.MessagesSent.Add(1)
		}
	}

	return lastErr
}
