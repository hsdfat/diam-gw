package gateway_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/hsdfat/diam-gw/client"
	"github.com/hsdfat/diam-gw/dra"
	"github.com/hsdfat/diam-gw/pkg/connection"
	"github.com/hsdfat/diam-gw/pkg/logger"
)

func splitHostPort(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort %s: %v", addr, err)
	}
	tcp, err := net.ResolveTCPAddr("tcp", "0.0.0.0:"+portStr)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	return host, tcp.Port
}

// waitActiveConns blocks until every DRA in `draNames` has at least `perDRA`
// Open connections in the pool, or fails the test after `timeout`.
func waitActiveConns(t *testing.T, pool *client.DRAPool, draNames []string, perDRA int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		allGood := true
		for _, name := range draNames {
			cp := pool.GetDRAPool(name)
			if cp == nil {
				allGood = false
				break
			}
			active := 0
			for _, c := range cp.GetAllConnections() {
				if c.IsActive() {
					active++
				}
			}
			if active < perDRA {
				allGood = false
				break
			}
		}
		if allGood {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("not all DRAs reached %d active connections within %s", perDRA, timeout)
}

// startRealDRA boots the real DRA implementation on an ephemeral localhost
// port and returns (addr, stopFn). Differs from the simulator-based DRA in
// other tests: this is the actual dra.DRANode we plan to deploy against.
func startRealDRA(t *testing.T, name, originHost string) (string, func()) {
	t.Helper()

	// Reserve an ephemeral port, then hand it to DRA (it does its own Listen).
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	node := dra.NewDRANode(dra.Config{
		NodeName:      name,
		ListenAddr:    addr,
		OriginHost:    originHost,
		OriginRealm:   "epc.test",
		ProductName:   "test-dra",
		VendorID:      10415,
		HostIPs:       []net.IP{net.ParseIP("127.0.0.1")},
		SupportedApps: []uint32{client.AppIDS6a, client.AppIDS13},
	})

	done := make(chan struct{})
	go func() {
		_ = node.StartNode()
		close(done)
	}()

	// Wait until DRA is accepting.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			_ = c.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	return addr, func() {
		node.Stop()
		<-done
	}
}

// s6aPoolConfig builds a minimal S6a DRAPoolConfig dialing a single DRA.
// OriginHost/OriginRealm decide how the DRA identifies this gateway peer,
// which is what MME's Destination-Host / Destination-Realm must match to
// route traffic here.
func s6aPoolConfig(draHost string, draPort int, gwOriginHost, gwOriginRealm string, portMin, portMax int) *client.DRAPoolConfig {
	return &client.DRAPoolConfig{
		Name:              "s6a",
		LocalAddr:         "127.0.0.1",
		LocalPortMin:      portMin,
		LocalPortMax:      portMax,
		OriginHost:        gwOriginHost,
		OriginRealm:       gwOriginRealm,
		ProductName:       "diam-gw-e2e",
		VendorID:          10415,
		AuthAppIDs:        []uint32{client.AppIDS6a},
		ConnectionsPerDRA: 1,
		DRAs: []*client.DRAServerConfig{
			{Name: "dra-1", Host: draHost, Port: draPort, Priority: 1},
		},
		ConnectTimeout:      2 * time.Second,
		CERTimeout:          2 * time.Second,
		DWRInterval:         30 * time.Second,
		DWRTimeout:          5 * time.Second,
		MaxDWRFailures:      3,
		HealthCheckInterval: 1 * time.Second,
		ReconnectInterval:   500 * time.Millisecond,
		MaxReconnectDelay:   5 * time.Second,
		ReconnectBackoff:    1.5,
		SendBufferSize:      64,
		RecvBufferSize:      64,
	}
}

func s13PoolConfig(draHost string, draPort int, gwOriginHost, gwOriginRealm string, portMin, portMax int) *client.DRAPoolConfig {
	cfg := s6aPoolConfig(draHost, draPort, gwOriginHost, gwOriginRealm, portMin, portMax)
	cfg.Name = "s13"
	cfg.AuthAppIDs = []uint32{client.AppIDS13}
	return cfg
}

