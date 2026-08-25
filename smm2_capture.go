package main

// Optional wire-measured of the RMC requests the client sends, so the emulator's SMM2 upload
// behavior can be diffed against the real-Switch measured. Enabled by SMM2_CAPTURE=<file>;
// each request is logged as "C->S 0x<proto>.<method> call=<id> len=<N> <hex>", the same
// (protocol, method, body) granularity the Switch measured decodes to.

import (
	"fmt"
	"os"
	"sync"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

var (
	captureFile      = os.Getenv("SMM2_CAPTURE")
	captureMu        sync.Mutex
	captureFH        *os.File
	captureOpenTried bool
)

// captureRMC appends one request frame to the measured file (no-op unless SMM2_CAPTURE set).
func captureRMC(tag string, req *nex.RMCMessage) {
	if captureFile == "" {
		return
	}
	captureMu.Lock()
	defer captureMu.Unlock()
	if captureFH == nil {
		if captureOpenTried {
			return
		}
		captureOpenTried = true
		f, err := os.Create(captureFile)
		if err != nil {
			fmt.Printf("[SMM2 Capture] cannot open %s: %v\n", captureFile, err)
			return
		}
		captureFH = f
		fmt.Printf("[SMM2 Capture] recording client requests -> %s\n", captureFile)
	}
	fmt.Fprintf(captureFH, "%s 0x%02x.%d call=%d len=%d %x\n", tag, req.Protocol, req.Method, req.CallID, len(req.Body), req.Body)
	_ = captureFH.Sync()
}
