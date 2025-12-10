package models_base

import "fmt"

// DiameterIdentity data type.
type DiameterIdentity OctetString

// DecodeDiameterIdentity decodes a DiameterIdentity from byte array.
func DecodeDiameterIdentity(b []byte) (Type, error) {
	d := make([]byte, len(b))
	copy(d, b)
	return DiameterIdentity(d), nil
}

// Serialize implements the Type interface.
func (s DiameterIdentity) Serialize() []byte {
	return []byte(s)
}

// Len implements the Type interface.
func (s DiameterIdentity) Len() int {
	return len(s)
}

// Padding implements the Type interface.
func (s DiameterIdentity) Padding() int {
	l := len(s)
	return pad4(l) - l
}

// Type implements the Type interface.
func (s DiameterIdentity) Type() TypeID {
	return DiameterIdentityType
}

// String implements the Type interface.
func (s DiameterIdentity) String() string {
	return fmt.Sprintf("DiameterIdentity{%s},Padding:%d", string(s), s.Padding())
}