// backendClientConfig builds a PoolConfig used by the gateway's AddressClient
// to dial HSS/EIR backends. Origin identity is the gateway side of the link.
func backendClientConfig(gwOriginHost, gwOriginRealm string, appIDs []uint32) *client.PoolConfig {
	return &client.PoolConfig{
		OriginHost:          gwOriginHost,
		OriginRealm:         gwOriginRealm,
		ProductName:         "diam-gw-backend",
		VendorID:            10415,
		DialTimeout:         2 * time.Second,
		SendTimeout:         3 * time.Second,
		CERTimeout:          2 * time.Second,
		DWRInterval:         30 * time.Second,
		DWRTimeout:          5 * time.Second,
		MaxDWRFailures:      3,
		ReconnectEnabled:    true,
		ReconnectInterval:   500 * time.Millisecond,
		MaxReconnectDelay:   5 * time.Second,
		ReconnectBackoff:    1.5,
		SendBufferSize:      64,
		RecvBufferSize:      64,
		AuthAppIDs:          appIDs,
		HealthCheckInterval: 10 * time.Second,
	}
}

// registerS6aForwarder wires up the gateway's S6a pool handler: on incoming
// AIR from the DRA, forward the raw bytes to the HSS backend via AddressClient
// and write the AIA back on the original DRA connection. DRA handles HbH/E2E
// correlation in both directions automatically.
func registerS6aForwarder(t *testing.T, gw *client.Gateway, backend *client.AddressClient, hssAddr string) {
	t.Helper()
	cmd := connection.Command{Interface: int(client.AppIDS6a), Code: 318, Request: true}
	gw.Pool(client.AppIDS6a).HandleFunc(cmd, func(msg *connection.Message, draConn connection.Conn) {
		full := append(msg.Header, msg.Body...)
		aia, err := backend.SendWithTimeout(hssAddr, full, 3*time.Second)
		if err != nil {
			t.Logf("gateway→HSS forward failed: %v", err)
			return
		}
		if _, err := draConn.Write(aia); err != nil {
			t.Logf("gateway→DRA reply failed: %v", err)
		}
	})
}

func registerS13Forwarder(t *testing.T, gw *client.Gateway, backend *client.AddressClient, eirAddr string) {
	t.Helper()
	cmd := connection.Command{Interface: int(client.AppIDS13), Code: 324, Request: true}
	gw.Pool(client.AppIDS13).HandleFunc(cmd, func(msg *connection.Message, draConn connection.Conn) {
		full := append(msg.Header, msg.Body...)
		mica, err := backend.SendWithTimeout(eirAddr, full, 3*time.Second)
		if err != nil {
			t.Logf("gateway→EIR forward failed: %v", err)
			return
		}
		if _, err := draConn.Write(mica); err != nil {
			t.Logf("gateway→DRA reply failed: %v", err)
		}
	})
}

// TestE2E_RealDRA_S6a exercises the full S6a flow end-to-end:
// MME -> real DRA -> diam-gw -> HSS simulator, and AIA back along the same path.
func TestE2E_RealDRA_S6a(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	log := logger.New("s6a-e2e", "warn")

	draAddr, stopDRA := startRealDRA(t, "dra-1", "dra1.epc.test")
	defer stopDRA()
	draHost, draPort := splitHostPort(t, draAddr)

	// HSS backend — ephemeral port so parallel tests don't collide.
	hss := NewS6aHSSSimulator(ctx, "127.0.0.1:0", log.With("mod", "hss").(logger.Logger))
	if err := hss.Start(); err != nil {
		t.Fatalf("start HSS: %v", err)
	}
	defer hss.Stop()

	// Gateway S6a pool: OriginHost=gw-s6a.test, OriginRealm=hss.realm. The MME's
	// AIR targets this exact OriginHost via Destination-Host, which is how the
	// DRA decides to route here (peerByHost lookup in dra/route.go).
	gwCfg := &client.GatewayConfig{
		Pools: map[uint32]*client.DRAPoolConfig{
			client.AppIDS6a: s6aPoolConfig(draHost, draPort, "gw-s6a.test", "hss.realm", 47000, 47100),
		},
	}
	gw, err := client.NewGateway(ctx, gwCfg, log.With("mod", "gw").(logger.Logger))
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	if err := gw.Start(); err != nil {
		t.Fatalf("gw.Start: %v", err)
	}
	defer gw.Close()

	// Backend AddressClient the gateway uses to reach the HSS. Its identity is
	// the gateway's identity to the HSS peer (distinct from the identity the
	// gateway advertises to the DRA, though they can be the same).
	backend, err := client.NewAddressClient(ctx, backendClientConfig("gw-backend.test", "gw.test", []uint32{client.AppIDS6a}), log.With("mod", "backend").(logger.Logger))
	if err != nil {
		t.Fatalf("NewAddressClient: %v", err)
	}

	registerS6aForwarder(t, gw, backend, hss.ListenAddr())

	// Make sure diam-gw is registered with the DRA before the MME sends.
	waitActiveConns(t, gw.Pool(client.AppIDS6a), []string{"dra-1"}, 1, 3*time.Second)

	mme := NewMMESimulator(ctx, draAddr, "mme.epc.test", "epc.test", log.With("mod", "mme").(logger.Logger))
	if err := mme.Start(); err != nil {
		t.Fatalf("start MME: %v", err)
	}
	defer mme.Stop()

	aia, err := mme.SendAIR("123456789012345", "hss.realm", "gw-s6a.test", 5*time.Second)
	if err != nil {
		t.Fatalf("AIR round-trip failed: %v", err)
	}
	hdr, err := parseHdr(aia)
	if err != nil {
		t.Fatalf("bad answer header: %v", err)
	}
	if hdr.cmdCode != 318 || hdr.isRequest {
		t.Errorf("expected AIA (318, answer), got cmd=%d isReq=%v", hdr.cmdCode, hdr.isRequest)
	}
	if hss.GetStats().ResponsesSent == 0 {
		t.Errorf("HSS never responded: stats=%+v", hss.GetStats())
	}
	t.Logf("S6a e2e success: HSS stats=%+v", hss.GetStats())
}

