package connection

import (
	"bufio"
	"context"
	"crypto/tls"
	"io"
	"net"
	"sync"
	"time"
)

// ConnectionConfig holds configuration for a connection
type ConnectionConfig struct {
	ReadTimeout  time.Duration // Maximum duration before timing out read
	WriteTimeout time.Duration // Maximum duration before timing out write
	BufferSize   int           // Buffer size for read/write operations
}

// DefaultConnectionConfig returns default connection configuration
func DefaultConnectionConfig() *ConnectionConfig {
	return &ConnectionConfig{
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		BufferSize:   4096,
	}
}

// liveSwitchReader is a switchReader that's safe for concurrent reads and switches
type liveSwitchReader struct {
	sync.Mutex
	r         io.Reader
	pr        *io.PipeReader
	pipeCopyF func()
}

func (sr *liveSwitchReader) Read(p []byte) (n int, err error) {
	sr.Lock()
	// Check if closeNotifier was created prior to this Read call & start it
	if sr.pr != nil && sr.pipeCopyF != nil {
		go sr.pipeCopyF()
		sr.r = sr.pr
		sr.pr = nil
		sr.pipeCopyF = nil
	}
	r := sr.r
	sr.Unlock()
	return r.Read(p)
}

// conn represents a Diameter connection (used by both server and client)
type conn struct {
	rwc      net.Conn
	sr       liveSwitchReader
	buf      *bufio.ReadWriter
	tlsState *tls.ConnectionState
	config   *ConnectionConfig

	mu           sync.Mutex
	closeNotifyc chan struct{}
	clientGone   bool
	ctx          context.Context
	ctxMu        sync.Mutex
}

// NewConn creates a new connection wrapper
func NewConn(rwc net.Conn, config *ConnectionConfig) Conn {
	if config == nil {
		config = DefaultConnectionConfig()
	}

	c := &conn{
		rwc:    rwc,
		sr:     liveSwitchReader{r: rwc},
		config: config,
		ctx:    context.Background(),
	}
	c.buf = bufio.NewReadWriter(bufio.NewReader(&c.sr), bufio.NewWriter(rwc))

	// Check for TLS connection
	if tlsConn, ok := rwc.(*tls.Conn); ok {
		state := tlsConn.ConnectionState()
		c.tlsState = &state
	}

	return c
}

// Write writes a message to the connection
func (c *conn) Write(b []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.config.WriteTimeout > 0 {
		c.rwc.SetWriteDeadline(time.Now().Add(c.config.WriteTimeout))
	}

	// Use bufio.Writer but ensure proper flushing
	n, err := c.buf.Writer.Write(b)
	if err != nil {
		return 0, err
	}

	// Force flush to ensure data is sent
	if err = c.buf.Writer.Flush(); err != nil {
		return 0, err
	}

	return n, nil
}

// WriteStream implements the Conn interface (for compatibility)
func (c *conn) WriteStream(b []byte, stream uint) (int, error) {
	return c.Write(b)
}

// Close closes the connection
func (c *conn) Close() error {
	return c.rwc.Close()
}

// LocalAddr returns the local address of the connection
func (c *conn) LocalAddr() net.Addr {
	return c.rwc.LocalAddr()
}

// RemoteAddr returns the peer address of the connection
func (c *conn) RemoteAddr() net.Addr {
	return c.rwc.RemoteAddr()
}

// TLS returns the TLS connection state, or nil
func (c *conn) TLS() *tls.ConnectionState {
	return c.tlsState
}

// CloseNotify implements the CloseNotifier interface
func (c *conn) CloseNotify() <-chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closeNotifyc == nil {
		c.closeNotifyc = make(chan struct{})

		pr, pw := io.Pipe()
		c.sr.Lock()
		readSource := c.sr.r
		c.sr.pr = pr
		c.sr.pipeCopyF = func() {
			_, err := io.Copy(pw, readSource)
			if err == nil {
				err = io.EOF
			}
			pw.CloseWithError(err)
			c.notifyClientGone()
		}
		c.sr.Unlock()
	}
	return c.closeNotifyc
}

func (c *conn) notifyClientGone() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closeNotifyc != nil && !c.clientGone {
		close(c.closeNotifyc)
		c.clientGone = true
	}
}

// Context returns the internal context or a new context.Background
func (c *conn) Context() context.Context {
	c.ctxMu.Lock()
	defer c.ctxMu.Unlock()
	if c.ctx == nil {
		c.ctx = context.Background()
	}
	return c.ctx
}

// SetContext replaces the internal context with the given one
func (c *conn) SetContext(ctx context.Context) {
	c.ctxMu.Lock()
	c.ctx = ctx
	c.ctxMu.Unlock()
}

// Connection returns the underlying network connection
func (c *conn) Connection() net.Conn {
	return c.rwc
}

// ReadMessage reads the next message from the connection
func (c *conn) ReadMessage() (*Message, error) {
	if c.config.ReadTimeout > 0 {
		c.rwc.SetReadDeadline(time.Now().Add(c.config.ReadTimeout))
	}

	buf := newReaderBuffer()
	defer putReaderBuffer(buf)
	m := &Message{}
	err := m.readHeader(c.buf.Reader, buf)
	if err != nil {
		return nil, err
	}
	if err = m.readBody(c.buf.Reader, buf); err != nil {
		return m, err
	}
	return m, nil
}
