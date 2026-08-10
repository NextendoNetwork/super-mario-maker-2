package main

import (
	"fmt"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

// piaKeepalivePacketType is the undocumented PRUDP packet type 8 that SMM2's Pia
// 5.19 sends as a periodic keepalive on the secure stream (empty payload, NeedsAck,
// ~every 3s). The base PRUDP switch only knows types 0-4, so with no handler the
// keepalive went un-ACKed and SMM2 treated the connection as dead → soft-lock when
// entering "En ligne". ACKing it unblocks the online menu. Harmless for MK8 (never
// sends type 8).
const piaKeepalivePacketType uint8 = 8

func setupSMM2Type8Keepalive(endpoint *nex.Endpoint) {
	endpoint.RegisterCustomPacketHandler(piaKeepalivePacketType, func(c *nex.Connection, p *nex.Packet) {
		if p.HasFlag(nex.FlagNeedACK) {
			c.SendAck(p)
		}
		fmt.Printf("[SMM2 Type8] Pia keepalive seqID=%d -> ACK\n", p.PacketID)
	})
}
