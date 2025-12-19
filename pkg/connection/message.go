package connection

import (
	"bytes"
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