// TestE2E_RealDRA_S13 exercises the full S13 flow: MME -> DRA -> diam-gw -> EIR.
func TestE2E_RealDRA_S13(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	log := logger.New("s13-e2e", "warn")

	draAddr, stopDRA := startRealDRA(t, "dra-1", "dra1.epc.test")
	defer stopDRA()
	draHost, draPort := splitHostPort(t, draAddr)

	eir := NewS13EIRSimulator(ctx, "", "127.0.0.1:0", log.With("mod", "eir").(logger.Logger))
	if err := eir.Start(); err != nil {
		t.Fatalf("start EIR: %v", err)
	}
	defer eir.Stop()

	gwCfg := &client.GatewayConfig{
		Pools: map[uint32]*client.DRAPoolConfig{
			client.AppIDS13: s13PoolConfig(draHost, draPort, "gw-s13.test", "eir.realm", 47200, 47300),
		},
	}
	gw, err := client.NewGateway(ctx, gwCfg, log.With("mod", "gw").(logger.Logger))
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	if err := gw.Start(); err != nil {
		t.Fatalf("gw.Start: %v", err)
	}
	defer gw.Close()

	backend, err := client.NewAddressClient(ctx, backendClientConfig("gw-backend.test", "gw.test", []uint32{client.AppIDS13}), log.With("mod", "backend").(logger.Logger))
	if err != nil {
		t.Fatalf("NewAddressClient: %v", err)
	}

	registerS13Forwarder(t, gw, backend, eir.listenAddr)

	waitActiveConns(t, gw.Pool(client.AppIDS13), []string{"dra-1"}, 1, 3*time.Second)

	mme := NewMMESimulator(ctx, draAddr, "mme.epc.test", "epc.test", log.With("mod", "mme").(logger.Logger))
	if err := mme.Start(); err != nil {
		t.Fatalf("start MME: %v", err)
	}
	defer mme.Stop()

	mica, err := mme.SendMICR("353490069873319", "eir.realm", "gw-s13.test", 5*time.Second)
	if err != nil {
		t.Fatalf("MICR round-trip failed: %v", err)
	}
	hdr, err := parseHdr(mica)
	if err != nil {
		t.Fatalf("bad answer header: %v", err)
	}
	if hdr.cmdCode != 324 || hdr.isRequest {
		t.Errorf("expected MICA (324, answer), got cmd=%d isReq=%v", hdr.cmdCode, hdr.isRequest)
	}
	stats := eir.GetStats()
	if stats.ResponsesSent == 0 {
		t.Errorf("EIR never responded: stats=%+v", stats)
	}
	t.Logf("S13 e2e success: EIR stats=%+v", stats)
}

