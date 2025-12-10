package models_base

import (
	"encoding/binary"
	"fmt"
)

type Unsigned32 uint32

func DecodeUnsigned32(b []byte) (Type, error) {
	if len(b) != 4 {
		return Unsigned32(0), nil
	}
	return Unsigned32(binary.BigEndian.Uint32(b)), nil
}

func (n Unsigned32) Serialize() []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, uint32(n))
	return b
}

func (n Unsigned32) Len() int {
	return 4
}

func (n Unsigned32) Padding() int {
	return 0
}

func (n Unsigned32) Type() TypeID {
	return Unsigned32Type
}

func (n Unsigned32) String() string {
	return fmt.Sprintf("Unsigned32{%d}", n)
}
