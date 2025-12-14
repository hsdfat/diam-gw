package metrics

import (
	"fmt"
	"sync"
	"sync/atomic"
)

// MessageTypeMetrics tracks the count of requests sent for each message type (command code)
type MessageTypeMetrics struct {
	counters map[uint32]*atomic.Uint64
	mu       sync.RWMutex
}

// NewMessageTypeMetrics creates a new MessageTypeMetrics instance
func NewMessageTypeMetrics() *MessageTypeMetrics {
	return &MessageTypeMetrics{
		counters: make(map[uint32]*atomic.Uint64),
	}
}

// Increment increments the counter for a specific message type (command code)
func (m *MessageTypeMetrics) Increment(commandCode uint32) {
	m.mu.Lock()
	counter, exists := m.counters[commandCode]
	if !exists {
		counter = &atomic.Uint64{}
		m.counters[commandCode] = counter
	}
	m.mu.Unlock()
	counter.Add(1)
}

// Get returns the count for a specific message type
func (m *MessageTypeMetrics) Get(commandCode uint32) uint64 {
	m.mu.RLock()
	counter, exists := m.counters[commandCode]
	m.mu.RUnlock()

	if !exists {
		return 0
	}
	return counter.Load()
}

// GetAll returns a snapshot of all message type counters
func (m *MessageTypeMetrics) GetAll() map[uint32]uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[uint32]uint64)
	for code, counter := range m.counters {
		result[code] = counter.Load()
	}
	return result
}

// Reset clears all counters
func (m *MessageTypeMetrics) Reset() {
	m.mu.Lock()
	m.counters = make(map[uint32]*atomic.Uint64)
	m.mu.Unlock()
}

// CommandCodeToName maps Diameter command codes to human-readable names
func CommandCodeToName(code uint32) string {
	switch code {
	// Base protocol commands
	case 257:
		return "CER/CEA"
	case 280:
		return "DWR/DWA"
	case 282:
		return "DPR/DPA"
	// 3GPP S6a/S6d commands
	case 303:
		return "ULA"
	case 306:
		return "CLR"
	// 3GPP S13 commands
	case 324:
		return "ECR/ECA"
	// 3GPP S7a commands
	case 325:
		return "AAR/AAA"
	// Other common commands
	case 271:
		return "ACR/ACA"
	case 272:
		return "STR/STA"
	case 275:
		return "ASR/ASA"
	default:
		return fmt.Sprintf("CMD_%d", code)
	}
}

// FormatMetrics formats the metrics for display
func FormatMetrics(direction string, metrics *MessageTypeMetrics) string {
	var output string
	counters := metrics.GetAll()

	output = fmt.Sprintf("\n%s Metrics by Message Type:\n", direction)
	output += "┌─────────────────────────────────┬───────────┐\n"
	output += "│ Message Type                    │ Count     │\n"
	output += "├─────────────────────────────────┼───────────┤\n"

	total := uint64(0)
	for code, count := range counters {
		name := CommandCodeToName(code)
		output += fmt.Sprintf("│ %-31s │ %9d │\n", name, count)
		total += count
	}

	output += "├─────────────────────────────────┼───────────┤\n"
	output += fmt.Sprintf("│ %-31s │ %9d │\n", "TOTAL", total)
	output += "└─────────────────────────────────┴───────────┘\n"

	return output
}

// CompactMetrics formats the metrics in a compact format (single line)
func CompactMetrics(direction string, metrics *MessageTypeMetrics) string {
	output := fmt.Sprintf("%s: ", direction)
	counters := metrics.GetAll()
	total := uint64(0)

	for code, count := range counters {
		if count > 0 {
			name := CommandCodeToName(code)
			output += fmt.Sprintf("[%s=%d] ", name, count)
			total += count
		}
	}

	output += fmt.Sprintf("(Total=%d)", total)
	return output
}
