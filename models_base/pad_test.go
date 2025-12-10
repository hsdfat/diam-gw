package models_base

import "testing"

func TestPad4(t *testing.T) {
	if n := pad4(2); n != 4 {
		t.Fatalf("Unexpected result. Want 4, have %d", n)
	}
}
