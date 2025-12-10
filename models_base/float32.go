package models_base

import (
	"encoding/binary"
	"fmt"
	"math"
)

type Float32 float32

func DecodeFloat32(b []byte) (Type, error) {
	if len(b) != 4 {
		return Float32(0), nil
	}
	return Float32(math.Float32frombits(binary.BigEndian.Uint32(b))), nil
}

func (n Float32) Serialize() []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, math.Float32bits(float32(n)))
	return b
}

func (n Float32) Len() int {
	return 4
}

func (n Float32) Padding() int {
	return 0
}

func (n Float32) Type() TypeID {
	return Float32Type
}

func (n Float32) String() string {
	return fmt.Sprintf("Float32{%0.4f}", n)
}
