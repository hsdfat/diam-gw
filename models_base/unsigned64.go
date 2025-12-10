package models_base

import (
	"encoding/binary"
	"fmt"
)

type Unsigned64 uint64

func DecodeUnsigned64(b []byte) (Type, error) {
	if len(b) != 8 {
		return Unsigned64(0), nil
	}
	return Unsigned64(binary.BigEndian.Uint64(b)), nil
}

func (n Unsigned64) Serialize() []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, uint64(n))
	return b
}

func (n Unsigned64) Len() int {
	return 8
}

func (n Unsigned64) Padding() int {
	return 0
}

func (n Unsigned64) Type() TypeID {
	return Unsigned64Type
}

func (n Unsigned64) String() string {
	return fmt.Sprintf("Unsigned64{%d}", n)
}
