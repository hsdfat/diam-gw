package server

import (
	"time"

	"github.com/hsdfat/diam-gw/pkg/connection"
)

// LoggingMiddleware creates a middleware that logs message details
func LoggingMiddleware(s *Server) Middleware {
	return func(next Handler) Handler {
		return func(msg *Message, conn Conn) {
			start := time.Now()

			// Parse command from message header
			cmd, err := parseCommand(msg.Header)
			if err != nil {
				s.logger.Errorw("Failed to parse command in logging middleware", "error", err)
			} else {
				s.logger.Infow("Request received",
					"remote_addr", conn.RemoteAddr().String(),
					"interface", cmd.Interface,
					"code", cmd.Code,
					"length", msg.Length)
			}

			// Call next handler
			next(msg, conn)

			// Log completion
			duration := time.Since(start)
			s.logger.Infow("Request processed",
				"remote_addr", conn.RemoteAddr().String(),
				"duration_ms", duration.Milliseconds())
		}
	}
}

// MetricsMiddleware creates a middleware that tracks request metrics
func MetricsMiddleware(s *Server) Middleware {
	return func(next Handler) Handler {
		return func(msg *Message, conn Conn) {
			start := time.Now()

			// Call next handler
			next(msg, conn)

			// Track processing time
			duration := time.Since(start)

			// Parse command for detailed metrics
			cmd, err := parseCommand(msg.Header)
			if err == nil {
				s.logger.Debugw("Metrics",
					"interface", cmd.Interface,
					"code", cmd.Code,
					"duration_ms", duration.Milliseconds(),
					"size_bytes", msg.Length)
			}
		}
	}
}

// RecoveryMiddleware creates a middleware that recovers from panics
func RecoveryMiddleware(s *Server) Middleware {
	return func(next Handler) Handler {
		return func(msg *Message, conn Conn) {
			defer func() {
				if r := recover(); r != nil {
					s.logger.Errorw("Panic recovered in handler",
						"error", r,
						"remote_addr", conn.RemoteAddr().String())
					s.stats.Errors.Add(1)
				}
			}()

			next(msg, conn)
		}
	}
}

// RateLimitMiddleware creates a middleware that implements basic rate limiting
// This is a simple example - for production use a more sophisticated rate limiter
func RateLimitMiddleware(s *Server, maxRequestsPerSecond int) Middleware {
	var (
		lastReset     time.Time
		requestCount  int
		maxRequests   = maxRequestsPerSecond
	)

	return func(next Handler) Handler {
		return func(msg *Message, conn Conn) {
			now := time.Now()

			// Reset counter every second
			if now.Sub(lastReset) >= time.Second {
				lastReset = now
				requestCount = 0
			}

			requestCount++
			if requestCount > maxRequests {
				s.logger.Warnw("Rate limit exceeded",
					"remote_addr", conn.RemoteAddr().String(),
					"requests", requestCount)
				// Optionally send error response here
				return
			}

			next(msg, conn)
		}
	}
}

// ValidationMiddleware creates a middleware that validates message structure
func ValidationMiddleware(s *Server) Middleware {
	return func(next Handler) Handler {
		return func(msg *Message, conn Conn) {
			// Validate message length
			if msg.Length < 20 {
				s.logger.Errorw("Invalid message length",
					"length", msg.Length,
					"remote_addr", conn.RemoteAddr().String())
				s.stats.Errors.Add(1)
				return
			}

			// Validate header length
			if len(msg.Header) != 20 {
				s.logger.Errorw("Invalid header length",
					"header_len", len(msg.Header),
					"remote_addr", conn.RemoteAddr().String())
				s.stats.Errors.Add(1)
				return
			}

			// Parse and validate command
			cmd, err := parseCommand(msg.Header)
			if err != nil {
				s.logger.Errorw("Failed to parse command",
					"error", err,
					"remote_addr", conn.RemoteAddr().String())
				s.stats.Errors.Add(1)
				return
			}

			// Validate command code is not zero
			if cmd.Code == 0 {
				s.logger.Errorw("Invalid command code",
					"code", cmd.Code,
					"remote_addr", conn.RemoteAddr().String())
				s.stats.Errors.Add(1)
				return
			}

			next(msg, conn)
		}
	}
}

