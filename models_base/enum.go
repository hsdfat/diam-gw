package models_base

import "fmt"

type Enumerated Integer32

func DecodeEnumerated(b []byte) (Type, error) {
	v, err := DecodeInteger32(b)
	return Enumerated(v.(Integer32)), err
}

func (n Enumerated) Serialize() []byte {
	return Integer32(n).Serialize()
}

func (n Enumerated) Len() int {
	return 4
}

func (n Enumerated) Padding() int {
	return 0
}

func (n Enumerated) Type() TypeID {
	return EnumeratedType
}

func (n Enumerated) String() string {
	return fmt.Sprintf("Enumerated{%d}", n)
}