// TestE2E_RealDRA_Both brings up both pools in one gateway instance and sends
// interleaved AIR + MICR traffic through a single MME peer.
func TestE2E_RealDRA_Both(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	log := logger.New("both-e2e", "warn")

	draAddr, stopDRA := startRealDRA(t, "dra-1", "dra1.epc.test")
	defer stopDRA()
	draHost, draPort := splitHostPort(t, draAddr)

	hss := NewS6aHSSSimulator(ctx, "127.0.0.1:0", log.With("mod", "hss").(logger.Logger))
	if err := hss.Start(); err != nil {
		t.Fatalf("start HSS: %v", err)
	}
	defer hss.Stop()

	eir := NewS13EIRSimulator(ctx, "", "127.0.0.1:0", log.With("mod", "eir").(logger.Logger))
	if err := eir.Start(); err != nil {
		t.Fatalf("start EIR: %v", err)
	}
	defer eir.Stop()

	// Each pool has its own OriginHost so the DRA can route by Destination-Host.
	// Port ranges don't overlap — they must not since the OS binds per-source-port.
	gwCfg := &client.GatewayConfig{
		Pools: map[uint32]*client.DRAPoolConfig{
			client.AppIDS6a: s6aPoolConfig(draHost, draPort, "gw-s6a.test", "hss.realm", 47400, 47500),
			client.AppIDS13: s13PoolConfig(draHost, draPort, "gw-s13.test", "eir.realm", 47600, 47700),
		},
	}
	gw, err := client.NewGateway(ctx, gwCfg, log.With("mod", "gw").(logger.Logger))
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	if err := gw.Start(); err != nil {
		t.Fatalf("gw.Start: %v", err)
	}
	defer gw.Close()

	backend, err := client.NewAddressClient(ctx, backendClientConfig("gw-backend.test", "gw.test", []uint32{client.AppIDS6a, client.AppIDS13}), log.With("mod", "backend").(logger.Logger))
	if err != nil {
		t.Fatalf("NewAddressClient: %v", err)
	}

	registerS6aForwarder(t, gw, backend, hss.ListenAddr())
	registerS13Forwarder(t, gw, backend, eir.listenAddr)

	waitActiveConns(t, gw.Pool(client.AppIDS6a), []string{"dra-1"}, 1, 3*time.Second)
	waitActiveConns(t, gw.Pool(client.AppIDS13), []string{"dra-1"}, 1, 3*time.Second)

	mme := NewMMESimulator(ctx, draAddr, "mme.epc.test", "epc.test", log.With("mod", "mme").(logger.Logger))
	if err := mme.Start(); err != nil {
		t.Fatalf("start MME: %v", err)
	}
	defer mme.Stop()

	// Interleave: AIR, MICR, AIR, MICR.
	for i := 0; i < 2; i++ {
		aia, err := mme.SendAIR("12345678901234"+string(rune('0'+i)), "hss.realm", "gw-s6a.test", 5*time.Second)
		if err != nil {
			t.Fatalf("AIR #%d: %v", i, err)
		}
		if hdr, _ := parseHdr(aia); hdr.cmdCode != 318 || hdr.isRequest {
			t.Errorf("AIR #%d: expected AIA, got cmd=%d req=%v", i, hdr.cmdCode, hdr.isRequest)
		}

		mica, err := mme.SendMICR("35349006987331"+string(rune('0'+i)), "eir.realm", "gw-s13.test", 5*time.Second)
		if err != nil {
			t.Fatalf("MICR #%d: %v", i, err)
		}
		if hdr, _ := parseHdr(mica); hdr.cmdCode != 324 || hdr.isRequest {
			t.Errorf("MICR #%d: expected MICA, got cmd=%d req=%v", i, hdr.cmdCode, hdr.isRequest)
		}
	}

	if hss.GetStats().ResponsesSent < 2 {
		t.Errorf("HSS expected >=2 responses, got %d", hss.GetStats().ResponsesSent)
	}
	if eir.GetStats().ResponsesSent < 2 {
		t.Errorf("EIR expected >=2 responses, got %d", eir.GetStats().ResponsesSent)
	}

	sent, recv := mme.Stats()
	t.Logf("combined e2e: MME sent=%d recv=%d, HSS=%+v, EIR=%+v",
		sent, recv, hss.GetStats(), eir.GetStats())
	if sent != recv {
		t.Errorf("MME sent %d requests but received %d answers", sent, recv)
	}
}