// TimeoutMiddleware creates a middleware that enforces handler execution timeout
func TimeoutMiddleware(s *Server, timeout time.Duration) Middleware {
	return func(next Handler) Handler {
		return func(msg *Message, conn Conn) {
			done := make(chan struct{})

			go func() {
				next(msg, conn)
				close(done)
			}()

			select {
			case <-done:
				// Handler completed successfully
			case <-time.After(timeout):
				s.logger.Warnw("Handler timeout",
					"timeout", timeout,
					"remote_addr", conn.RemoteAddr().String())
			}
		}
	}
}

// CommandFilterMiddleware creates a middleware that only allows specific commands
func CommandFilterMiddleware(s *Server, allowedCommands map[int]bool) Middleware {
	return func(next Handler) Handler {
		return func(msg *Message, conn Conn) {
			cmd, err := parseCommand(msg.Header)
			if err != nil {
				s.logger.Errorw("Failed to parse command in filter",
					"error", err,
					"remote_addr", conn.RemoteAddr().String())
				return
			}

			if !allowedCommands[cmd.Code] {
				s.logger.Warnw("Command not allowed",
					"code", cmd.Code,
					"remote_addr", conn.RemoteAddr().String())
				return
			}

			next(msg, conn)
		}
	}
}

// ContextMiddleware creates a middleware that adds context values
func ContextMiddleware(s *Server) Middleware {
	return func(next Handler) Handler {
		return func(msg *Message, conn Conn) {
			// Parse command and add to connection context if needed
			cmd, err := parseCommand(msg.Header)
			if err == nil {
				s.logger.Debugw("Processing command",
					"interface", cmd.Interface,
					"code", cmd.Code,
					"request", cmd.Request)
			}

			next(msg, conn)
		}
	}
}

// ChainMiddleware chains multiple middlewares together
func ChainMiddleware(middlewares ...Middleware) Middleware {
	return func(next Handler) Handler {
		for i := len(middlewares) - 1; i >= 0; i-- {
			next = middlewares[i](next)
		}
		return next
	}
}

// ConditionalMiddleware applies a middleware only if the condition is met
func ConditionalMiddleware(condition func(*Message, Conn) bool, middleware Middleware) Middleware {
	return func(next Handler) Handler {
		wrappedHandler := middleware(next)
		return func(msg *Message, conn Conn) {
			if condition(msg, conn) {
				wrappedHandler(msg, conn)
			} else {
				next(msg, conn)
			}
		}
	}
}

// InterfaceSpecificMiddleware applies middleware only for specific interfaces
func InterfaceSpecificMiddleware(interfaceID int, middleware Middleware) Middleware {
	return ConditionalMiddleware(func(msg *Message, conn Conn) bool {
		cmd, err := parseCommand(msg.Header)
		if err != nil {
			return false
		}
		return cmd.Interface == interfaceID
	}, middleware)
}

// CommandSpecificMiddleware applies middleware only for specific commands
func CommandSpecificMiddleware(commandCode int, middleware Middleware) Middleware {
	return ConditionalMiddleware(func(msg *Message, conn Conn) bool {
		cmd, err := parseCommand(msg.Header)
		if err != nil {
			return false
		}
		return cmd.Code == commandCode
	}, middleware)
}

// RequestOnlyMiddleware applies middleware only for requests (not answers)
func RequestOnlyMiddleware(middleware Middleware) Middleware {
	return ConditionalMiddleware(func(msg *Message, conn Conn) bool {
		cmd, err := parseCommand(msg.Header)
		if err != nil {
			return false
		}
		return cmd.Request
	}, middleware)
}

// AnswerOnlyMiddleware applies middleware only for answers (not requests)
func AnswerOnlyMiddleware(middleware Middleware) Middleware {
	return ConditionalMiddleware(func(msg *Message, conn Conn) bool {
		cmd, err := connection.ParseCommand(msg.Header)
		if err != nil {
			return false
		}
		return !cmd.Request
	}, middleware)
}
