package models_base

import "fmt"

type UTF8String OctetString

func DecodeUTF8String(b []byte) (Type, error) {
	d := make([]byte, len(b))
	copy(d, b)
	return UTF8String(d), nil
}

func (s UTF8String) Serialize() []byte {
	return OctetString(s).Serialize()
}

func (s UTF8String) Len() int {
	return len(s)
}

func (s UTF8String) Padding() int {
	l := len(s)
	return pad4(l) - l
}

func (s UTF8String) Type() TypeID {
	return UTF8StringType
}

func (s UTF8String) String() string {
	return fmt.Sprintf("UTF8String{%s},Padding:%d", string(s), s.Padding())
}
