package main

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hsdfat/diam-gw/commands/s6a"
	"github.com/hsdfat/diam-gw/models_base"
	"github.com/hsdfat/diam-gw/pkg/logger"
)

// LoadGeneratorConfig holds load generator configuration
type LoadGeneratorConfig struct {
	Duration      time.Duration
	RampUpTime    time.Duration
	S13RatePerSec int
	S6aRatePerSec int
	GxRatePerSec  int
}

// LoadGenerator generates load for performance testing
type LoadGenerator struct {
	dra    *DRA
	config LoadGeneratorConfig
	ctx    context.Context
	cancel context.CancelFunc

	// Statistics
	stats LoadStats
	wg    sync.WaitGroup
}

// LoadStats holds load generation statistics
type LoadStats struct {
	S13Sent       atomic.Uint64
	S13Received   atomic.Uint64
	S6aSent       atomic.Uint64
	S6aReceived   atomic.Uint64
	GxSent        atomic.Uint64
	GxReceived    atomic.Uint64
	TotalSent     atomic.Uint64
	TotalReceived atomic.Uint64
	Errors        atomic.Uint64
	StartTime     time.Time
	EndTime       time.Time
}

// NewLoadGenerator creates a new load generator
func NewLoadGenerator(dra *DRA, config LoadGeneratorConfig) *LoadGenerator {
	ctx, cancel := context.WithCancel(context.Background())

	return &LoadGenerator{
		dra:    dra,
		config: config,
		ctx:    ctx,
		cancel: cancel,
	}
}

// Start starts the load generator
func (lg *LoadGenerator) Start() error {
	logger.Log.Infow("Starting load generator",
		"duration", lg.config.Duration,
		"ramp_up", lg.config.RampUpTime,
		"s13_rate", lg.config.S13RatePerSec,
		"s6a_rate", lg.config.S6aRatePerSec,
		"gx_rate", lg.config.GxRatePerSec)

	lg.stats.StartTime = time.Now()

	// Start generators for each interface
	if lg.config.S13RatePerSec > 0 {
		lg.wg.Add(1)
		go lg.generateS13Traffic()
	}

	if lg.config.S6aRatePerSec > 0 {
		lg.wg.Add(1)
		go lg.generateS6aTraffic()
	}

	if lg.config.GxRatePerSec > 0 {
		lg.wg.Add(1)
		go lg.generateGxTraffic()
	}

	// Start statistics reporter
	lg.wg.Add(1)
	go lg.reportStats()

	// Schedule stop after duration
	time.AfterFunc(lg.config.Duration, func() {
		lg.Stop()
	})

	return nil
}

// Stop stops the load generator
func (lg *LoadGenerator) Stop() {
	logger.Log.Infow("Stopping load generator...")
	lg.cancel()
	lg.stats.EndTime = time.Now()
}

// Wait waits for all generators to finish
func (lg *LoadGenerator) Wait() {
	lg.wg.Wait()
	lg.printFinalStats()
}

// generateS13Traffic generates S13 interface traffic (ME-Identity-Check)
func (lg *LoadGenerator) generateS13Traffic() {
	defer lg.wg.Done()

	logger.Log.Infow("Starting S13 traffic generator", "target_rate", lg.config.S13RatePerSec)

	// Calculate interval between messages
	interval := time.Second / time.Duration(lg.config.S13RatePerSec)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Ramp-up: gradually increase rate
	rampUpSteps := 10
	currentRate := 1
	targetRate := lg.config.S13RatePerSec
	rampUpInterval := lg.config.RampUpTime / time.Duration(rampUpSteps)
	rateIncrement := (targetRate - currentRate) / rampUpSteps

	rampUpTicker := time.NewTicker(rampUpInterval)
	defer rampUpTicker.Stop()

	isRampingUp := lg.config.RampUpTime > 0
	rampUpStart := time.Now()

	for {
		select {
		case <-lg.ctx.Done():
			return

		case <-rampUpTicker.C:
			if isRampingUp && time.Since(rampUpStart) < lg.config.RampUpTime {
				currentRate += rateIncrement
				if currentRate > targetRate {
					currentRate = targetRate
				}
				newInterval := time.Second / time.Duration(currentRate)
				ticker.Reset(newInterval)
				logger.Log.Debugw("S13 rate ramping up", "current_rate", currentRate)
			} else {
				isRampingUp = false
				if currentRate != targetRate {
					currentRate = targetRate
					ticker.Reset(interval)
					logger.Log.Infow("S13 reached target rate", "rate", targetRate)
				}
			}

		case <-ticker.C:
			if err := lg.sendS13Request(); err != nil {
				logger.Log.Debugw("Failed to send S13 request", "error", err)
				lg.stats.Errors.Add(1)
			} else {
				lg.stats.S13Sent.Add(1)
				lg.stats.TotalSent.Add(1)
			}
		}
	}
}

