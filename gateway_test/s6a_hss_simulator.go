package gateway_test

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"sync/atomic"
	"time"

	"github.com/hsdfat/diam-gw/commands/base"
	"github.com/hsdfat/diam-gw/models_base"
	"github.com/hsdfat/diam-gw/pkg/logger"
	"github.com/hsdfat/diam-gw/server"
)

// S6aHSSSimulator simulates an HSS for the S6a interface. It accepts AIR
// (command 318, app 16777251) and replies with AIA by copying the request
// bytes and flipping the R flag — same trick the S13 EIR simulator uses.
// Good enough for the integration tests; HbH/E2E match by construction.
type S6aHSSSimulator struct {
	server     *server.Server
	listenAddr string
	ctx        context.Context
	cancel     context.CancelFunc
	logger     logger.Logger

	requestsReceived atomic.Uint64
	responsesSent    atomic.Uint64
	errorCount       atomic.Uint64

	originHost  string
	originRealm string
}

type S6aHSSStats struct {
	RequestsReceived uint64
	ResponsesSent    uint64
	Errors           uint64
}

func NewS6aHSSSimulator(ctx context.Context, localAddress string, log logger.Logger) *S6aHSSSimulator {
	ctx, cancel := context.WithCancel(ctx)
	return &S6aHSSSimulator{
		listenAddr:  localAddress,
		ctx:         ctx,
		cancel:      cancel,
		logger:      log,
		originHost:  "hss-s6a.example.com",
		originRealm: "example.com",
	}
}

func (h *S6aHSSSimulator) Start() error {
	config := &server.ServerConfig{
		ListenAddress:  h.listenAddr,
		MaxConnections: 10,
		ConnectionConfig: &server.ConnectionConfig{
			OriginHost:       h.originHost,
			OriginRealm:      h.originRealm,
			ProductName:      "S6a-HSS-Simulator",
			VendorID:         10415,
			ReadTimeout:      30 * time.Second,
			WriteTimeout:     10 * time.Second,
			WatchdogInterval: 30 * time.Second,
			WatchdogTimeout:  10 * time.Second,
			MaxMessageSize:   65535,
			SendChannelSize:  1000,
			RecvChannelSize:  1000,
			HandleWatchdog:   true,
		},
		RecvChannelSize: 1000,
	}

	h.server = server.NewServer(config, h.logger)
	h.registerHandlers()
	go h.processMessages()

	go func() {
		if err := h.server.Start(); err != nil {
			h.logger.Errorw("HSS server error", "error", err)
		}
	}()

	time.Sleep(100 * time.Millisecond)
	if h.server.GetListener() != nil {
		h.listenAddr = h.server.GetListener().Addr().String()
	}
	h.logger.Infow("S6a HSS simulator started", "address", h.listenAddr)
	return nil
}

func (h *S6aHSSSimulator) Stop() error {
	h.cancel()
	if h.server != nil {
		return h.server.Stop()
	}
	return nil
}

func (h *S6aHSSSimulator) ListenAddr() string { return h.listenAddr }

func (h *S6aHSSSimulator) registerHandlers() {
	h.server.HandleFunc(server.Command{Interface: 0, Code: 257, Request: true}, h.handleCER)
	h.server.HandleFunc(server.Command{Interface: 0, Code: 280, Request: true}, h.handleDWR)
	h.server.HandleFunc(server.Command{Interface: 0, Code: 282, Request: true}, h.handleDPR)
	// Authentication-Information-Request (Command Code 318, App-ID 16777251)
	h.server.HandleFunc(server.Command{Interface: 16777251, Code: 318, Request: true}, h.handleAIR)
}

func (h *S6aHSSSimulator) processMessages() {
	recvChan := h.server.Receive()
	for {
		select {
		case <-h.ctx.Done():
			return
		case msgCtx, ok := <-recvChan:
			if !ok {
				return
			}
			if err := h.handleApplicationMessage(msgCtx); err != nil {
				h.logger.Errorw("HSS failed to handle application message", "error", err)
				h.errorCount.Add(1)
			}
		}
	}
}

