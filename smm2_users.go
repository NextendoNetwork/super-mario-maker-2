package main

// Persistent maker-profile registry.
//
// RegisterUser(47) is where the client hands the server the identity it just built —
// username, Mii bytes, region/country, a device id (per kinnay/NintendoClients'
// RegisterUserParam: username string, UnknownStruct1, qBuffer, region_id u8,
// country_code string, pseudo_device_id string). Until now the request was only
// logged and thrown away, so nothing distinguished "PID has a maker profile" from
// "PID has never registered" across reconnects — get_users(48)/sync_user_profile(49)
// always fell back to empty, and the client re-prompted Mii/name creation every time.
//
// This stores the registration per PID (in memory + a JSON file, so it also survives
// server restarts). Nothing in the Courses/Leaderboard hub paths touches this file —
// it only feeds get_users(48)/sync_user_profile(49)'s own-profile fallback.

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

// registeredProfile is one PID's RegisterUser(47) payload, kept for future
// GetUsers(48)/SyncUserProfile(49) answers.
type registeredProfile struct {
	PID            uint64 `json:"pid"`
	Username       string `json:"username"`
	MiiDataHex     string `json:"mii_data_hex"` // qBuffer from RegisterUserParam, hex
	Unk1Hex        string `json:"unk1_hex"`     // UnknownStruct1 body, opaque, hex
	RegionID       uint8  `json:"region_id"`
	CountryCode    string `json:"country_code"`
	PseudoDeviceID string `json:"pseudo_device_id"`
	RegisteredAt   int64  `json:"registered_at"`
}

type profileRegistry struct {
	mu    sync.Mutex
	byPID map[uint64]*registeredProfile
	path  string
}

var profiles = &profileRegistry{byPID: map[uint64]*registeredProfile{}}

// init loads any previously-registered profiles from disk at startup, no main.go
// wiring needed.
func init() {
	profiles.load()
}

func (p *profileRegistry) load() {
	_ = os.MkdirAll(storageDir, 0o755)
	p.path = filepath.Join(storageDir, "profiles.json")
	if b, err := os.ReadFile(p.path); err == nil {
		var list []*registeredProfile
		if json.Unmarshal(b, &list) == nil {
			for _, r := range list {
				p.byPID[r.PID] = r
			}
		}
	}
	fmt.Printf("[SMM2 Profiles] %d perfil(es) maker registrado(s) cargado(s) desde disco\n", len(p.byPID))
}

func (p *profileRegistry) persistLocked() {
	list := make([]*registeredProfile, 0, len(p.byPID))
	for _, r := range p.byPID {
		list = append(list, r)
	}
	if b, err := json.MarshalIndent(list, "", "  "); err == nil {
		tmp := p.path + ".tmp"
		if os.WriteFile(tmp, b, 0o644) == nil {
			_ = os.Rename(tmp, p.path)
		}
	}
}

// register stores (or overwrites) the profile for pid.
func (p *profileRegistry) register(pid uint64, username string, miiData, unk1 []byte, regionID uint8, countryCode, pseudoDeviceID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.byPID[pid] = &registeredProfile{
		PID: pid, Username: username,
		MiiDataHex: hex.EncodeToString(miiData), Unk1Hex: hex.EncodeToString(unk1),
		RegionID: regionID, CountryCode: countryCode, PseudoDeviceID: pseudoDeviceID,
		RegisteredAt: time.Now().Unix(),
	}
	p.persistLocked()
}

func (p *profileRegistry) get(pid uint64) *registeredProfile {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.byPID[pid]
}

func (r *registeredProfile) miiBytes() []byte {
	if r == nil {
		return nil
	}
	b, _ := hex.DecodeString(r.MiiDataHex)
	return b
}

func (r *registeredProfile) unk1Bytes() []byte {
	if r == nil {
		return nil
	}
	b, _ := hex.DecodeString(r.Unk1Hex)
	return b
}

// parseRegisterUserParam decodes RegisterUser(47)'s request body per
// kinnay/NintendoClients' documented RegisterUserParam:
//
//	String username
//	UnknownStruct1 unk1     (opaque Structure: [version u8][length u32][body])
//	qBuffer miiData
//	Uint8 regionID
//	String countryCode
//	String pseudoDeviceID
func parseRegisterUserParam(s *nex.Settings, body []byte) (username string, unk1, miiData []byte, regionID uint8, countryCode, pseudoDeviceID string, ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	in := nex.NewStreamIn(body, s)
	_ = in.U8() // RegisterUserParam struct version
	p := in.Substream()

	username = p.String()

	_ = p.U8() // UnknownStruct1 version
	unk1Sub := p.Substream()
	unk1 = unk1Sub.ReadAll()

	miiData = p.QBuffer()
	regionID = p.U8()
	countryCode = p.String()
	pseudoDeviceID = p.String()
	ok = true
	return
}

