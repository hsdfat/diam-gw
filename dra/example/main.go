// Package main shows how to start a DRA node.
//
//	go run ./dra/example
package main

import (
	"log"
	"net"

	"github.com/hsdfat/diam-gw/dra"
)

func main() {
	node := dra.NewDRANode(dra.Config{
		NodeName:    "dra-01",
		ListenAddr:  "0.0.0.0:3868",
		OriginHost:  "dra01.epc.mnc001.mcc001.3gppnetwork.org",
		OriginRealm: "epc.mnc001.mcc001.3gppnetwork.org",
		ProductName: "mini-dra",
		VendorID:    10415, // 3GPP
		HostIPs:     []net.IP{net.ParseIP("127.0.0.1")},
		// S6a/S6d + S13 — DRA just relays, so listing the apps peers will
		// negotiate is enough.
		SupportedApps: []uint32{16777251, 16777252},
	})
	log.Printf("starting %s", node.GetNodeName())
	if err := node.StartNode(); err != nil {
		log.Fatalf("dra: %v", err)
	}
}
