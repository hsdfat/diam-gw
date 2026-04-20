package client

import (
	"context"
	"fmt"
	"sync"

	"github.com/hsdfat/diam-gw/pkg/connection"
	"github.com/hsdfat/diam-gw/pkg/logger"
)

// Application IDs for 3GPP Diameter interfaces
const (
	AppIDS6a uint32 = 16777251
	AppIDS13 uint32 = 16777252
)

// GatewayConfig holds configuration for a multi-application Diameter gateway.
// Each pool is fully independent: its own local IP, port range, origin host,
// realm, and list of DRAs with priorities.
type GatewayConfig struct {
	Pools map[uint32]*DRAPoolConfig // key: Diameter Application-ID
}

// Gateway routes Diameter messages to the appropriate application-specific
// DRA pool. Pools do not share any state — an S13 outage cannot impact S6a.
type Gateway struct {
	pools  map[uint32]*DRAPool
	logger logger.Logger
	ctx    context.Context
	cancel context.CancelFunc
}

// NewGateway builds a Gateway with one DRAPool per configured Application-ID.
func NewGateway(ctx context.Context, cfg *GatewayConfig, log logger.Logger) (*Gateway, error) {
	if cfg == nil || len(cfg.Pools) == 0 {
		return nil, fmt.Errorf("GatewayConfig must define at least one pool")
	}
	if log == nil {
		log = logger.New("diameter-gateway", "info")
	}

	ctx, cancel := context.WithCancel(ctx)

	gw := &Gateway{
		pools:  make(map[uint32]*DRAPool, len(cfg.Pools)),
		logger: log,
		ctx:    ctx,
		cancel: cancel,
	}

	for appID, poolCfg := range cfg.Pools {
		pool, err := NewDRAPool(ctx, poolCfg, log)
		if err != nil {
			cancel()
			return nil, fmt.Errorf("pool for app-id %d (%s): %w", appID, poolCfg.Name, err)
		}
		gw.pools[appID] = pool
	}

	return gw, nil
}

// Start starts all application pools concurrently.
func (g *Gateway) Start() error {
	var wg sync.WaitGroup
	errCh := make(chan error, len(g.pools))

	for appID, pool := range g.pools {
		wg.Add(1)
		go func(id uint32, p *DRAPool) {
			defer wg.Done()
			if err := p.Start(); err != nil {
				errCh <- fmt.Errorf("app-id %d: %w", id, err)
			}
		}(appID, pool)
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		// First error wins; callers can inspect per-pool health via Pool()
		return err
	}
	return nil
}

// Send dispatches a message to the pool registered for appID, using priority
// + round-robin selection.
func (g *Gateway) Send(appID uint32, data []byte) error {
	pool, ok := g.pools[appID]
	if !ok {
		return fmt.Errorf("no pool configured for Application-ID %d", appID)
	}
	return pool.Send(data)
}

// SendToDRA dispatches a message to a specific DRA within the pool for appID,
// bypassing priority routing. Returns an error if the DRA is unknown or
// currently unhealthy.
func (g *Gateway) SendToDRA(appID uint32, draName string, data []byte) error {
	pool, ok := g.pools[appID]
	if !ok {
		return fmt.Errorf("no pool configured for Application-ID %d", appID)
	}
	return pool.SendToDRA(draName, data)
}

// Pool returns the DRAPool for an application, or nil if none is registered.
func (g *Gateway) Pool(appID uint32) *DRAPool {
	return g.pools[appID]
}

// HandleFunc registers a response handler on every underlying pool.
func (g *Gateway) HandleFunc(cmd connection.Command, handler Handler) {
	for _, pool := range g.pools {
		pool.HandleFunc(cmd, handler)
	}
}

// Close shuts down all application pools.
func (g *Gateway) Close() error {
	g.cancel()
	var firstErr error
	for appID, pool := range g.pools {
		if err := pool.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("closing pool %d: %w", appID, err)
		}
	}
	return firstErr
}
