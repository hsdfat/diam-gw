package connection

import (
	"encoding/binary"
	"errors"
)

// Command represents a Diameter command identifier
type Command struct {
	Interface int // Application ID (e.g., S13=16777252, S6a=16777251)
	Code      int // Command Code
	Request   bool
}

type Handler func(msg *Message, conn Conn)

// ParseCommand extracts the command information from a message header
func ParseCommand(header []byte) (Command, error) {
	if len(header) < 20 {
		return Command{}, errors.New("invalid header length")
	}

	// Diameter header format:
	// 0-3: Version(1) + Length(3)
	// 4-7: Command Flags(1) + Command Code(3)
	// 8-11: Application ID (4 bytes)
	// 12-15: Hop-by-Hop ID
	// 16-19: End-to-End ID
	flags := header[4]
	request := (flags & 0x80) != 0
	commandCode := int(binary.BigEndian.Uint32([]byte{0, header[5], header[6], header[7]}))
	applicationID := int(binary.BigEndian.Uint32(header[8:12]))

	return Command{
		Interface: applicationID,
		Code:      commandCode,
		Request:   request,
	}, nil
}
