package main

// SMM2 online-init for DataStore (0x73) and Utility (0x6E) — public build.
//
// A full server-side DataStore/Utility implementation for SMM2's Course World is not part
// of this tree. The handlers below answer every method with a valid empty response so the
// game proceeds through online bring-up and the live log reveals which methods it calls,
// to be implemented over time. No response bodies are shipped in the public build.

import (
	"embed"
	"fmt"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

// capturedResponses maps (protocolID<<16 | methodID) -> a response body. Empty in the
// public build; handlers fall through to the empty-response path when a key is absent.
// capturedFS is empty in the public build (no measured bodies shipped).
var capturedFS embed.FS

var capturedResponses = map[uint32][]byte{}

func replayKey(proto uint16, method uint32) uint32 { return uint32(proto)<<16 | method }

// loadCapturedResponses is a no-op in the public build (no bodies shipped).
func loadCapturedResponses() {}

// replayHandler answers a protocol's methods. 0x73.8 = DataStore::NotFound (0x80690004),
// handled gracefully by SMM2; any other method returns an empty-list success (count = 0).
func replayHandler(proto uint16) nex.RMCHandler {
	return func(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
		s := conn.Settings
		if proto == 0x73 && req.Method == 8 {
			return nex.NewRMCError(s, proto, req.CallID, 0x80690004)
		}
		if body, ok := capturedResponses[replayKey(proto, req.Method)]; ok {
			return nex.NewRMCSuccess(s, proto, req.Method, req.CallID, body)
		}
		out := nex.NewStreamOut(s)
		out.U32(0)
		fmt.Printf("[SMM2] 0x%02x.%d callID=%d -> empty-list fallback (stub)\n", proto, req.Method, req.CallID)
		return nex.NewRMCSuccess(s, proto, req.Method, req.CallID, out.Bytes())
	}
}

// setupSMM2InitReplay registers DataStore (0x73) + Utility (0x6E) stub handlers.
func setupSMM2InitReplay(endpoint *nex.Endpoint) {
	loadCapturedResponses()
	endpoint.Register(0x73, smm2DataStoreHandler())
	endpoint.Register(0x6E, replayHandler(0x6E))
	fmt.Println("[SMM2] DataStore(0x73) + Utility(0x6E) stub registered (no bodies in public build)")
}
