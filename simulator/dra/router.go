package main

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
)

// Router handles message routing to backend servers
type Router struct {
	backends []*Backend
	mu       sync.RWMutex
	counter  atomic.Uint64 // For round-robin
}

// Backend represents a backend Diameter server
type Backend struct {
	Host   string
	Port   int
	Weight int
	Active bool

	// Statistics
	TotalRequests atomic.Uint64
	Failures      atomic.Uint64
}

// NewRouter creates a new router
func NewRouter(backendServers string) (*Router, error) {
	if backendServers == "" {
		return &Router{backends: []*Backend{}}, nil
	}

	backends := make([]*Backend, 0)

	// Parse backend server list (format: host:port,host:port,...)
	serverList := strings.Split(backendServers, ",")
	for _, server := range serverList {
		server = strings.TrimSpace(server)
		if server == "" {
			continue
		}

		parts := strings.Split(server, ":")
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid backend server format: %s (expected host:port)", server)
		}

		var port int
		if _, err := fmt.Sscanf(parts[1], "%d", &port); err != nil {
			return nil, fmt.Errorf("invalid port in backend server: %s", server)
		}

		backends = append(backends, &Backend{
			Host:   parts[0],
			Port:   port,
			Weight: 1,
			Active: true,
		})
	}

	if len(backends) == 0 {
		return nil, fmt.Errorf("no valid backend servers configured")
	}

	return &Router{backends: backends}, nil
}

// SelectBackend selects a backend server using round-robin
func (r *Router) SelectBackend() (*Backend, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.backends) == 0 {
		return nil, fmt.Errorf("no backend servers available")
	}

	// Simple round-robin
	idx := r.counter.Add(1) % uint64(len(r.backends))

	// Find next active backend
	for i := 0; i < len(r.backends); i++ {
		backend := r.backends[(int(idx)+i)%len(r.backends)]
		if backend.Active {
			return backend, nil
		}
	}

	return nil, fmt.Errorf("no active backend servers")
}

// MarkBackendFailed marks a backend as failed
func (r *Router) MarkBackendFailed(backend *Backend) {
	backend.Failures.Add(1)

	// Simple failure threshold: deactivate after 5 failures
	if backend.Failures.Load() > 5 {
		backend.Active = false
	}
}

// GetBackends returns list of all backends
func (r *Router) GetBackends() []*Backend {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*Backend, len(r.backends))
	copy(result, r.backends)
	return result
}