func (h *S6aHSSSimulator) handleCER(msg *server.Message, conn server.Conn) {
	cer := &base.CapabilitiesExchangeRequest{}
	fullMsg := append(msg.Header, msg.Body...)
	if err := cer.Unmarshal(fullMsg); err != nil {
		h.logger.Errorw("HSS failed to unmarshal CER", "error", err)
		return
	}

	cea := base.NewCapabilitiesExchangeAnswer()
	cea.Header.HopByHopID = cer.Header.HopByHopID
	cea.Header.EndToEndID = cer.Header.EndToEndID
	cea.ResultCode = models_base.Unsigned32(2001)
	cea.OriginHost = models_base.DiameterIdentity(h.originHost)
	cea.OriginRealm = models_base.DiameterIdentity(h.originRealm)
	cea.ProductName = models_base.UTF8String("S6a-HSS-Simulator")
	cea.VendorId = models_base.Unsigned32(10415)

	if localAddr, ok := conn.LocalAddr().(*net.TCPAddr); ok {
		cea.HostIpAddress = []models_base.Address{models_base.Address(localAddr.IP)}
	}
	cea.AuthApplicationId = []models_base.Unsigned32{16777251}

	out, err := cea.Marshal()
	if err != nil {
		h.logger.Errorw("HSS failed to marshal CEA", "error", err)
		return
	}
	if _, err := conn.Write(out); err != nil {
		h.logger.Errorw("HSS failed to send CEA", "error", err)
	}
}

func (h *S6aHSSSimulator) handleDWR(msg *server.Message, conn server.Conn) {
	fullMsg := append(msg.Header, msg.Body...)
	dwa, err := createDWA(fullMsg, h.originHost, h.originRealm)
	if err != nil {
		h.logger.Errorw("HSS failed to create DWA", "error", err)
		return
	}
	if _, err := conn.Write(dwa); err != nil {
		h.logger.Errorw("HSS failed to send DWA", "error", err)
	}
}

func (h *S6aHSSSimulator) handleDPR(msg *server.Message, conn server.Conn) {
	h.logger.Infow("HSS handling DPR")
}

// handleAIR builds an AIA by copying the AIR bytes and clearing the R flag.
// This keeps HbH/E2E identical, which is what the DRA's pending table and
// the MME simulator's waiter both key on.
func (h *S6aHSSSimulator) handleAIR(msg *server.Message, conn server.Conn) {
	h.requestsReceived.Add(1)

	fullMsg := append(msg.Header, msg.Body...)
	aia := make([]byte, len(fullMsg))
	copy(aia, fullMsg)
	aia[4] &= ^byte(0x80) // clear R bit -> answer

	h.logger.Infow("HSS sending AIA",
		"h2h", extractHopByHopID(aia),
		"e2e", extractEndToEndID(aia))

	if _, err := conn.Write(aia); err != nil {
		h.logger.Errorw("HSS failed to send AIA", "error", err)
		h.errorCount.Add(1)
	} else {
		h.responsesSent.Add(1)
	}
}

func (h *S6aHSSSimulator) handleApplicationMessage(msgCtx *server.MessageContext) error {
	msg := msgCtx.Message
	if len(msg) < 20 {
		return fmt.Errorf("message too short")
	}
	appID := binary.BigEndian.Uint32(msg[8:12])
	// Base protocol and already-handled AIR are processed by registered handlers.
	if appID == 0 || appID == 16777251 {
		return nil
	}
	return nil
}

func (h *S6aHSSSimulator) GetStats() S6aHSSStats {
	return S6aHSSStats{
		RequestsReceived: h.requestsReceived.Load(),
		ResponsesSent:    h.responsesSent.Load(),
		Errors:           h.errorCount.Load(),
	}
}
