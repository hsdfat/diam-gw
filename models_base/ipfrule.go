package models_base

import "fmt"

// IPFilterRule data type.
type IPFilterRule OctetString

// DecodeIPFilterRule decodes an IPFilterRule data type from byte array.
func DecodeIPFilterRule(b []byte) (Type, error) {
	d := make([]byte, len(b))
	copy(d, b)
	return IPFilterRule(OctetString(d)), nil
}

// Serialize implements the Type interface.
func (s IPFilterRule) Serialize() []byte {
	return OctetString(s).Serialize()
}

// Len implements the Type interface.
func (s IPFilterRule) Len() int {
	return len(s)
}

// Padding implements the Type interface.
func (s IPFilterRule) Padding() int {
	l := len(s)
	return pad4(l) - l
}

// Type implements the Type interface.
func (s IPFilterRule) Type() TypeID {
	return IPFilterRuleType
}

// String implements the Type interface.
func (s IPFilterRule) String() string {
	return fmt.Sprintf("IPFilterRule{%s},Padding:%d", string(s), s.Padding())
}
