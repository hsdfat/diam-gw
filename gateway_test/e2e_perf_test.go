package gateway_test

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hsdfat/diam-gw/client"
	"github.com/hsdfat/diam-gw/pkg/logger"
)

// perfStack is the full MME→DRA→gateway→HSS/EIR rig, reusable across tests.
type perfStack struct {
	hss *S6aHSSSimulator
	eir *S13EIRSimulator
	gw  *client.Gateway

	draAddr string
	stopDRA func()

	mmes []*MMESimulator
}

func (p *perfStack) close() {
	for _, m := range p.mmes {
		m.Stop()
	}
	if p.gw != nil {
		_ = p.gw.Close()
	}
	if p.hss != nil {
		_ = p.hss.Stop()
	}
	if p.eir != nil {
		_ = p.eir.Stop()
	}
	if p.stopDRA != nil {
		p.stopDRA()
	}
}

type perfStackOpts struct {
	withS6a      bool
	withS13      bool
	s6aPortRange [2]int
	s13PortRange [2]int
}

// buildPerfStack wires up one DRA, the requested backends, one gateway with
// the requested pools, and registers forwarders. Returns before any MMEs
// connect — caller adds MMEs via addMME().
func buildPerfStack(t *testing.T, ctx context.Context, opts perfStackOpts) *perfStack {
	t.Helper()

	// One logger instance; raise to "error" to keep perf runs quiet.
	log := logger.New("perf", "error")

	draAddr, stopDRA := startRealDRA(t, "dra-1", "dra1.epc.test")
	draHost, draPort := splitHostPort(t, draAddr)

	stack := &perfStack{draAddr: draAddr, stopDRA: stopDRA}

	pools := map[uint32]*client.DRAPoolConfig{}
	if opts.withS6a {
		stack.hss = NewS6aHSSSimulator(ctx, "127.0.0.1:0", log.With("mod", "hss").(logger.Logger))
		if err := stack.hss.Start(); err != nil {
			stack.close()
			t.Fatalf("start HSS: %v", err)
		}
		pools[client.AppIDS6a] = s6aPoolConfig(draHost, draPort, "gw-s6a.test", "hss.realm",
			opts.s6aPortRange[0], opts.s6aPortRange[1])
		// DRA keys peers by OriginHost (dra/peer.go:92) — a second connection
		// with the same OriginHost evicts the first. Stay at 1 conn/DRA.
	}
	if opts.withS13 {
		stack.eir = NewS13EIRSimulator(ctx, "", "127.0.0.1:0", log.With("mod", "eir").(logger.Logger))
		if err := stack.eir.Start(); err != nil {
			stack.close()
			t.Fatalf("start EIR: %v", err)
		}
		pools[client.AppIDS13] = s13PoolConfig(draHost, draPort, "gw-s13.test", "eir.realm",
			opts.s13PortRange[0], opts.s13PortRange[1])
	}

	gw, err := client.NewGateway(ctx, &client.GatewayConfig{Pools: pools}, log.With("mod", "gw").(logger.Logger))
	if err != nil {
		stack.close()
		t.Fatalf("NewGateway: %v", err)
	}
	stack.gw = gw
	if err := gw.Start(); err != nil {
		stack.close()
		t.Fatalf("gw.Start: %v", err)
	}

	// One backend AddressClient shared across pools; it multiplexes by remote addr.
	var appIDs []uint32
	if opts.withS6a {
		appIDs = append(appIDs, client.AppIDS6a)
	}
	if opts.withS13 {
		appIDs = append(appIDs, client.AppIDS13)
	}
	backend, err := client.NewAddressClient(ctx, backendClientConfig("gw-backend.test", "gw.test", appIDs),
		log.With("mod", "backend").(logger.Logger))
	if err != nil {
		stack.close()
		t.Fatalf("NewAddressClient: %v", err)
	}
	if opts.withS6a {
		registerS6aForwarder(t, gw, backend, stack.hss.ListenAddr())
		waitActiveConns(t, gw.Pool(client.AppIDS6a), []string{"dra-1"}, 1, 5*time.Second)
	}
	if opts.withS13 {
		registerS13Forwarder(t, gw, backend, stack.eir.listenAddr)
		waitActiveConns(t, gw.Pool(client.AppIDS13), []string{"dra-1"}, 1, 5*time.Second)
	}
	return stack
}

