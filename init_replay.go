package main

// SMM2 online-init for DataStore (0x73) and Utility (0x6E).
//
// A full server-side DataStore implementation for SMM2's Course World is not part of
// this tree. Instead we replay byte-exact captured response bodies from measured/
// (protocol.method -> body, filename resp_0x<proto-hex>_m<method-decimal>[_tag].bin)
// for the structural methods SMM2 needs to get through online init, and synthesize
// everything else. loadCapturedResponses() also carves a UserInfo template out of the
// get_users(48) capture (see carveUserTemplates in smm2_profile.go) so smm2GetUsers can
// patch a fresh identity into it instead of ever replaying a captured player's own data.
//
// IMPORTANT: this file has reverted to a "public build" no-op stub THREE TIMES during
// this project's debugging sessions, silently undoing loadCapturedResponses() and the
// captureResponses S->C logging wrapper below. If you're reading this after another
// unexplained regression (get_users(48) suddenly "no template", or measured_live.txt
// suddenly missing all S->C lines again), check THIS file first before assuming a code
// bug — something in the workflow (git checkout of a stale ref, a "public build"
// generation step, a merge) keeps restoring an older version of it specifically.

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

// capturedFS is only used by loadM132Types (smm2_objects.go) to read embedded
// per-type relation descriptors if this binary was built with a //go:embed measured
// directive; empty otherwise (no embed directive here), which is fine.
var capturedFS embed.FS

// capturedResponses maps (protocolID<<16 | methodID) -> a captured response body,
// loaded from measured/ at startup.
var capturedResponses = map[uint32][]byte{}

// replayDir is where captured response bodies live. Override with SMM2_REPLAY_DIR if
// they're not next to the binary.
var replayDir = envOr("SMM2_REPLAY_DIR", "measured")

func replayKey(proto uint16, method uint32) uint32 { return uint32(proto)<<16 | method }

var replayFileRe = regexp.MustCompile(`^resp_0x([0-9a-fA-F]+)_m([0-9]+)(?:_.*)?\.bin$`)
var liveLogRe = regexp.MustCompile(`S->C\s+0x73\.48\s+call=\d+\s+len=\d+\s+([0-9a-fA-F]+)`)

// loadM48FromMeasuredLive is a fallback source for the get_users(48) template: if
// measured/resp_0x73_m48*.bin is missing, scrape the last "S->C 0x73.48" line out of
// a previous SMM2_CAPTURE log (measured_live.txt). Only used when the real capture
// file isn't present.
func loadM48FromMeasuredLive() []byte {
	for _, candidate := range []string{"measured_live.txt", filepath.Join(replayDir, "measured_live.txt")} {
		b, err := os.ReadFile(candidate)
		if err != nil {
			continue
		}
		lines := strings.Split(string(b), "\n")
		for i := len(lines) - 1; i >= 0; i-- {
			line := strings.TrimSpace(lines[i])
			if !strings.Contains(line, "S->C 0x73.48") {
				continue
			}
			m := liveLogRe.FindStringSubmatch(line)
			if len(m) < 2 {
				continue
			}
			hexStr := strings.ReplaceAll(m[1], " ", "")
			if len(hexStr) < 2 {
				continue
			}
			body, err := hexToBytes(hexStr)
			if err != nil {
				fmt.Printf("[SMM2 Replay] cannot decode measured_live.txt 0x73.48 response: %v\n", err)
				continue
			}
			return body
		}
	}
	return nil
}

func hexToBytes(s string) ([]byte, error) {
	if len(s)%2 != 0 {
		return nil, fmt.Errorf("odd hex length: %d", len(s))
	}
	b := make([]byte, len(s)/2)
	for i := 0; i < len(s); i += 2 {
		v, err := strconv.ParseUint(s[i:i+2], 16, 8)
		if err != nil {
			return nil, err
		}
		b[i/2] = byte(v)
	}
	return b, nil
}

// loadCapturedResponses scans replayDir for resp_0x<proto>_m<method>[_tag].bin files
// and loads each into capturedResponses. It also carves a UserInfo/Result template out
// of the get_users(48) capture (falling back to measured_live.txt if the .bin is
// missing) so smm2GetUsers has a byte-exact shell to patch identities into.
func loadCapturedResponses() {
	loaded := 0

	if st, err := os.Stat(replayDir); err == nil && st.IsDir() {
		entries, err := os.ReadDir(replayDir)
		if err == nil {
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				m := replayFileRe.FindStringSubmatch(e.Name())
				if m == nil {
					continue
				}

				proto64, errProto := strconv.ParseUint(m[1], 16, 16)
				method64, errMethod := strconv.ParseUint(m[2], 10, 32)
				if errProto != nil || errMethod != nil {
					continue
				}

				path := filepath.Join(replayDir, e.Name())
				body, err := os.ReadFile(path)
				if err != nil {
					fmt.Printf("[SMM2 Replay] cannot read %s: %v\n", path, err)
					continue
				}

				capturedResponses[replayKey(uint16(proto64), uint32(method64))] = body
				loaded++
			}
		} else {
			fmt.Printf("[SMM2 Replay] cannot read directory %q: %v\n", replayDir, err)
		}
	} else {
		fmt.Printf("[SMM2 Replay] directory %q not found or not a directory\n", replayDir)
	}

	if _, ok := capturedResponses[replayKey(0x73, 48)]; !ok {
		if body := loadM48FromMeasuredLive(); len(body) > 0 {
			capturedResponses[replayKey(0x73, 48)] = body
			loaded++
			fmt.Printf("[SMM2 Replay] loaded fallback get_users(48) template from measured_live.txt\n")
		}
	}

	if body, ok := capturedResponses[replayKey(0x73, 48)]; ok {
		carveUserTemplates(body)
		if len(resultSuccessTpl) != 4 {
			resultSuccessTpl = []byte{0, 0, 0, 0}
		}
	}

	fmt.Printf("[SMM2 Replay] loaded %d response bodies from %q\n", loaded, replayDir)
	if _, ok := capturedResponses[replayKey(0x73, 48)]; !ok {
		fmt.Printf("[SMM2 Replay] missing required capture: resp_0x73_m48*.bin\n")
	}
	if _, ok := capturedResponses[replayKey(0x73, 49)]; !ok {
		fmt.Printf("[SMM2 Replay] missing recommended capture: resp_0x73_m49*.bin\n")
	}
}

// replayHandler answers a protocol's methods from capturedResponses. 0x73.8 =
// DataStore::NotFound (0x80690004), handled gracefully by SMM2; any other
// uncaptured method returns an empty-list success (count = 0).
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

// captureResponses wraps a handler so its S->C response is also appended to the
// SMM2_CAPTURE log (smm2_capture.go), same granularity as the C->S requests already
// captured from endpoint.OnRMC in main.go. Without this we only ever see half the
// conversation (what the client asked), never what we actually answered.
func captureResponses(h nex.RMCHandler) nex.RMCHandler {
	return func(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
		resp := h(conn, req)
		if resp != nil {
			captureRMC("S->C", resp)
		}
		return resp
	}
}

// setupSMM2InitReplay registers DataStore (0x73) + Utility (0x6E) handlers, loading
// captured response bodies from measured/ first.
func setupSMM2InitReplay(endpoint *nex.Endpoint) {
	loadCapturedResponses()
	endpoint.Register(0x73, captureResponses(smm2DataStoreHandler()))
	endpoint.Register(0x6E, captureResponses(replayHandler(0x6E)))
	fmt.Printf("[SMM2] DataStore(0x73) + Utility(0x6E) registered (%d captured response(s) loaded)\n", len(capturedResponses))
}