// generateS6aTraffic generates S6a interface traffic (Authentication)
func (lg *LoadGenerator) generateS6aTraffic() {
	defer lg.wg.Done()

	logger.Log.Infow("Starting S6a traffic generator", "target_rate", lg.config.S6aRatePerSec)

	interval := time.Second / time.Duration(lg.config.S6aRatePerSec)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Ramp-up logic similar to S13
	rampUpSteps := 10
	currentRate := 1
	targetRate := lg.config.S6aRatePerSec
	rampUpInterval := lg.config.RampUpTime / time.Duration(rampUpSteps)
	rateIncrement := (targetRate - currentRate) / rampUpSteps

	rampUpTicker := time.NewTicker(rampUpInterval)
	defer rampUpTicker.Stop()

	isRampingUp := lg.config.RampUpTime > 0
	rampUpStart := time.Now()

	for {
		select {
		case <-lg.ctx.Done():
			return

		case <-rampUpTicker.C:
			if isRampingUp && time.Since(rampUpStart) < lg.config.RampUpTime {
				currentRate += rateIncrement
				if currentRate > targetRate {
					currentRate = targetRate
				}
				newInterval := time.Second / time.Duration(currentRate)
				ticker.Reset(newInterval)
				logger.Log.Debugw("S6a rate ramping up", "current_rate", currentRate)
			} else {
				isRampingUp = false
				if currentRate != targetRate {
					currentRate = targetRate
					ticker.Reset(interval)
					logger.Log.Infow("S6a reached target rate", "rate", targetRate)
				}
			}

		case <-ticker.C:
			if err := lg.sendS6aRequest(); err != nil {
				logger.Log.Debugw("Failed to send S6a request", "error", err)
				lg.stats.Errors.Add(1)
			} else {
				lg.stats.S6aSent.Add(1)
				lg.stats.TotalSent.Add(1)
			}
		}
	}
}

// generateGxTraffic generates Gx interface traffic (Policy Control)
func (lg *LoadGenerator) generateGxTraffic() {
	defer lg.wg.Done()

	logger.Log.Infow("Starting Gx traffic generator", "target_rate", lg.config.GxRatePerSec)

	interval := time.Second / time.Duration(lg.config.GxRatePerSec)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Ramp-up logic
	rampUpSteps := 10
	currentRate := 1
	targetRate := lg.config.GxRatePerSec
	rampUpInterval := lg.config.RampUpTime / time.Duration(rampUpSteps)
	rateIncrement := (targetRate - currentRate) / rampUpSteps

	rampUpTicker := time.NewTicker(rampUpInterval)
	defer rampUpTicker.Stop()

	isRampingUp := lg.config.RampUpTime > 0
	rampUpStart := time.Now()

	for {
		select {
		case <-lg.ctx.Done():
			return

		case <-rampUpTicker.C:
			if isRampingUp && time.Since(rampUpStart) < lg.config.RampUpTime {
				currentRate += rateIncrement
				if currentRate > targetRate {
					currentRate = targetRate
				}
				newInterval := time.Second / time.Duration(currentRate)
				ticker.Reset(newInterval)
				logger.Log.Debugw("Gx rate ramping up", "current_rate", currentRate)
			} else {
				isRampingUp = false
				if currentRate != targetRate {
					currentRate = targetRate
					ticker.Reset(interval)
					logger.Log.Infow("Gx reached target rate", "rate", targetRate)
				}
			}

		case <-ticker.C:
			if err := lg.sendGxRequest(); err != nil {
				logger.Log.Debugw("Failed to send Gx request", "error", err)
				lg.stats.Errors.Add(1)
			} else {
				lg.stats.GxSent.Add(1)
				lg.stats.TotalSent.Add(1)
			}
		}
	}
}

// sendS13Request sends a ME-Identity-Check-Request
func (lg *LoadGenerator) sendS13Request() error {
	conn := lg.dra.GetFirstConnection()
	if conn == nil {
		return fmt.Errorf("no connections available")
	}

	// Generate random IMEI
	imei := fmt.Sprintf("%015d", rand.Int63n(1000000000000000))
	return conn.SendMICR(imei)
}

