package main

// Dynamic profile — never serve a measured player's identity.
//
// The measured DataStore responses embed the REAL Nintendo player the measured was
// taken from: sync_user_profile(49) returns the name "a player", and get_users(48)
// returns 261 OTHER real users (names, Miis, maker codes). Replaying them leaked
// those identities and showed the wrong pseudo on every Nextendo player's profile.
//
// Instead we keep ONE measured struct as a byte-exact TEMPLATE and rewrite only the
// identity fields (pid + name + maker code) for the CONNECTED Nextendo account,
// resolved from nextendo-account /api/names. Everything else (Mii bytes, the version-3
// stat maps, badges) is kept verbatim so the structure stays valid and SMM2's online
// init still enters Course World — the fragile part the replay was chosen to avoid.

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

// --- pid -> Nextendo pseudo (via nextendo-account /api/names), cached ------------

var (
	pseudoMu    sync.Mutex
	pseudoCache = map[uint64]string{}
)

// resolvePseudo returns the Nextendo pseudo for a pid, or "" if unknown/unreachable.
func resolvePseudo(pid uint64) string {
	pseudoMu.Lock()
	if n, ok := pseudoCache[pid]; ok {
		pseudoMu.Unlock()
		return n
	}
	pseudoMu.Unlock()

	resp, err := gateClient.Get(fmt.Sprintf("%s/api/names?pids=%d", accountBaseURL, pid))
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var out struct {
		Names map[string]struct {
			Name string `json:"name"`
		} `json:"names"`
	}
	if json.NewDecoder(resp.Body).Decode(&out) != nil {
		return ""
	}
	name := out.Names[strconv.FormatUint(pid, 10)].Name
	if name != "" {
		pseudoMu.Lock()
		pseudoCache[pid] = name
		pseudoMu.Unlock()
	}
	return name
}

// pseudoOr returns the resolved pseudo, or a safe fallback derived from the pid.
// It is NEVER a measured name; online requires a Nextendo account, so a resolved
// pseudo is the normal path and the fallback only covers a transient lookup miss.
func pseudoOr(pid uint64) string {
	if n := resolvePseudo(pid); n != "" {
		return n
	}
	return fmt.Sprintf("Nextendo%d", pid%1000000)
}

// makerCode derives a stable 9-char SMM2-style code from a pid so we don't serve the
// measured player's real maker code. Uses Nintendo's confusable-free code alphabet.
func makerCode(pid uint64) string {
	const alpha = "0123456789BCDFGHJKLMNPQRSTVWXY" // 30 chars, no vowels/confusables
	x := (pid ^ 0x9e3779b97f4a7c15) * 0xff51afd7ed558ccd
	b := make([]byte, 9)
	for i := range b {
		b[i] = alpha[x%30]
		x = x/30 + 0x9e3779b97f4a7c15
	}
	return string(b)
}

// --- template patching ----------------------------------------------------------
//
// A NEX structure on the wire is [version u8][length u32][body]. We slice off the
// 5-byte header, rewrite the leading identity fields, keep the remaining bytes
// verbatim, and reframe with a corrected length.

// frameStruct wraps body with a [version u8][length u32] header.
func frameStruct(s *nex.Settings, version byte, body []byte) []byte {
	out := nex.NewStreamOut(s)
	out.U8(version)
	out.Buffer(body) // u32 length + body
	return out.Bytes()
}

// patchSyncProfile rewrites sync_user_profile(49) for the connected account.
// Body layout: [pid u64][username string][unk1..unk6 …].
func patchSyncProfile(s *nex.Settings, tmpl []byte, pid uint64, pseudo string) []byte {
	if len(tmpl) < 6 {
		return tmpl
	}
	version := tmpl[0]
	in := nex.NewStreamIn(tmpl[5:], s)
	_ = in.PID()      // old pid
	_ = in.String()   // old username
	rest := in.ReadAll()

	out := nex.NewStreamOut(s)
	out.PID(pid)
	out.String(pseudo)
	out.Write(rest)
	return frameStruct(s, version, out.Bytes())
}

