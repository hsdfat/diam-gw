package client

import (
	"encoding/binary"
	"testing"
)

func TestParseMessageHeader(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		wantErr bool
	}{
		{
			name:    "too short",
			data:    make([]byte, 10),
			wantErr: true,
		},
		{
			name: "valid CER",
			data: func() []byte {
				header := make([]byte, 20)
				header[0] = 1    // version
				header[1] = 0    // length byte 1 (3-byte length field)
				header[2] = 0    // length byte 2
				header[3] = 100  // length byte 3 (total = 100)
				header[4] = 0x80 // flags (request)
				header[5] = 0    // command code byte 1 (3-byte command code field)
				header[6] = 1    // command code byte 2
				header[7] = 1    // command code byte 3 (total = 257)
				binary.BigEndian.PutUint32(header[8:12], 0)   // application ID
				binary.BigEndian.PutUint32(header[12:16], 1)  // hop-by-hop
				binary.BigEndian.PutUint32(header[16:20], 1)  // end-to-end
				return header
			}(),
			wantErr: false,
		},
		{
			name: "invalid version",
			data: func() []byte {
				header := make([]byte, 20)
				header[0] = 2 // invalid version
				binary.BigEndian.PutUint32([]byte{0, header[1], header[2], header[3]}, 100)
				return header
			}(),
			wantErr: true,
		},
		{
			name: "invalid length",
			data: func() []byte {
				header := make([]byte, 20)
				header[0] = 1  // version
				header[1] = 0  // length byte 1
				header[2] = 0  // length byte 2
				header[3] = 10 // length byte 3 (total = 10, too short)
				return header
			}(),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := ParseMessageHeader(tt.data)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseMessageHeader() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr && info == nil {
				t.Error("ParseMessageHeader() returned nil info without error")
			}
		})
	}
}

func TestMessageInfo_IsBaseProtocol(t *testing.T) {
	tests := []struct {
		name          string
		applicationID uint32
		expected      bool
	}{
		{"base protocol", 0, true},
		{"S13 application", 16777252, false},
		{"other application", 12345, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &MessageInfo{ApplicationID: tt.applicationID}
			if got := info.IsBaseProtocol(); got != tt.expected {
				t.Errorf("IsBaseProtocol() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestResultCode_IsSuccess(t *testing.T) {
	tests := []struct {
		code     ResultCode
		expected bool
	}{
		{ResultCodeSuccess, true},
		{ResultCode(2999), true},
		{ResultCodeCommandUnsupported, false},
		{ResultCodeTooBusy, false},
		{ResultCodeAVPUnsupported, false},
	}

	for _, tt := range tests {
		t.Run(tt.code.String(), func(t *testing.T) {
			if got := tt.code.IsSuccess(); got != tt.expected {
				t.Errorf("IsSuccess() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestResultCode_String(t *testing.T) {
	tests := []struct {
		code     ResultCode
		expected string
	}{
		{ResultCodeSuccess, "DIAMETER_SUCCESS"},
		{ResultCodeCommandUnsupported, "DIAMETER_COMMAND_UNSUPPORTED"},
		{ResultCodeTooBusy, "DIAMETER_TOO_BUSY"},
		{ResultCode(9999), "RESULT_CODE_9999"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			got := tt.code.String()
			if got != tt.expected {
				t.Errorf("String() = %v, want %v", got, tt.expected)
			}
		})
	}
}
