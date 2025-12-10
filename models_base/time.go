package models_base

import (
	"encoding/binary"
	"fmt"
	"time"
)

type Time time.Time

const (
	rfc868offset = 2208988800
	rfc2030offset = 2085978496
)

func DecodeTime(b []byte) (Type, error) {
	if len(b) != 4 {
		return &Time{}, nil
	}
	ts := int64(binary.BigEndian.Uint32(b))
	if (b[0] >> 7) == 0 {
		ts += rfc2030offset
	} else {
		ts -= rfc868offset
	}
	return Time(time.Unix(ts, 0)), nil
}

func (t Time) Serialize() []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, uint32(time.Time(t).Unix())+rfc868offset)
	return b
}

func (t Time) Len() int {
	return 4
}

func (t Time) Padding() int {
	return 0
}

func (t Time) Type() TypeID {
	return TimeType
}

func (t Time) String() string {
	return fmt.Sprintf("Time{%s}", time.Time(t))
}