// patchUserInfo rewrites one get_users UserInfo for a given pid/name.
// Body layout: [pid u64][code string][name string][unk1 … version-3 fields …].
func patchUserInfo(s *nex.Settings, tmpl []byte, pid uint64, name string) []byte {
	if len(tmpl) < 6 {
		return tmpl
	}
	version := tmpl[0]
	in := nex.NewStreamIn(tmpl[5:], s)
	_ = in.PID()      // old pid
	_ = in.String()   // old maker code
	_ = in.String()   // old name
	rest := in.ReadAll()

	out := nex.NewStreamOut(s)
	out.PID(pid)
	out.String(makerCode(pid))
	out.String(name)
	out.Write(rest)
	return frameStruct(s, version, out.Bytes())
}

// --- templates carved once from the measured get_users(48) blob ------------------

var (
	userInfoTemplate []byte // one byte-exact UserInfo (holly's), reused as a shell
	resultSuccessTpl []byte // one byte-exact success Result element
)

// carveUserTemplates extracts the first UserInfo and the first Result from the
// measured get_users(48) response (list<UserInfo> + list<result>). Each UserInfo is
// self-framed ([ver][len][body]) so we can slice it by its own length without a full
// decode; results are 4-byte codes.
func carveUserTemplates(m48 []byte) {
	if len(m48) < 4 {
		return
	}
	count := binary.LittleEndian.Uint32(m48[0:4])
	p := 4
	for i := uint32(0); i < count && p+5 <= len(m48); i++ {
		bodyLen := int(binary.LittleEndian.Uint32(m48[p+1 : p+5]))
		end := p + 5 + bodyLen
		if end > len(m48) {
			return
		}
		if i == 0 {
			userInfoTemplate = append([]byte(nil), m48[p:end]...)
		}
		p += 5 + bodyLen
	}
	// results: [u32 count][result u32]...
	if p+8 <= len(m48) {
		resultSuccessTpl = append([]byte(nil), m48[p+4:p+8]...)
	}
}

// smm2GetUsers answers get_users(48) with one freshly-identified UserInfo per
// requested pid — the connected account's own pseudo for its pid, resolved pseudos
// for any others — so no measured player is ever served.
func smm2GetUsers(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	pids := parseGetUsersPIDs(conn, req)

	out := nex.NewStreamOut(s)
	out.U32(uint32(len(pids))) // list<UserInfo>
	for _, pid := range pids {
		out.Write(patchUserInfo(s, userInfoTemplate, pid, pseudoOr(pid)))
	}
	out.U32(uint32(len(pids))) // list<result>
	for range pids {
		out.Write(resultSuccessTpl)
	}
	fmt.Printf("[SMM2 DataStore] get_users(48) -> %d profils dynamiques (pseudo Nextendo)\n", len(pids))
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}

// parseGetUsersPIDs reads GetUsersParam { pids: list<pid>, option: u32 } from the
// request. Falls back to the caller's own pid if the list is empty or implausible.
func parseGetUsersPIDs(conn *nex.Connection, req *nex.RMCMessage) []uint64 {
	in := nex.NewStreamIn(req.Body, conn.Settings)
	_ = in.U8()           // GetUsersParam struct version
	sub := in.Substream() // param body
	n := sub.U32()
	if n == 0 || n > 256 {
		return []uint64{conn.PID}
	}
	pids := make([]uint64, 0, n)
	for i := uint32(0); i < n; i++ {
		pids = append(pids, sub.PID())
	}
	if len(pids) == 0 {
		return []uint64{conn.PID}
	}
	return pids
}

// parseGetUsersOption re-reads just the trailing resultOption u32 from
// GetUsersParam, separate from parseGetUsersPIDs so nothing that already depends on
// that function's exact signature is touched.
func parseGetUsersOption(conn *nex.Connection, req *nex.RMCMessage) uint32 {
	in := nex.NewStreamIn(req.Body, conn.Settings)
	_ = in.U8()
	sub := in.Substream()
	n := sub.U32()
	for i := uint32(0); i < n; i++ {
		_ = sub.PID()
	}
	if sub.Remaining() >= 4 {
		return sub.U32()
	}
	return 0
}
