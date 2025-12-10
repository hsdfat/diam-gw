package models_base

import (
	"encoding/binary"
	"fmt"
)

type Integer64 int64

func DecodeInteger64(b []byte) (Type, error) {
	if len(b) != 8 {
		return Integer64(0), nil
	}
	return Integer64(int64(binary.BigEndian.Uint64(b))), nil
}

func (n Integer64) Serialize() []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, uint64(n))
	return b
}

func (n Integer64) Len() int {
	return 8
}

func (n Integer64) Padding() int {
	return 0
}

func (n Integer64) Type() TypeID {
	return Integer64Type
}

func (n Integer64) String() string {
	return fmt.Sprintf("Integer64{%d}", n)
}
