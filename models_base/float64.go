package models_base

import (
	"encoding/binary"
	"fmt"
	"math"
)

type Float64 float64

func DecodeFloat64(b []byte) (Type, error) {
	if len(b) != 8 {
		return Float64(0), nil
	}
	return Float64(math.Float64frombits(binary.BigEndian.Uint64(b))), nil
}

func (n Float64) Serialize() []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, math.Float64bits(float64(n)))
	return b
}

func (n Float64) Len() int {
	return 8
}

func (n Float64) Padding() int {
	return 0
}

func (n Float64) Type() TypeID {
	return Float64Type
}

func (n Float64) String() string {
	return fmt.Sprintf("Float64{%0.4f}", n)
}
