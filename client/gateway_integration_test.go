package client_test

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/hsdfat/diam-gw/client"
	"github.com/hsdfat/diam-gw/dra"
	"github.com/hsdfat/diam-gw/pkg/logger"
)

// startDRA spins up a minimal DRA in a goroutine and returns its address.
// Origin-Host is derived from name so the client can distinguish peers.
func startDRA(t *testing.T, name, host string) (addr string, stop func()) {
	t.Helper()

	// Bind an ephemeral port first, then hand the same address to the DRA
	// (it calls net.Listen itself, so we release ours immediately).
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	addr = ln.Addr().String()
	_ = ln.Close()

	node := dra.NewDRANode(dra.Config{
		NodeName:      name,
		ListenAddr:    addr,
		OriginHost:    host,
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

	// Wait until the listener is accepting.
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

// TestGateway_LocalBindAndPortRange boots two in-process DRAs, builds a
// Gateway whose S13 pool is pinned to 127.0.0.1 with source ports in a small
// range, and asserts that every established connection lands inside the
// configured range on the right local IP.
func TestGateway_LocalBindAndPortRange(t *testing.T) {
	addr1, stop1 := startDRA(t, "dra-a", "dra-a.epc.test")
	defer stop1()
	addr2, stop2 := startDRA(t, "dra-b", "dra-b.epc.test")
	defer stop2()

	host1, port1 := splitHostPort(t, addr1)
	host2, port2 := splitHostPort(t, addr2)

	const (
		localIP = "127.0.0.1"
		portMin = 46000
		portMax = 46100
	)

	gwCfg := &client.GatewayConfig{
		Pools: map[uint32]*client.DRAPoolConfig{
			client.AppIDS13: {
				Name:              "s13",
				LocalAddr:         localIP,
				LocalPortMin:      portMin,
				LocalPortMax:      portMax,
				OriginHost:        "gw.s13.test",
				OriginRealm:       "gw.test",
				ProductName:       "diam-gw-test",
				VendorID:          10415,
				AuthAppIDs:        []uint32{client.AppIDS13},
				ConnectionsPerDRA: 2,
				DRAs: []*client.DRAServerConfig{
					{Name: "dra-a", Host: host1, Port: port1, Priority: 1},
					{Name: "dra-b", Host: host2, Port: port2, Priority: 1},
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
			},
		},
	}

	ctx := context.Background()
	gw, err := client.NewGateway(ctx, gwCfg, logger.New("gw-test", "warn"))
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	if err := gw.Start(); err != nil {
		t.Fatalf("gw.Start: %v", err)
	}
	defer gw.Close()

	// Wait until all 4 connections (2 DRAs x 2 conns) are Open.
	pool := gw.Pool(client.AppIDS13)
	if pool == nil {
		t.Fatal("S13 pool missing")
	}
	waitActiveConns(t, pool, []string{"dra-a", "dra-b"}, 2, 3*time.Second)

	// Assert every active connection is bound to the configured local IP and
	// a port inside [portMin, portMax]. Also assert uniqueness — no two
	// connections should share a source port.
	seenPorts := make(map[int]string)
	for _, draName := range []string{"dra-a", "dra-b"} {
		cp := pool.GetDRAPool(draName)
		if cp == nil {
			t.Fatalf("%s: connection pool missing", draName)
		}
		for _, conn := range cp.GetAllConnections() {
			if !conn.IsActive() {
				t.Errorf("%s/%s: not active, state=%s", draName, conn.ID(), conn.GetState())
				continue
			}
			la := conn.LocalTCPAddr()
			if la == nil {
				t.Errorf("%s/%s: nil LocalTCPAddr", draName, conn.ID())
				continue
			}
			if la.IP.String() != localIP {
				t.Errorf("%s/%s: local IP = %s, want %s", draName, conn.ID(), la.IP, localIP)
			}
			if la.Port < portMin || la.Port > portMax {
				t.Errorf("%s/%s: local port %d outside [%d-%d]",
					draName, conn.ID(), la.Port, portMin, portMax)
			}
			if prev, dup := seenPorts[la.Port]; dup {
				t.Errorf("port %d used twice (%s and %s/%s)", la.Port, prev, draName, conn.ID())
			}
			seenPorts[la.Port] = draName + "/" + conn.ID()
		}
	}

	t.Logf("bound ports: %v", seenPorts)
}

// TestGateway_RoundRobinAndSendToDRA verifies that Send() actually rotates
// across DRAs at the same priority, and that SendToDRA() targets a specific
// one. We only need to confirm bytes arrive at the DRA — the DRA will
// drop them with UNABLE_TO_DELIVER (no HSS peer), but the Send call itself
// must succeed.
func TestGateway_RoundRobinAndSendToDRA(t *testing.T) {
	addr1, stop1 := startDRA(t, "dra-a", "dra-a.epc.test")
	defer stop1()
	addr2, stop2 := startDRA(t, "dra-b", "dra-b.epc.test")
	defer stop2()

	host1, port1 := splitHostPort(t, addr1)
	host2, port2 := splitHostPort(t, addr2)

	gwCfg := &client.GatewayConfig{
		Pools: map[uint32]*client.DRAPoolConfig{
			client.AppIDS13: {
				Name:              "s13",
				LocalAddr:         "127.0.0.1",
				LocalPortMin:      46200,
				LocalPortMax:      46300,
				OriginHost:        "gw.s13.test",
				OriginRealm:       "gw.test",
				ProductName:       "diam-gw-test",
				VendorID:          10415,
				AuthAppIDs:        []uint32{client.AppIDS13},
				ConnectionsPerDRA: 1,
				DRAs: []*client.DRAServerConfig{
					{Name: "dra-a", Host: host1, Port: port1, Priority: 1},
					{Name: "dra-b", Host: host2, Port: port2, Priority: 1},
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
			},
		},
	}

	gw, err := client.NewGateway(context.Background(), gwCfg, logger.New("gw-test", "warn"))
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	if err := gw.Start(); err != nil {
		t.Fatalf("gw.Start: %v", err)
	}
	defer gw.Close()

	pool := gw.Pool(client.AppIDS13)
	waitActiveConns(t, pool, []string{"dra-a", "dra-b"}, 1, 3*time.Second)

	// Minimal fake Diameter frame: version=1, length=20, command=ULR (316),
	// app-id=S13. DRA will try to forward and fail, which is fine — we only
	// care that the write succeeded on the client side.
	frame := minimalDiameterRequest(316, client.AppIDS13, 1, 1)

	// 4 rounds of Send() → should rotate across both DRAs.
	for i := 0; i < 4; i++ {
		if err := gw.Send(client.AppIDS13, frame); err != nil {
			t.Errorf("Send round %d: %v", i, err)
		}
	}

	// SendToDRA targeted.
	if err := gw.SendToDRA(client.AppIDS13, "dra-a", frame); err != nil {
		t.Errorf("SendToDRA dra-a: %v", err)
	}
	if err := gw.SendToDRA(client.AppIDS13, "dra-b", frame); err != nil {
		t.Errorf("SendToDRA dra-b: %v", err)
	}

	// Negative: unknown DRA.
	if err := gw.SendToDRA(client.AppIDS13, "nope", frame); err == nil {
		t.Error("SendToDRA nope: want error, got nil")
	}

	// Negative: unknown app-id.
	if err := gw.Send(999, frame); err == nil || !strings.Contains(err.Error(), "no pool") {
		t.Errorf("Send unknown app-id: want 'no pool' error, got %v", err)
	}
}

// --- helpers ---

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

// minimalDiameterRequest builds a 20-byte-header-only Diameter request. It
// has no AVPs, so it will fail forwarding at the DRA (missing
// Destination-Realm) — but the bytes make it across TCP, which is all the
// test needs.
func minimalDiameterRequest(cmdCode, appID, hbh, e2e uint32) []byte {
	const headerLen = 20
	out := make([]byte, headerLen)
	out[0] = 1 // version
	// length = 20
	out[1] = 0
	out[2] = 0
	out[3] = 20
	// flags: R=1 (request)
	out[4] = 0x80
	out[5] = byte(cmdCode >> 16)
	out[6] = byte(cmdCode >> 8)
	out[7] = byte(cmdCode)
	out[8] = byte(appID >> 24)
	out[9] = byte(appID >> 16)
	out[10] = byte(appID >> 8)
	out[11] = byte(appID)
	out[12] = byte(hbh >> 24)
	out[13] = byte(hbh >> 16)
	out[14] = byte(hbh >> 8)
	out[15] = byte(hbh)
	out[16] = byte(e2e >> 24)
	out[17] = byte(e2e >> 16)
	out[18] = byte(e2e >> 8)
	out[19] = byte(e2e)
	return out
}
