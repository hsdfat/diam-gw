package models_base

import (
	"encoding/binary"
	"fmt"
)

type Integer32 int32

func DecodeInteger32(b []byte) (Type, error) {
	if len(b) != 4 {
		return Integer32(0), nil
	}
	return Integer32(int32(binary.BigEndian.Uint32(b))), nil
}

func (n Integer32) Serialize() []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, uint32(n))
	return b
}

func (n Integer32) Len() int {
	return 4
}

func (n Integer32) Padding() int {
	return 0
}

func (n Integer32) Type() TypeID {
	return Integer32Type
}

func (n Integer32) String() string {
	return fmt.Sprintf("Integer32{%d}", n)
}
