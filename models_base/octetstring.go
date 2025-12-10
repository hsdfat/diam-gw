package models_base

import "fmt"

type OctetString string

func DecodeOctetString(b []byte) (Type, error) {
	d := make([]byte, len(b))
	copy(d, b)
	return OctetString(d), nil
}

func (s OctetString) Serialize() []byte {
	return []byte(s)
}

func (s OctetString) Len() int {
	return len(s)
}

func (s OctetString) Padding() int {
	l := len(s)
	return pad4(l) - l
}

func (s OctetString) Type() TypeID {
	return OctetStringType
}

func (s OctetString) String() string {
	return fmt.Sprintf("OctetString{%#x},Padding:%d", string(s), s.Padding())
}