func (p *perfStack) addMME(t *testing.T, ctx context.Context, originHost string) *MMESimulator {
	t.Helper()
	log := logger.New("perf-mme", "error")
	m := NewMMESimulator(ctx, p.draAddr, originHost, "epc.test", log)
	if err := m.Start(); err != nil {
		t.Fatalf("start MME %s: %v", originHost, err)
	}
	p.mmes = append(p.mmes, m)
	return m
}

// latencyStats computes p50/p95/p99 on a collected sample. Sorts in place.
type latencyStats struct {
	count int
	min   time.Duration
	p50   time.Duration
	p95   time.Duration
	p99   time.Duration
	max   time.Duration
	mean  time.Duration
}

func computeLatencyStats(samples []time.Duration) latencyStats {
	if len(samples) == 0 {
		return latencyStats{}
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	var sum time.Duration
	for _, s := range samples {
		sum += s
	}
	pct := func(p float64) time.Duration {
		idx := int(float64(len(samples)-1) * p)
		return samples[idx]
	}
	return latencyStats{
		count: len(samples),
		min:   samples[0],
		p50:   pct(0.50),
		p95:   pct(0.95),
		p99:   pct(0.99),
		max:   samples[len(samples)-1],
		mean:  sum / time.Duration(len(samples)),
	}
}

func (s latencyStats) String() string {
	return fmt.Sprintf("n=%d min=%s p50=%s p95=%s p99=%s max=%s mean=%s",
		s.count, s.min, s.p50, s.p95, s.p99, s.max, s.mean)
}

// TestE2E_Perf_SingleMME measures steady-state latency of the full S6a path
// with one MME issuing AIRs sequentially. This isolates per-request cost
// (DRA forward + gateway handler + HSS roundtrip) without socket contention.
func TestE2E_Perf_SingleMME(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping perf in -short")
	}
	const iterations = 500

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	stack := buildPerfStack(t, ctx, perfStackOpts{
		withS6a:      true,
		s6aPortRange: [2]int{48000, 48100},
	})
	defer stack.close()

	mme := stack.addMME(t, ctx, "mme-perf-single.epc.test")

	latencies := make([]time.Duration, 0, iterations)
	var errCount int
	start := time.Now()
	for i := 0; i < iterations; i++ {
		t0 := time.Now()
		_, err := mme.SendAIR(fmt.Sprintf("12345678901%04d", i), "hss.realm", "gw-s6a.test", 5*time.Second)
		if err != nil {
			errCount++
			continue
		}
		latencies = append(latencies, time.Since(t0))
	}
	total := time.Since(start)
	qps := float64(len(latencies)) / total.Seconds()

	stats := computeLatencyStats(latencies)
	t.Logf("single-MME S6a: %s", stats)
	t.Logf("  total=%s errors=%d qps=%.1f", total, errCount, qps)
	t.Logf("  HSS=%+v", stack.hss.GetStats())

	if errCount > iterations/20 { // >5% failure
		t.Errorf("too many errors: %d / %d", errCount, iterations)
	}
}

// TestE2E_Perf_ConcurrentMMEs fans multiple MME peers out against the gateway
// simultaneously. Each MME uses its own TCP socket to the DRA, so we exercise
// DRA's pending-table under concurrent HbH remapping and the gateway's
// per-connection handler dispatcher.
func TestE2E_Perf_ConcurrentMMEs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping perf in -short")
	}
	const (
		numMMEs          = 10
		requestsPerMME   = 100
		totalRequests    = numMMEs * requestsPerMME
		perRequestBudget = 5 * time.Second
	)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	stack := buildPerfStack(t, ctx, perfStackOpts{
		withS6a:      true,
		s6aPortRange: [2]int{48200, 48400},
	})
	defer stack.close()

	mmes := make([]*MMESimulator, numMMEs)
	for i := 0; i < numMMEs; i++ {
		mmes[i] = stack.addMME(t, ctx, fmt.Sprintf("mme-perf-%02d.epc.test", i))
	}

	var (
		successCount atomic.Uint64
		errorCount   atomic.Uint64
		latMu        sync.Mutex
	)
	latencies := make([]time.Duration, 0, totalRequests)

	var wg sync.WaitGroup
	start := time.Now()
	for i := 0; i < numMMEs; i++ {
		wg.Add(1)
		go func(mme *MMESimulator, idx int) {
			defer wg.Done()
			local := make([]time.Duration, 0, requestsPerMME)
			for j := 0; j < requestsPerMME; j++ {
				t0 := time.Now()
				_, err := mme.SendAIR(fmt.Sprintf("1234567890%02d%04d", idx, j),
					"hss.realm", "gw-s6a.test", perRequestBudget)
				if err != nil {
					errorCount.Add(1)
					continue
				}
				local = append(local, time.Since(t0))
				successCount.Add(1)
			}
			latMu.Lock()
			latencies = append(latencies, local...)
			latMu.Unlock()
		}(mmes[i], i)
	}
	wg.Wait()
	total := time.Since(start)
	qps := float64(successCount.Load()) / total.Seconds()

	stats := computeLatencyStats(latencies)
	t.Logf("concurrent-MME S6a (%d MMEs × %d): %s", numMMEs, requestsPerMME, stats)
	t.Logf("  total=%s ok=%d err=%d qps=%.1f",
		total, successCount.Load(), errorCount.Load(), qps)
	t.Logf("  HSS=%+v", stack.hss.GetStats())

	if successCount.Load() < uint64(totalRequests*95/100) {
		t.Errorf("success rate too low: %d / %d", successCount.Load(), totalRequests)
	}
}