// smm2RegisterUser handles RegisterUser(47): parse + persist the profile for the
// connected PID, then ack with an empty body (this method returns nothing per the
// documented spec — no bool, no shape at all).
func smm2RegisterUser(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	username, unk1, miiData, regionID, countryCode, pseudoDeviceID, ok := parseRegisterUserParam(s, req.Body)
	if ok {
		profiles.register(conn.PID, username, miiData, unk1, regionID, countryCode, pseudoDeviceID)
		fmt.Printf("[SMM2 Profiles] RegisterUser(47) pid=%d username=%q mii=%dB country=%q -> guardado\n",
			conn.PID, username, len(miiData), countryCode)
	} else {
		fmt.Printf("[SMM2 Profiles] RegisterUser(47) pid=%d: no se pudo parsear (%dB) -> no guardado\n", conn.PID, len(req.Body))
	}
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, nil)
}

// --- consuming the registry from get_users(48) / sync_user_profile(49) ------------
//
// Step 1 only stored what RegisterUser sent, without changing what get_users/
// sync_user_profile answered — so even a PID that had already registered still got
// "no profile" on every re-entry, and the client re-prompted Mii creation every single
// time. These two builders close that loop: a registered PID gets a real (if minimal)
// answer instead of the always-empty fallback.
//
// Deliberately compact, NOT the richer stat-map/badges shape we tried earlier and
// confirmed (via measured_live.txt, multiple sessions) triggers a client-side
// communication error for non-boot resultOptions. This mirrors the one shape we know
// is safe: [pid][code][name][zero tail], sized like the one real capture we have.

// syntheticUserInfoFromProfile builds a minimal UserInfo for get_users(48) from a
// stored registration.
//
// CHANGED: now embeds the REAL captured UnknownStruct1 + Mii bytes instead of a flat
// zero tail. Evidence for this: even with profiles.json already holding a registered
// profile, the client STILL re-prompted Mii/name creation on the very first get_users
// of a fresh session (measured_live.txt, clean repro) — meaning "pid found" alone isn't
// what the client checks; it's specifically whether the Mii data it reads back looks
// valid. We had the real Mii bytes captured from RegisterUser(47) the whole time but
// never wrote them into this response.
func syntheticUserInfoFromProfile(s *nex.Settings, pid uint64, r *registeredProfile) []byte {
	name := pseudoOr(pid)
	var unk1, mii []byte
	if r != nil {
		if r.Username != "" {
			name = r.Username
		}
		unk1 = r.unk1Bytes()
		mii = r.miiBytes()
	}
	out := nex.NewStreamOut(s)
	out.PID(pid)
	out.String(makerCode(pid))
	out.String(name)
	out.Write(frameStruct(s, 0, unk1)) // UnknownStruct1: real bytes if we captured them
	out.QBuffer(mii)                   // Mii: the real bytes from RegisterUser, not zeros
	return frameStruct(s, 0, out.Bytes())
}

// syntheticSyncProfileResult builds a minimal SyncUserProfileResult for
// sync_user_profile(49) from a stored registration. Real shape per kinnay's wiki:
// PID, username, UnknownStruct1, qBuffer, Uint8, country_code, Uint8, Bool, Bool.
func syntheticSyncProfileResult(s *nex.Settings, pid uint64, r *registeredProfile) []byte {
	name := pseudoOr(pid)
	country := ""
	if r != nil {
		if r.Username != "" {
			name = r.Username
		}
		country = r.CountryCode
	}
	out := nex.NewStreamOut(s)
	out.PID(pid)
	out.String(name)
	out.Write(frameStruct(s, 0, r.unk1Bytes())) // UnknownStruct1: real if we have it
	out.QBuffer(r.miiBytes())                   // real Mii bytes if registered
	out.U8(0)                        // unknown
	out.String(country)
	out.U8(0)       // unknown
	out.Bool(false) // unknown
	out.Bool(false) // unknown
	return frameStruct(s, 0, out.Bytes())
}

// smm2GetUsersFromProfiles answers get_users(48) using the registered-profile store.
//
// Only for resultOption==0xE284 do we serve the real registered profile (with real Mii
// bytes, since that's what actually lets the client accept "you have a Maker" — tested
// and confirmed via a clean repro: 0xE284 with real Mii got far enough to render Course
// World for a frame). Serving that SAME data for 0x2284 was tried and made things
// worse — a graceful bounce-back to Mii creation became a hard communication error.
// That rules out "0x2284 just needs the same data" — it needs a genuinely different,
// larger structure (more resultOption bits = more populated fields = more wire bytes),
// which we still don't have the documented layout for. Back to serving 0 users for
// anything other than 0xE284, which is at least not a hard error.
func smm2GetUsersFromProfiles(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	pids := parseGetUsersPIDs(conn, req)
	option := parseGetUsersOption(conn, req)

	var users [][]byte
	if option == 0xE284 {
		for _, pid := range pids {
			if r := profiles.get(pid); r != nil {
				users = append(users, syntheticUserInfoFromProfile(s, pid, r))
			}
		}
	}

	out := nex.NewStreamOut(s)
	out.U32(uint32(len(users)))
	for _, u := range users {
		out.Write(u)
	}
	out.U32(uint32(len(users)))
	for range users {
		out.Write([]byte{0, 0, 0, 0})
	}
	fmt.Printf("[SMM2 DataStore] get_users(48) opt=%#x -> %d/%d perfil(es) registrado(s) encontrado(s)\n", option, len(users), len(pids))
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}
