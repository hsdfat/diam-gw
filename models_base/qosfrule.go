package models_base

import "fmt"

// QoSFilterRule data type.
type QoSFilterRule OctetString

// DecodeQoSFilterRule decodes an QoSFilterRule data type from byte array.
func DecodeQoSFilterRule(b []byte) (Type, error) {
	d := make([]byte, len(b))
	copy(d, b)
	return QoSFilterRule(OctetString(d)), nil
}

// Serialize implements the Type interface.
func (s QoSFilterRule) Serialize() []byte {
	return OctetString(s).Serialize()
}

// Len implements the Type interface.
func (s QoSFilterRule) Len() int {
	return len(s)
}

// Padding implements the Type interface.
func (s QoSFilterRule) Padding() int {
	l := len(s)
	return pad4(l) - l
}

// Type implements the Type interface.
func (s QoSFilterRule) Type() TypeID {
	return QoSFilterRuleType
}

// String implements the Type interface.
func (s QoSFilterRule) String() string {
	return fmt.Sprintf("QoSFilterRule{%s},Padding:%d", string(s), s.Padding())
}
