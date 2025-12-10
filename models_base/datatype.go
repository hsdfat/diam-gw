package models_base

type Type interface {
	Serialize() []byte
	Len() int
	Padding() int
	Type() TypeID
	String() string
}

type TypeID int

const (
	UnknownType TypeID = iota
	AddressType
	DiameterIdentityType
	DiameterURIType
	EnumeratedType
	Float32Type
	Float64Type
	GroupedType
	IPFilterRuleType
	IPv4Type
	Integer32Type
	Integer64Type
	OctetStringType
	QoSFilterRuleType
	TimeType
	UTF8StringType
	Unsigned32Type
	Unsigned64Type
	IPv6Type
)

var Available = map[string]TypeID{
	"Address":          AddressType,
	"DiameterIdentity": DiameterIdentityType,
	"DiameterURI":      DiameterURIType,
	"Enumerated":       EnumeratedType,
	"Float32":          Float32Type,
	"Float64":          Float64Type,
	"Grouped":          GroupedType,
	"IPFilterRule":     IPFilterRuleType,
	"IPv4":             IPv4Type,
	"IPv6":             IPv6Type,
	"Integer32":        Integer32Type,
	"Integer64":        Integer64Type,
	"OctetString":      OctetStringType,
	"QoSFilterRule":    QoSFilterRuleType,
	"Time":             TimeType,
	"UTF8String":       UTF8StringType,
	"Unsigned32":       Unsigned32Type,
	"Unsigned64":       Unsigned64Type,
}
