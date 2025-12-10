package models_base

func pad4(n int) int {
	return n + ((4 - n) & 3)
}