// TestE2E_Perf_MixedS6aS13 runs S6a and S13 traffic concurrently against a
// single gateway with both pools. Shows that a slow backend on one interface
// does not starve the other.
func TestE2E_Perf_MixedS6aS13(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping perf in -short")
	}
	const (
		s6aMMEs        = 5
		s13MMEs        = 5
		requestsPerMME = 100
		perReqBudget   = 5 * time.Second
	)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	stack := buildPerfStack(t, ctx, perfStackOpts{
		withS6a:      true,
		withS13:      true,
		s6aPortRange: [2]int{48500, 48700},
		s13PortRange: [2]int{48700, 48900},
	})
	defer stack.close()

	s6aGroup := make([]*MMESimulator, s6aMMEs)
	for i := 0; i < s6aMMEs; i++ {
		s6aGroup[i] = stack.addMME(t, ctx, fmt.Sprintf("mme-s6a-%02d.epc.test", i))
	}
	s13Group := make([]*MMESimulator, s13MMEs)
	for i := 0; i < s13MMEs; i++ {
		s13Group[i] = stack.addMME(t, ctx, fmt.Sprintf("mme-s13-%02d.epc.test", i))
	}

	var (
		s6aOK, s6aErr atomic.Uint64
		s13OK, s13Err atomic.Uint64
		s6aLat, s13Lat []time.Duration
		s6aMu, s13Mu sync.Mutex
	)

	var wg sync.WaitGroup
	start := time.Now()

	for i, mme := range s6aGroup {
		wg.Add(1)
		go func(mme *MMESimulator, idx int) {
			defer wg.Done()
			local := make([]time.Duration, 0, requestsPerMME)
			for j := 0; j < requestsPerMME; j++ {
				t0 := time.Now()
				_, err := mme.SendAIR(fmt.Sprintf("1234567890%02d%04d", idx, j),
					"hss.realm", "gw-s6a.test", perReqBudget)
				if err != nil {
					s6aErr.Add(1)
					continue
				}
				s6aOK.Add(1)
				local = append(local, time.Since(t0))
			}
			s6aMu.Lock()
			s6aLat = append(s6aLat, local...)
			s6aMu.Unlock()
		}(mme, i)
	}

	for i, mme := range s13Group {
		wg.Add(1)
		go func(mme *MMESimulator, idx int) {
			defer wg.Done()
			local := make([]time.Duration, 0, requestsPerMME)
			for j := 0; j < requestsPerMME; j++ {
				t0 := time.Now()
				_, err := mme.SendMICR(fmt.Sprintf("3534900698733%02d%01d", idx, j%10),
					"eir.realm", "gw-s13.test", perReqBudget)
				if err != nil {
					s13Err.Add(1)
					continue
				}
				s13OK.Add(1)
				local = append(local, time.Since(t0))
			}
			s13Mu.Lock()
			s13Lat = append(s13Lat, local...)
			s13Mu.Unlock()
		}(mme, i)
	}
	wg.Wait()
	total := time.Since(start)

	s6aStats := computeLatencyStats(s6aLat)
	s13Stats := computeLatencyStats(s13Lat)
	s6aQps := float64(s6aOK.Load()) / total.Seconds()
	s13Qps := float64(s13OK.Load()) / total.Seconds()

	t.Logf("mixed S6a/S13 over %s", total)
	t.Logf("  S6a: ok=%d err=%d qps=%.1f | %s",
		s6aOK.Load(), s6aErr.Load(), s6aQps, s6aStats)
	t.Logf("  S13: ok=%d err=%d qps=%.1f | %s",
		s13OK.Load(), s13Err.Load(), s13Qps, s13Stats)
	t.Logf("  HSS=%+v", stack.hss.GetStats())
	t.Logf("  EIR=%+v", stack.eir.GetStats())

	totalReqs := uint64(s6aMMEs*requestsPerMME + s13MMEs*requestsPerMME)
	totalOK := s6aOK.Load() + s13OK.Load()
	if totalOK < totalReqs*95/100 {
		t.Errorf("overall success rate too low: %d / %d", totalOK, totalReqs)
	}
}

