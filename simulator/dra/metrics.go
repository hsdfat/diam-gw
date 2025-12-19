package main

import (
	"time"
)

// Metrics handles DRA metrics collection and reporting
type Metrics struct {
	interval time.Duration

	// Add custom metrics here as needed
}

// NewMetrics creates a new metrics collector
func NewMetrics(interval time.Duration) *Metrics {
	return &Metrics{
		interval: interval,
	}
}
