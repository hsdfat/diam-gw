package connection

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
)

// Message represents a Diameter message
type Message struct {
	Length uint32
	Header []byte
	Body   []byte
}

type DiamConnectionInfo struct {
	Message  *Message
	DiamConn Conn
}

// Buffer pool for message reading
var readerBufferPool sync.Pool

const (
	maxMessageLength = 1 << 12 // 4096 bytes
)

func newReaderBuffer() *bytes.Buffer {
	if v := readerBufferPool.Get(); v != nil {
		return v.(*bytes.Buffer)
	}
	return bytes.NewBuffer(make([]byte, maxMessageLength))
}

func putReaderBuffer(b *bytes.Buffer) {
	if cap(b.Bytes()) == maxMessageLength {
		b.Reset()
		readerBufferPool.Put(b)
	}
}

func readerBufferSlice(buf *bytes.Buffer, l int) []byte {
	b := buf.Bytes()
	if l <= maxMessageLength && cap(b) >= maxMessageLength {
		return b[:l]
	}
	return make([]byte, l)
}

// DiameterMessageType represents the type of Diameter message
type DiameterMessageType int

const (
	// Message types based on command code
	MessageTypeCER DiameterMessageType = 257 // Capabilities-Exchange-Request
	MessageTypeCEA DiameterMessageType = 257 // Capabilities-Exchange-Answer
	MessageTypeDWR DiameterMessageType = 280 // Device-Watchdog-Request
	MessageTypeDWA DiameterMessageType = 280 // Device-Watchdog-Answer
	MessageTypeDPR DiameterMessageType = 282 // Disconnect-Peer-Request
	MessageTypeDPA DiameterMessageType = 282 // Disconnect-Peer-Answer
)

// MessageFlags represents Diameter message flags
type MessageFlags struct {
	Request   bool
	Proxiable bool
	Error     bool
	Retrans   bool
}

// MessageInfo holds parsed information from a Diameter message header
type MessageInfo struct {
	Version       uint8
	Length        uint32
	CommandCode   uint32
	ApplicationID uint32
	HopByHopID    uint32
	EndToEndID    uint32
	Flags         MessageFlags
	IsRequest     bool
	IsProxiable   bool
	IsError       bool
	IsRetrans     bool
}

// ErrInvalidMessage indicates an invalid Diameter message
type ErrInvalidMessage struct {
	Reason string
}

func (e ErrInvalidMessage) Error() string {
	return fmt.Sprintf("invalid message: %s", e.Reason)
}

// ParseMessageHeader parses a Diameter message header
func ParseMessageHeader(data []byte) (*MessageInfo, error) {
	if len(data) < 20 {
		return nil, ErrInvalidMessage{Reason: "message too short for header"}
	}

	info := &MessageInfo{
		Version:       data[0],
		Length:        binary.BigEndian.Uint32([]byte{0, data[1], data[2], data[3]}),
		CommandCode:   binary.BigEndian.Uint32([]byte{0, data[5], data[6], data[7]}),
		ApplicationID: binary.BigEndian.Uint32(data[8:12]),
		HopByHopID:    binary.BigEndian.Uint32(data[12:16]),
		EndToEndID:    binary.BigEndian.Uint32(data[16:20]),
	}

	// Parse flags
	flags := data[4]
	info.IsRequest = (flags & 0x80) != 0
	info.IsProxiable = (flags & 0x40) != 0
	info.IsError = (flags & 0x20) != 0
	info.IsRetrans = (flags & 0x10) != 0

	// Also set Flags struct
	info.Flags.Request = info.IsRequest
	info.Flags.Proxiable = info.IsProxiable
	info.Flags.Error = info.IsError
	info.Flags.Retrans = info.IsRetrans

	// Validate version
	if info.Version != 1 {
		return nil, ErrInvalidMessage{Reason: fmt.Sprintf("unsupported version: %d", info.Version)}
	}

	// Validate length
	if info.Length < 20 {
		return nil, ErrInvalidMessage{Reason: fmt.Sprintf("invalid length: %d", info.Length)}
	}

	return info, nil
}

// IsBaseProtocol returns true if the message is a base protocol message
func (m *MessageInfo) IsBaseProtocol() bool {
	return m.ApplicationID == 0
}

// GetMessageType returns the message type
func (m *MessageInfo) GetMessageType() DiameterMessageType {
	return DiameterMessageType(m.CommandCode)
}

// String returns a string representation of the message info
func (m *MessageInfo) String() string {
	msgType := "Answer"
	if m.IsRequest {
		msgType = "Request"
	}

	cmdName := "Unknown"
	switch m.CommandCode {
	case 257:
		cmdName = "CER/CEA"
	case 280:
		cmdName = "DWR/DWA"
	case 282:
		cmdName = "DPR/DPA"
	case 324:
		cmdName = "ECR/ECA"
	}

	return fmt.Sprintf("%s %s (Code=%d, AppID=%d, H2H=%d, E2E=%d)",
		cmdName, msgType, m.CommandCode, m.ApplicationID, m.HopByHopID, m.EndToEndID)
}

// ReadMessage reads a binary stream from the reader and parses it into a Message
func ReadMessage(reader io.Reader) (*Message, error) {
	buf := newReaderBuffer()
	defer putReaderBuffer(buf)
	m := &Message{}
	err := m.readHeader(reader, buf)
	if err != nil {
		return nil, err
	}
	if err = m.readBody(reader, buf); err != nil {
		return m, err
	}
	return m, nil
}

// readHeader reads the message header
func (m *Message) readHeader(r io.Reader, buf *bytes.Buffer) error {
	b := buf.Bytes()[:20]

	_, err := io.ReadFull(r, b)
	if err != nil {
		return err
	}

	m.Length = uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	if m.Length < 20 {
		return errors.New("invalid header: message length less than 20 bytes")
	}

	m.Header = make([]byte, 20)
	copy(m.Header, b)

	return nil
}

// readBody reads the message body
func (m *Message) readBody(r io.Reader, buf *bytes.Buffer) error {
	if m.Length <= 20 {
		m.Body = []byte{}
		return nil
	}

	bodyLen := int(m.Length - 20)
	b := readerBufferSlice(buf, bodyLen)

	n, err := io.ReadFull(r, b)
	if err != nil {
		return fmt.Errorf("readBody error: %v, %d bytes read", err, n)
	}

	m.Body = make([]byte, bodyLen)
	copy(m.Body, b)

	return nil
}