// TestE2E_Perf_Pipeline exercises the gateway with multiple concurrent in-flight
// requests per MME. Unlike the sequential tests above (one request at a time per
// MME), this test has `pipeline` goroutines per MME all calling SendAIR at once,
// stressing the DRA's pending-table and the gateway dispatcher simultaneously.
func TestE2E_Perf_Pipeline(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping perf in -short")
	}
	const (
		numMMEs        = 5
		pipeline       = 8   // concurrent in-flight per MME
		requestsPerMME = 400 // total AIRs per MME
		perReqBudget   = 5 * time.Second
	)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	stack := buildPerfStack(t, ctx, perfStackOpts{
		withS6a:      true,
		s6aPortRange: [2]int{49000, 49200},
	})
	defer stack.close()

	mmes := make([]*MMESimulator, numMMEs)
	for i := 0; i < numMMEs; i++ {
		mmes[i] = stack.addMME(t, ctx, fmt.Sprintf("mme-pipe-%02d.epc.test", i))
	}

	var (
		successCount atomic.Uint64
		errorCount   atomic.Uint64
		latMu        sync.Mutex
	)
	latencies := make([]time.Duration, 0, numMMEs*requestsPerMME)

	var outerWg sync.WaitGroup
	start := time.Now()

	for i := 0; i < numMMEs; i++ {
		outerWg.Add(1)
		go func(mme *MMESimulator, mmeIdx int) {
			defer outerWg.Done()

			sem := make(chan struct{}, pipeline) // cap inflight to `pipeline`
			var innerWg sync.WaitGroup
			var localMu sync.Mutex
			local := make([]time.Duration, 0, requestsPerMME)

			for j := 0; j < requestsPerMME; j++ {
				sem <- struct{}{}
				innerWg.Add(1)
				go func(j int) {
					defer func() { <-sem; innerWg.Done() }()
					t0 := time.Now()
					_, err := mme.SendAIR(
						fmt.Sprintf("1234567890%02d%04d", mmeIdx, j),
						"hss.realm", "gw-s6a.test", perReqBudget)
					if err != nil {
						errorCount.Add(1)
						return
					}
					d := time.Since(t0)
					successCount.Add(1)
					localMu.Lock()
					local = append(local, d)
					localMu.Unlock()
				}(j)
			}
			innerWg.Wait()

			latMu.Lock()
			latencies = append(latencies, local...)
			latMu.Unlock()
		}(mmes[i], i)
	}

	outerWg.Wait()
	total := time.Since(start)
	totalReqs := uint64(numMMEs * requestsPerMME)
	qps := float64(successCount.Load()) / total.Seconds()

	stats := computeLatencyStats(latencies)
	t.Logf("pipeline S6a (%d MMEs × %d reqs, depth=%d): %s",
		numMMEs, requestsPerMME, pipeline, stats)
	t.Logf("  total=%s ok=%d err=%d qps=%.1f",
		total, successCount.Load(), errorCount.Load(), qps)
	t.Logf("  HSS=%+v", stack.hss.GetStats())

	if successCount.Load() < totalReqs*95/100 {
		t.Errorf("success rate too low: %d / %d", successCount.Load(), totalReqs)
	}
}