// sendS6aRequest sends an Authentication-Information-Request
func (lg *LoadGenerator) sendS6aRequest() error {
	conn := lg.dra.GetFirstConnection()
	if conn == nil {
		return fmt.Errorf("no connections available")
	}

	// Create AIR
	air := s6a.NewAuthenticationInformationRequest()
	air.SessionId = models_base.UTF8String(fmt.Sprintf("%s;%d;%d",
		lg.dra.config.OriginHost, time.Now().Unix(), rand.Int63()))
	air.AuthSessionState = models_base.Enumerated(1) // NO_STATE_MAINTAINED
	air.OriginHost = models_base.DiameterIdentity(lg.dra.config.OriginHost)
	air.OriginRealm = models_base.DiameterIdentity(lg.dra.config.OriginRealm)

	// Set destination
	peerHost := conn.peerHost.Load()
	peerRealm := conn.peerRealm.Load()
	if peerHost == nil || peerRealm == nil {
		return fmt.Errorf("peer identity not available")
	}

	destHost := models_base.DiameterIdentity(peerHost.(string))
	air.DestinationHost = &destHost
	air.DestinationRealm = models_base.DiameterIdentity(peerRealm.(string))

	// Set User-Name (IMSI)
	imsi := fmt.Sprintf("%015d", rand.Int63n(1000000000000000))
	air.UserName = models_base.UTF8String(imsi)

	// Set Visited-PLMN-Id (example: MCC=001, MNC=01)
	visitedPlmnId := []byte{0x00, 0x10, 0x10}
	air.VisitedPlmnId = models_base.OctetString(visitedPlmnId)

	// Generate H2H and E2E IDs
	air.Header.HopByHopID = uint32(time.Now().UnixNano() & 0xFFFFFFFF)
	air.Header.EndToEndID = uint32(time.Now().UnixNano() & 0xFFFFFFFF)

	// Marshal AIR
	airData, err := air.Marshal()
	if err != nil {
		return fmt.Errorf("failed to marshal AIR: %w", err)
	}

	// Send AIR
	select {
	case conn.sendCh <- airData:
		logger.Log.Debugw("AIR sent", "conn_id", conn.id, "imsi", imsi, "session_id", air.SessionId)
		return nil
	case <-lg.ctx.Done():
		return lg.ctx.Err()
	case <-time.After(5 * time.Second):
		return fmt.Errorf("timeout sending AIR")
	}
}

// sendGxRequest sends a Credit-Control-Request (placeholder for now)
func (lg *LoadGenerator) sendGxRequest() error {
	// For now, just count as sent
	// TODO: Implement Gx CCR when Gx commands are available
	logger.Log.Debugw("Gx request not implemented yet")
	return nil
}

// reportStats periodically reports statistics
func (lg *LoadGenerator) reportStats() {
	defer lg.wg.Done()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-lg.ctx.Done():
			return
		case <-ticker.C:
			lg.printCurrentStats()
		}
	}
}

// printCurrentStats prints current statistics
func (lg *LoadGenerator) printCurrentStats() {
	elapsed := time.Since(lg.stats.StartTime)
	totalSent := lg.stats.TotalSent.Load()
	totalReceived := lg.stats.TotalReceived.Load()

	rate := float64(totalSent) / elapsed.Seconds()

	logger.Log.Infow("=== Load Generator Stats ===")
	logger.Log.Infow("Elapsed Time", "duration", elapsed.Round(time.Second))
	logger.Log.Infow("Messages Sent",
		"s13", lg.stats.S13Sent.Load(),
		"s6a", lg.stats.S6aSent.Load(),
		"gx", lg.stats.GxSent.Load(),
		"total", totalSent)
	logger.Log.Infow("Messages Received",
		"s13", lg.stats.S13Received.Load(),
		"s6a", lg.stats.S6aReceived.Load(),
		"gx", lg.stats.GxReceived.Load(),
		"total", totalReceived)
	logger.Log.Infow("Throughput", "rate", fmt.Sprintf("%.2f req/s", rate))
	logger.Log.Infow("Errors", "count", lg.stats.Errors.Load())
	logger.Log.Infow("===========================")
}

// printFinalStats prints final statistics
func (lg *LoadGenerator) printFinalStats() {
	if lg.stats.EndTime.IsZero() {
		lg.stats.EndTime = time.Now()
	}

	duration := lg.stats.EndTime.Sub(lg.stats.StartTime)
	totalSent := lg.stats.TotalSent.Load()
	totalReceived := lg.stats.TotalReceived.Load()

	avgRate := float64(totalSent) / duration.Seconds()

	logger.Log.Infow("╔════════════════════════════════════════════════════════════╗")
	logger.Log.Infow("║           Load Generator Final Statistics                 ║")
	logger.Log.Infow("╚════════════════════════════════════════════════════════════╝")
	logger.Log.Infow("Test Duration", "duration", duration.Round(time.Second))
	logger.Log.Infow("Messages Sent by Interface",
		"S13", lg.stats.S13Sent.Load(),
		"S6a", lg.stats.S6aSent.Load(),
		"Gx", lg.stats.GxSent.Load(),
		"Total", totalSent)
	logger.Log.Infow("Messages Received",
		"S13", lg.stats.S13Received.Load(),
		"S6a", lg.stats.S6aReceived.Load(),
		"Gx", lg.stats.GxReceived.Load(),
		"Total", totalReceived)
	logger.Log.Infow("Performance",
		"avg_rate", fmt.Sprintf("%.2f req/s", avgRate),
		"target_rate", lg.config.S13RatePerSec+lg.config.S6aRatePerSec+lg.config.GxRatePerSec)
	logger.Log.Infow("Errors", "total", lg.stats.Errors.Load())
	logger.Log.Infow("Success Rate",
		"percentage", fmt.Sprintf("%.2f%%", float64(totalSent-lg.stats.Errors.Load())/float64(totalSent)*100))
}