// TestE2E_Stress_Sustained runs a sustained load against the full S6a path for
// a configurable duration (default 2 min; set DIAM_STRESS_DURATION=30m for a
// full 30-minute endurance run). It periodically reports a latency snapshot so
// regressions are visible in the log rather than just at end-of-test.
//
// Run with:
//
//	go test ./gateway_test -run TestE2E_Stress_Sustained -v -timeout 35m \
//	    -count=1 DIAM_STRESS_DURATION=30m
func TestE2E_Stress_Sustained(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in -short")
	}

	duration := 2 * time.Minute
	if v := os.Getenv("DIAM_STRESS_DURATION"); v != "" {
		secs, err := strconv.Atoi(v)
		if err != nil {
			// Try time.ParseDuration as well
			if d, err2 := time.ParseDuration(v); err2 == nil {
				duration = d
			} else {
				t.Fatalf("DIAM_STRESS_DURATION %q: %v", v, err)
			}
		} else {
			duration = time.Duration(secs) * time.Second
		}
	}
	t.Logf("stress duration: %s (set DIAM_STRESS_DURATION=30m for full run)", duration)

	const (
		numMMEs    = 8
		pipeline   = 4   // concurrent in-flight per MME
		perReqBudg = 5 * time.Second
		reportEvery = 30 * time.Second
	)

	ctx, cancel := context.WithTimeout(context.Background(), duration+30*time.Second)
	defer cancel()

	stack := buildPerfStack(t, ctx, perfStackOpts{
		withS6a:      true,
		s6aPortRange: [2]int{49300, 49600},
	})
	defer stack.close()

	mmes := make([]*MMESimulator, numMMEs)
	for i := 0; i < numMMEs; i++ {
		mmes[i] = stack.addMME(t, ctx, fmt.Sprintf("mme-stress-%02d.epc.test", i))
	}

	var (
		totalOK  atomic.Uint64
		totalErr atomic.Uint64
		imsiSeq  atomic.Uint64
		latMu    sync.Mutex
		window   []time.Duration // current reporting window samples
	)

	startAll := time.Now()
	deadline := startAll.Add(duration)

	// Periodic reporter
	reportTicker := time.NewTicker(reportEvery)
	defer reportTicker.Stop()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-reportTicker.C:
				latMu.Lock()
				snap := make([]time.Duration, len(window))
				copy(snap, window)
				window = window[:0]
				latMu.Unlock()
				elapsed := time.Since(startAll).Round(time.Second)
				stats := computeLatencyStats(snap)
				t.Logf("[%s] ok=%d err=%d | window: %s",
					elapsed, totalOK.Load(), totalErr.Load(), stats)
			}
		}
	}()

	var wg sync.WaitGroup
	for i := 0; i < numMMEs; i++ {
		wg.Add(1)
		go func(mme *MMESimulator, mmeIdx int) {
			defer wg.Done()
			sem := make(chan struct{}, pipeline)
			var innerWg sync.WaitGroup

			for time.Now().Before(deadline) {
				sem <- struct{}{}
				seq := imsiSeq.Add(1)
				innerWg.Add(1)
				go func(seq uint64) {
					defer func() { <-sem; innerWg.Done() }()
					imsi := fmt.Sprintf("%015d", seq%999999999999999)
					t0 := time.Now()
					_, err := mme.SendAIR(imsi, "hss.realm", "gw-s6a.test", perReqBudg)
					d := time.Since(t0)
					if err != nil {
						totalErr.Add(1)
						return
					}
					totalOK.Add(1)
					latMu.Lock()
					window = append(window, d)
					latMu.Unlock()
				}(seq)
			}
			innerWg.Wait()
		}(mmes[i], i)
	}

	wg.Wait()
	elapsed := time.Since(startAll)
	ok := totalOK.Load()
	errs := totalErr.Load()
	qps := float64(ok) / elapsed.Seconds()

	// Final window flush
	latMu.Lock()
	finalStats := computeLatencyStats(window)
	latMu.Unlock()

	t.Logf("=== sustained stress result ===")
	t.Logf("  duration=%s ok=%d err=%d qps=%.1f", elapsed.Round(time.Second), ok, errs, qps)
	t.Logf("  final window latency: %s", finalStats)
	t.Logf("  HSS=%+v", stack.hss.GetStats())

	total := ok + errs
	if total > 0 && errs*100/total > 5 {
		t.Errorf("error rate %.1f%% exceeds 5%% threshold (%d err / %d total)",
			float64(errs)*100/float64(total), errs, total)
	}
}
