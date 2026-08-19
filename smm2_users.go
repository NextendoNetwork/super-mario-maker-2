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

// --- User-facing stat sub-structs ---------------------------------------------
//
// These mirror the wire-format stat maps the SMM2 client reads from UserInfo and
// CourseInfo. We track them as plain semantic fields in profiles.json / catalog
// so a future review can wire them to the wire with a real Nintendo capture
// (maker_stats' byte keys are NOT documented by Nintendo — neither
// nintendoclients nor kinnay — and we discovered empirically that key 0 renders
// as "likes received" in the SMM2 client, NOT "courses uploaded").
//
// Sub-structs are kept small and flat on purpose: they're the on-disk shape
// the user (and `cat profiles.json | jq`) sees, and they round-trip through
// encoding/json without surprises.

// playStats is what the user has done PLAYING (mirrors CourseInfo.play_stats keys).
// Per NintendoClients PlayStatsKeys: PLAYS=0, CLEARS=1, ATTEMPTS=2, DEATHS=3.
//
// Not directly fed by anything yet: SMM2's play events are reported by the client
// via undocumented methods, and the Nintendo central service aggregates them. On
// this private server we have no reporting path, so these stay at 0 unless a
// future handler starts writing them. The fields are PRESENT so the wire encoder
// can emit them as soon as data shows up.
type playStats struct {
	Plays    uint32 `json:"plays"`
	Clears   uint32 `json:"clears"`
	Attempts uint32 `json:"attempts"`
	Deaths   uint32 `json:"deaths"`
}

// makerStats is what the user has done as a MAKER — derived from activity on
// their uploaded courses. All fields are written by:
//   - Maker.Uploaded          : reconcileFromCatalog (catalog ground truth)
//   - Maker.{Plays,Clears,Attempts,Deaths}Received : event handlers, when wired
//   - Maker.LikesReceived     : rate_object (15) handler — slot 0 = like
//   - Maker.HeartsReceived    : rate_object (15) handler — slot 1 = heart (guess)
//   - Maker.BoosReceived      : rate_object (15) handler — slot 2 = boo (guess)
//   - Maker.MakerPoints       : derived (Nintendo ranking points; not yet)
type makerStats struct {
	Uploaded         uint32 `json:"uploaded"`
	PlaysReceived    uint32 `json:"plays_received"`
	ClearsReceived   uint32 `json:"clears_received"`
	AttemptsReceived uint32 `json:"attempts_received"`
	DeathsReceived   uint32 `json:"deaths_received"`
	LikesReceived    uint32 `json:"likes_received"`
	HeartsReceived   uint32 `json:"hearts_received"`
	BoosReceived     uint32 `json:"boos_received"`
	MakerPoints      uint32 `json:"maker_points"`
}

// multiplayerStats mirrors MultiplayerStatsKeys.
// 0=MULTIPLAYER_SCORE, 2=VERSUS_PLAYS, 3=VERSUS_WINS, 10=COOP_PLAYS, 11=COOP_WINS.
// Not fed by anything yet; placeholders so a future multiplayer handler can bump
// them without a JSON-shape change.
type multiplayerStats struct {
	Score       uint32 `json:"score"`
	VersusPlays uint32 `json:"versus_plays"`
	VersusWins  uint32 `json:"versus_wins"`
	CoopPlays   uint32 `json:"coop_plays"`
	CoopWins    uint32 `json:"coop_wins"`
}

// badgeInfo mirrors BadgeInfo{u16 unk1, u8 unk2}. Exact field meanings are
// undocumented; unk1 is likely a badge "type" (100-playmaker, 1000-first-clear,
// etc) and unk2 a level within that type. Empty for now — no handler awards badges.
type badgeInfo struct {
	Unk1 uint16 `json:"unk1"`
	Unk2 uint8  `json:"unk2"`
}

// registeredProfile is one PID's full profile: RegisterUser(47) payload + every
// per-user stat the SMM2 UserInfo struct can carry (per nintendoclients
// datastore_smm2 UserInfo). Kept in memory + a JSON file so it survives restarts.
//
// Anything that gets aggregated per-user is here, regardless of whether we have
// a wire encoding for it yet. New stat sources only need to add a writer.
type registeredProfile struct {
	// --- Identity (from RegisterUser 47) ---
	PID            uint64 `json:"pid"`
	Username       string `json:"username"`
	MiiDataHex     string `json:"mii_data_hex"` // qBuffer from RegisterUserParam, hex
	Unk1Hex        string `json:"unk1_hex"`     // UnknownStruct1 body, opaque, hex
	RegionID       uint8  `json:"region_id"`
	CountryCode    string `json:"country_code"`
	PseudoDeviceID string `json:"pseudo_device_id"`
	RegisteredAt   int64  `json:"registered_at"`

	// --- Player activity (the user playing) ---
	Play playStats `json:"play_stats"`

	// --- Maker activity (received on their uploaded courses) ---
	Maker makerStats `json:"maker_stats"`

	// --- Multiplayer ---
	Multiplayer multiplayerStats `json:"multiplayer_stats"`

	// --- Endless challenge best score per difficulty.
	// Key: 0=Easy, 1=Normal, 2=Expert, 3=Super Expert (per CourseDifficulty). Empty
	// for now — no handler reads/reports endless-mode high scores yet.
	EndlessHighScores map[uint8]uint32 `json:"endless_high_scores"`

	// --- Badges (per-badge, see badgeInfo). Empty for now. ---
	Badges []badgeInfo `json:"badges"`

	// --- Three unknown stat maps (Map<u8, u32>) per nintendoclients UserInfo
	// (unk7/unk8/unk9). No data today; present so a future handler has a place
	// to write without re-shaping the JSON.
	Unk7 map[uint8]uint32 `json:"unk7"`
	Unk8 map[uint8]uint32 `json:"unk8"`
	Unk9 map[uint8]uint32 `json:"unk9"`

	// --- UserInfo revision>=1 extras (would need UserInfo struct version>=1) ---
	Unk10 bool  `json:"unk10"`
	Unk11 int64 `json:"unk11"`
	Unk12 bool  `json:"unk12"`

	// --- UserInfo revision>=3 extras (would need UserInfo struct version>=3) ---
	Unk14 string           `json:"unk14"`
	Unk15 map[uint8]uint32 `json:"unk15"`
	Unk16 bool             `json:"unk16"`

	// --- Per-profile upload list (denormalized for cheap reads in 74).
	// UploadedCount is len(UploadedIDs); kept redundant for quick display without
	// an extra len() and to make the value trivially greppable in profiles.json.
	// Source of truth is the catalog (c.byID): on every server start we call
	// reconcileFromCatalog so an out-of-band edit / older server / manual move
	// doesn't drift the count. Also kept in sync with Maker.Uploaded.
	UploadedCount int      `json:"uploaded_count"`
	UploadedIDs   []uint64 `json:"uploaded_ids"`

	// --- Per-user relation sets, source of truth for m=75/m=76/m=80/m=81.
	// These were the missing piece behind an empty "courses I played" /
	// "courses I positive-rated" / "courses I first-cleared" /
	// "courses with my best time" tabs in the maker profile UI: the old code
	// only tracked COUNTS (Play.Clears, Course.LikeCount, etc.) but the
	// search-courses-by-X methods need the actual list of course_ids.
	//
	// PlayedCourses: set of data_ids this PID has touched at least once
	// (touched via touch_object(22), prepare_get_object(25) on a non-owned
	// course, or post_play_result(96)). Stored as a map[uint64]bool so
	// duplicates collapse and JSON serialises the keys naturally.
	//
	// RatedCourses: per (data_id) → rating slot (0=like, 1=heart, 2=boo).
	// A user can re-rate the same course, which overwrites the slot — the
	// counters in the catalog keep every transition but the per-user set
	// only stores the most recent (which is what search_courses_positive_rated_by
	// needs: a course appears here if the LATEST rating is positive, i.e.
	// slot 0 or 1). m=15 (rate_object) is the writer.
	PlayedCourses  map[uint64]bool  `json:"played_courses,omitempty"`
	RatedCourses   map[uint64]uint8 `json:"rated_courses,omitempty"`
	ClearedCourses map[uint64]bool  `json:"cleared_courses,omitempty"`

	// --- Per-user first-clears: courses this PID was the FIRST to clear
	// (set by setCourseTimes in storage.go on the first replay upload).
	// Currently redundant with playedCourses + CourseTimeStats, but kept
	// explicit for m=80 (search_courses_first_clear) and to make a future
	// "earliest clears" leaderboard trivial.
	FirstCleared map[uint64]bool `json:"first_cleared,omitempty"`
}

type profileRegistry struct {
	mu    sync.Mutex
	byPID map[uint64]*registeredProfile
	path  string
}

// profiles is the in-memory + on-disk registry of maker profiles indexed by PID.
// Auto-populated by RegisterUser(47); consumed by get_users(48) and the search
// methods on courses (53/54/55/75/76/80/81). Persists to <storageDir>/profiles.json.
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
	} else if os.IsNotExist(err) {
		// First run (or fresh STORAGE_DIR): materialize an empty profiles.json right away.
		p.persistLocked()
	}
	fmt.Printf("[SMM2 Profiles] %d perfil(es) maker registrado(s) cargado(s) desde disco\n", len(p.byPID))
}

func (p *profileRegistry) persistLocked() {
	list := make([]*registeredProfile, 0, len(p.byPID))
	for _, r := range p.byPID {
		list = append(list, r)
	}
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		fmt.Printf("[SMM2 Profiles] cannot marshal profiles.json: %v\n", err)
		return
	}
	tmp := p.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		fmt.Printf("[SMM2 Profiles] cannot write %s: %v\n", tmp, err)
		return
	}
	if err := os.Rename(tmp, p.path); err != nil {
		fmt.Printf("[SMM2 Profiles] cannot rename %s -> %s: %v\n", tmp, p.path, err)
	}
}

// register stores (or overwrites) the profile for pid.
func (p *profileRegistry) register(pid uint64, username string, miiData, unk1 []byte, regionID uint8, countryCode, pseudoDeviceID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.byPID[pid]
	if r == nil {
		r = &registeredProfile{PID: pid}
		p.byPID[pid] = r
	}
	r.Username = username
	r.MiiDataHex = hex.EncodeToString(miiData)
	r.Unk1Hex = hex.EncodeToString(unk1)
	r.RegionID = regionID
	r.CountryCode = countryCode
	r.PseudoDeviceID = pseudoDeviceID
	r.RegisteredAt = time.Now().Unix()
	p.persistLocked()
}

func (p *profileRegistry) get(pid uint64) *registeredProfile {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.byPID[pid]
}

// recordUpload adds dataID to pid's UploadedIDs (and bumps UploadedCount +
// Maker.Uploaded) if pid is already registered AND dataID isn't already on the
// list — idempotent, so a retry of the same upload (or the markReadyForPID sweep
// that fires on every CompletePostObjectsCourse call) doesn't double-count.
// Returns true if the list actually grew.
//
// Profiles that haven't been registered yet (no RegisterUser(47) received) are NOT
// materialised by this call — we don't have a username/Mii for them, so creating an
// entry here would just be a useless ghost profile. Their courses are still findable
// via the catalog directly (SearchCoursesPostedBy(74), get_courses(70) filtered by PID).
func (p *profileRegistry) recordUpload(pid uint64, dataID uint64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.byPID[pid]
	if r == nil {
		return false
	}
	for _, id := range r.UploadedIDs {
		if id == dataID {
			return false
		}
	}
	r.UploadedIDs = append(r.UploadedIDs, dataID)
	r.UploadedCount = len(r.UploadedIDs)
	r.Maker.Uploaded = uint32(len(r.UploadedIDs))
	p.persistLocked()
	return true
}

// recordPlay marks pid as having played dataID at least once. Called from
// touch_object(22), prepare_get_object(25) (non-self only), and
// post_play_result(96) — three paths cover the same notion of "the player
// entered this course's play context" but each can fire independently
// depending on which RMC the client happens to send (Ryujinx-Nextendo
// currently only fires m=25; the full SMM2 client fires all three).
// Idempotent: replaying the same course doesn't re-record. Persists the
// profile so a server restart keeps the relation.
func (p *profileRegistry) recordPlay(pid uint64, dataID uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.byPID[pid]
	if r == nil {
		return // ghost play — don't materialise a profile just for this
	}
	if r.PlayedCourses == nil {
		r.PlayedCourses = map[uint64]bool{}
	}
	if r.PlayedCourses[dataID] {
		return // already recorded
	}
	r.PlayedCourses[dataID] = true
	p.persistLocked()
}

// recordRate marks pid as having rated dataID with the given slot
// (0=like, 1=heart, 2=boo). A re-rate overwrites the slot — the catalog's
// LikeCount/HeartCount/BoosCount keeps the cumulative history, but the
// per-user set only stores the latest so that
// search_courses_positive_rated_by(75) reflects the CURRENT state of the
// user's vote (a user who re-rated from heart to boo should NOT appear in
// the positive-rated list anymore).
func (p *profileRegistry) recordRate(pid, dataID uint64, slot uint8) {
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.byPID[pid]
	if r == nil {
		return
	}
	if r.RatedCourses == nil {
		r.RatedCourses = map[uint64]uint8{}
	}
	r.RatedCourses[dataID] = slot
	p.persistLocked()
}

// recordFirstClear marks pid as the first clearer of dataID. Called from
// setCourseTimes (storage.go) when the very first replay upload lands
// for a course. Idempotent on repeat calls for the same course (the
// upstream guard is in setCourseTimes itself, but we double-check here
// so a stray second call doesn't double-write).
func (p *profileRegistry) recordFirstClear(pid, dataID uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.byPID[pid]
	if r == nil {
		return
	}
	if r.FirstCleared == nil {
		r.FirstCleared = map[uint64]bool{}
	}
	if r.ClearedCourses[dataID] {
		return
	}
	r.FirstCleared[dataID] = true
	p.persistLocked()
}

// recordClear is separate from FirstCleared: method 54 asks for every player who cleared a course, while method 80 asks only for first clears.
func (p *profileRegistry) recordClear(pid, dataID uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.byPID[pid]
	if r == nil {
		return
	}
	if r.ClearedCourses == nil {
		r.ClearedCourses = map[uint64]bool{}
	}
	if r.ClearedCourses[dataID] {
		return
	}
	r.ClearedCourses[dataID] = true
	p.persistLocked()
}

// coursesPlayed returns the data_ids the PID has played, oldest-first
// (insertion order). Returns nil if the PID is unknown.
func (p *profileRegistry) coursesPlayed(pid uint64) []uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.byPID[pid]
	if r == nil {
		return nil
	}
	out := make([]uint64, 0, len(r.PlayedCourses))
	for id := range r.PlayedCourses {
		out = append(out, id)
	}
	return out
}

// coursesPositiveRated returns the data_ids the PID has rated POSITIVELY
// (slot 0=like or 1=heart, NOT 2=boo). Powers m=75
// (search_courses_positive_rated_by).
func (p *profileRegistry) coursesPositiveRated(pid uint64) []uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.byPID[pid]
	if r == nil {
		return nil
	}
	out := make([]uint64, 0, len(r.RatedCourses))
	for id, slot := range r.RatedCourses {
		if slot == 0 || slot == 1 {
			out = append(out, id)
		}
	}
	return out
}

// coursesFirstCleared returns the data_ids the PID was the FIRST to clear.
// Powers m=80 (search_courses_first_clear).
func (p *profileRegistry) coursesFirstCleared(pid uint64) []uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.byPID[pid]
	if r == nil {
		return nil
	}
	out := make([]uint64, 0, len(r.FirstCleared))
	for id := range r.FirstCleared {
		out = append(out, id)
	}
	return out
}

// playersWhoPlayedCourse returns the PIDs that have played dataID, in
// stable iteration order. Source: each profile's PlayedCourses map. Returns
// nil if nobody has played it. Powers m=53 (search_users_played_course).
//
// SCAN, not a cached index. For 2-3 profiles (today's reality) this is
// instant; if the profile count grows large we'd add a parallel
// courseID→set<PID> map alongside the per-user relations. Keeping the
// source of truth in ONE place (the per-user map) is worth the iteration
// cost at this scale — no two-index desync to debug.
func (p *profileRegistry) playersWhoPlayedCourse(dataID uint64) []uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []uint64
	for pid, r := range p.byPID {
		if r.PlayedCourses[dataID] {
			out = append(out, pid)
		}
	}
	return out
}

// clearersOfCourse returns the PIDs that have FIRST-cleared dataID, in
// stable iteration order. Source: each profile's FirstCleared map. Returns
// nil if nobody has first-cleared it. Powers m=54 (search_users_cleared_course).
func (p *profileRegistry) clearersOfCourse(dataID uint64) []uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []uint64
	for pid, r := range p.byPID {
		if r.ClearedCourses[dataID] {
			out = append(out, pid)
		}
	}
	return out
}

// positiveRatersOfCourse returns the PIDs whose LATEST rating on dataID is
// positive (slot 0=like or 1=heart, NOT 2=boo), in stable iteration order.
// Source: each profile's RatedCourses map. Returns nil if nobody has
// rated it positively. Powers m=55 (search_users_positive_rated_course).
//
// "Latest" is key: a user who re-rated from heart (slot 1) to boo (slot 2)
// should NOT appear here. The slot overwrite is what recordRate does.
func (p *profileRegistry) positiveRatersOfCourse(dataID uint64) []uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []uint64
	for pid, r := range p.byPID {
		if slot, ok := r.RatedCourses[dataID]; ok && (slot == 0 || slot == 1) {
			out = append(out, pid)
		}
	}
	return out
}

// recordRating applies a rate_object(15) event to pid's maker stats.
//
// slot mapping (SMM2's DataStoreRatingTarget.slot):
//
//	0 = like  → Maker.LikesReceived++
//	1 = heart → Maker.HeartsReceived++  (tentative; slot-to-type mapping isn't
//	2 = boo   → Maker.BoosReceived++    documented anywhere we can verify)
//
// ratingValue of 0 typically means "cleared/reset a previous rating", so we DON'T
// count those — the previous rating stays in the aggregate. Only strictly-positive
// values bump the counter. No-op for unregistered PIDs (they have no maker profile
// to credit; the rate still goes through to the per-course counter in
// courses.recordRating, which is the source of truth for the course's own stats).
func (p *profileRegistry) recordRating(pid uint64, slot uint8, ratingValue int64) {
	if ratingValue <= 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.byPID[pid]
	if r == nil {
		return
	}
	switch slot {
	case 0:
		r.Maker.LikesReceived++
	case 1:
		r.Maker.HeartsReceived++
	case 2:
		r.Maker.BoosReceived++
	}
	p.persistLocked()
}

// applyMakerReceived adds deltas to a profile's "received" stats. Called by
// future play/clear/death event handlers. No-op for unregistered PIDs.
func (p *profileRegistry) applyMakerReceived(pid uint64, plays, clears, attempts, deaths uint32) {
	if plays == 0 && clears == 0 && attempts == 0 && deaths == 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.byPID[pid]
	if r == nil {
		return
	}
	r.Maker.PlaysReceived += plays
	r.Maker.ClearsReceived += clears
	r.Maker.AttemptsReceived += attempts
	r.Maker.DeathsReceived += deaths
	p.persistLocked()
}

// applyPlayStats adds deltas to a profile's own play_stats (the user playing).
// No-op for unregistered PIDs.
func (p *profileRegistry) applyPlayStats(pid uint64, plays, clears, attempts, deaths uint32) {
	if plays == 0 && clears == 0 && attempts == 0 && deaths == 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.byPID[pid]
	if r == nil {
		return
	}
	r.Play.Plays += plays
	r.Play.Clears += clears
	r.Play.Attempts += attempts
	r.Play.Deaths += deaths
	p.persistLocked()
}

// applyMultiplayer adds deltas to a profile's multiplayer_stats. No-op for
// unregistered PIDs.
func (p *profileRegistry) applyMultiplayer(pid uint64, score, versusPlays, versusWins, coopPlays, coopWins uint32) {
	if score == 0 && versusPlays == 0 && versusWins == 0 && coopPlays == 0 && coopWins == 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.byPID[pid]
	if r == nil {
		return
	}
	r.Multiplayer.Score += score
	r.Multiplayer.VersusPlays += versusPlays
	r.Multiplayer.VersusWins += versusWins
	r.Multiplayer.CoopPlays += coopPlays
	r.Multiplayer.CoopWins += coopWins
	p.persistLocked()
}

// setEndlessHighScore records a new best for a difficulty. Only writes if `score`
// exceeds the previous value (it's a HIGH score, not a counter). Pass difficulty
// per CourseDifficulty: 0=Easy, 1=Normal, 2=Expert, 3=Super Expert.
//
// No-op for unregistered PIDs. Allocates the map on first use.
func (p *profileRegistry) setEndlessHighScore(pid uint64, difficulty uint8, score uint32) {
	if score == 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.byPID[pid]
	if r == nil {
		return
	}
	if r.EndlessHighScores == nil {
		r.EndlessHighScores = map[uint8]uint32{}
	}
	if cur, ok := r.EndlessHighScores[difficulty]; !ok || score > cur {
		r.EndlessHighScores[difficulty] = score
		p.persistLocked()
	}
}

// addBadge appends a badge. No dedup: SMM2 might legitimately award the same
// type+level multiple times for repeat achievements. No-op for unregistered PIDs.
func (p *profileRegistry) addBadge(pid uint64, unk1 uint16, unk2 uint8) {
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.byPID[pid]
	if r == nil {
		return
	}
	r.Badges = append(r.Badges, badgeInfo{Unk1: unk1, Unk2: unk2})
	p.persistLocked()
}

// reconcileFromCatalog rebuilds every profile's UploadedIDs / UploadedCount from the
// catalog ground truth (map of dataID -> ownerPID). Called once at server start, AFTER
// the catalog has loaded. Also seeds an entry for any PID that owns courses but never
// called RegisterUser(47) — without this, a PID that uploaded before registering would
// not show up in profiles.json at all, even though the courses themselves are
// persistent. The seed entry has empty Username/Mii so it doesn't pretend to be a
// real registered profile; SearchCoursesPostedBy(74) still works for it.
func (p *profileRegistry) reconcileFromCatalog(catalog map[uint64]*courseMeta) {
	p.mu.Lock()
	defer p.mu.Unlock()
	changed := false
	// 1) Rebuild per-profile lists from the catalog.
	byPID := map[uint64][]uint64{}
	for id, m := range catalog {
		byPID[m.OwnerPID] = append(byPID[m.OwnerPID], id)
	}
	for pid, ids := range byPID {
		r := p.byPID[pid]
		if r == nil {
			// Seed a profile for an owner we know from the catalog but who never registered.
			r = &registeredProfile{PID: pid, UploadedCount: 0}
			p.byPID[pid] = r
			changed = true
		}
		// Cheap equality check before overwriting — avoids re-marshalling profiles.json
		// on every restart just because the order changed.
		same := len(r.UploadedIDs) == len(ids)
		if same {
			// Sort both for unordered compare.
			a := append([]uint64(nil), r.UploadedIDs...)
			b := append([]uint64(nil), ids...)
			sortUint64(a)
			sortUint64(b)
			for i := range a {
				if a[i] != b[i] {
					same = false
					break
				}
			}
		}
		if !same {
			r.UploadedIDs = append([]uint64(nil), ids...)
			r.UploadedCount = len(r.UploadedIDs)
			r.Maker.Uploaded = uint32(len(r.UploadedIDs))
			changed = true
		} else if r.Maker.Uploaded != uint32(len(r.UploadedIDs)) {
			// Counters drifted (e.g. an older profiles.json from before the
			// Maker.Uploaded field existed). Re-sync without touching the list.
			r.Maker.Uploaded = uint32(len(r.UploadedIDs))
			changed = true
		}
	}
	if changed {
		p.persistLocked()
	}
}

// sortUint64 is a tiny insertion sort used by reconcileFromCatalog. Avoids pulling in
// the "sort" stdlib package for one helper.
func sortUint64(a []uint64) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j-1] > a[j]; j-- {
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
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
	_, ok = parseParamStream(s, body, func(p *nex.StreamIn) bool {
		username = p.String()

		_ = p.U8() // UnknownStruct1 version
		unk1Sub := p.Substream()
		unk1 = unk1Sub.ReadAll()

		miiData = p.QBuffer()
		regionID = p.U8()
		countryCode = p.String()
		pseudoDeviceID = p.String()
		return true
	})
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
// time. These two builders close that loop: a registered PID gets a real answer
// instead of the always-empty fallback.
//
// FULL REWRITE: earlier versions of syntheticUserInfoFromProfile stopped after the Mii
// bytes — country, region, last_active, the stat maps, badges, everything past that
// was simply MISSING from the wire, not just empty. That's not a safe "minimal" shape,
// it's a truncated one: any resultOption bit expecting those fields would desync.
// Per the real UserInfo layout (nintendoclients.readthedocs.io reference for
// nex.datastore_smm2, which the wiki itself cuts off before showing), the version-0
// structure is: pid, code, name, unk1(UnknownStruct1), unk2(Mii bytes), country,
// region, last_active(DateTime), unk3/4/5(bool), play_stats/maker_stats/
// endless_challenge_high_scores/multiplayer_stats/unk7(Map<u8,u32>),
// badges(List<BadgeInfo>), unk8/unk9(Map<u8,u32>) — no revision>=1/2/3 extras, since
// our working compact template already used struct version=0 for 0xE284. Now build
// ALL of those fields (empty maps/list where we have no real data, but PRESENT and
// correctly typed) instead of stopping partway through.

// writeU8U32Map writes a NEX Map<Uint8, Uint32> — used for UserInfo's several stat
// maps (play_stats, maker_stats, endless_challenge_high_scores, multiplayer_stats,
// and the two still-unknown unk7/unk8/unk9 maps), and for CourseInfo's
// play_stats/ratings/unk4/comment_stats. Pass nil for an empty map (writes U32(0)).
func writeU8U32Map(out *nex.StreamOut, m map[uint8]uint32) {
	if m == nil {
		out.U32(0)
		return
	}
	out.U32(uint32(len(m)))
	for k, v := range m {
		out.U8(k)
		out.U32(v)
	}
}

// buildPlayStatsMap converts a playStats sub-struct into a wire Map<u8, u32>
// using the documented PlayStatsKeys: PLAYS=0, CLEARS=1, ATTEMPTS=2, DEATHS=3.
// Returns nil if every value is zero so the wire encoder writes a length-0 map
// (clients that read it see "no data" instead of "all zeros").
func buildPlayStatsMap(p playStats) map[uint8]uint32 {
	if p.Plays == 0 && p.Clears == 0 && p.Attempts == 0 && p.Deaths == 0 {
		return nil
	}
	m := map[uint8]uint32{0: p.Plays, 1: p.Clears, 2: p.Attempts, 3: p.Deaths}
	return m
}

// buildMultiplayerStatsMap converts a multiplayerStats into a wire Map<u8, u32>
// using the documented MultiplayerStatsKeys:
// 0=MULTIPLAYER_SCORE, 2=VERSUS_PLAYS, 3=VERSUS_WINS, 10=COOP_PLAYS, 11=COOP_WINS.
// Returns nil if every value is zero.
func buildMultiplayerStatsMap(m multiplayerStats) map[uint8]uint32 {
	if m.Score == 0 && m.VersusPlays == 0 && m.VersusWins == 0 && m.CoopPlays == 0 && m.CoopWins == 0 {
		return nil
	}
	out := map[uint8]uint32{
		0:  m.Score,
		2:  m.VersusPlays,
		3:  m.VersusWins,
		10: m.CoopPlays,
		11: m.CoopWins,
	}
	return out
}

// buildMakerStatsMap converts a makerStats into a wire Map<u8, u32>.
//
// WARNING: maker_stats' byte-key mapping is NOT documented by Nintendo (neither
// nintendoclients nor kinnay). Empirically, key 0 renders as "likes received" in
// the SMM2 client — an earlier "fix" that put UploadedCount in key 0 instead
// showed it as a likes count, confirming the slot. The byte keys for "courses
// uploaded" and the other maker fields are still unknown. Until a real Nintendo
// capture tells us the mapping, we emit ONLY what we've verified by experiment:
// key 0 = likes received. Clients that don't have a known key ignore the entry,
// and 74 (search_courses_posted_by) still serves the actual list of uploads.
//
// Other maker_stats fields (Uploaded, PlaysReceived, ClearsReceived, ...) are
// tracked in profiles.json and ready to be wired to the correct keys as soon as
// the mapping is known.
func buildMakerStatsMap(m makerStats) map[uint8]uint32 {
	if m.LikesReceived == 0 {
		return nil
	}
	return map[uint8]uint32{0: m.LikesReceived}
}

// userInfoFields is the resolved shape for one PID's UserInfo payload, after
// applying defaults from pseudoOr() and overrides from a registered profile
// (if any). The "empty" defaults match what we want a stranger-PID to look
// like: name = pseudoOr(pid), no unk1/mii, empty country, region 0, zero stats.
// Splits "what to put in" (resolve) from "how to put it on the wire" (write).
type userInfoFields struct {
	name    string
	unk1    []byte
	mii     []byte
	country string
	region  uint8
	play    playStats
	maker   makerStats
	multi   multiplayerStats
	endless map[uint8]uint32
	badges  []badgeInfo
	unk7    map[uint8]uint32
	unk8    map[uint8]uint32
	unk9    map[uint8]uint32
}

// resolveUserInfoFields returns the filled-in field set for one PID from a
// registered profile. If r is nil, returns the empty defaults. Each field is
// a direct copy of the profile's stored value — no transformation. Adding
// a new profile field is a 2-line change here + one wire field in writeUserInfo.
func resolveUserInfoFields(pid uint64, r *registeredProfile) userInfoFields {
	f := userInfoFields{
		name: pseudoOr(pid),
		unk1: make([]byte, 8), // 8 zero bytes: below this the client aborts avatar creation
	}
	if r == nil {
		return f
	}
	if r.Username != "" {
		f.name = r.Username
	}
	if len(r.unk1Bytes()) > 0 {
		f.unk1 = r.unk1Bytes()
	}
	f.mii = r.miiBytes()
	f.country = r.CountryCode
	f.region = r.RegionID
	f.play = r.Play
	f.maker = r.Maker
	f.multi = r.Multiplayer
	f.endless = r.EndlessHighScores
	f.badges = r.Badges
	f.unk7 = r.Unk7
	f.unk8 = r.Unk8
	f.unk9 = r.Unk9
	return f
}

// writeUserInfo emits the full version-0 UserInfo wire format from resolved
// fields. 18 fields, grouped:
//
//	header (7)  — PID, code, name, unk1, mii, country, region
//	last_active (1)
//	3 unknown bools (3)
//	5 stat maps  (5) — play, maker, endless, multi, unk7
//	badge list   (1)
//	3 trailing unknown maps (3) — unk8, unk9 + frame
//
// Order, types, and framing match the kinnay/nintendoclients UserInfo shape
// exactly. Helpers (writeU8U32Map, writeBoolFalse3, writeDateTimeNow,
// writeBadgeInfoList) keep this to one wire call per logical field.
func writeUserInfo(s *nex.Settings, pid uint64, f userInfoFields) []byte {
	out := nex.NewStreamOut(s)
	out.PID(pid)
	out.String(makerCode(pid))
	out.String(f.name)
	out.Write(frameStruct(s, 0, f.unk1)) // unk1: UnknownStruct1 (pose/hat/shirt/pants), real if captured
	out.QBuffer(f.mii)                    // unk2: Mii bytes, real if registered
	out.String(f.country)
	out.U8(f.region)
	writeDateTimeNow(out)                  // last_active
	writeBoolFalse3(out)                   // unk3 / unk4 / unk5
	writeU8U32Map(out, buildPlayStatsMap(f.play))          // play_stats (PlayStatsKeys)
	writeU8U32Map(out, buildMakerStatsMap(f.maker))        // maker_stats (only verified keys)
	writeU8U32Map(out, f.endless)                          // endless_challenge_high_scores
	writeU8U32Map(out, buildMultiplayerStatsMap(f.multi))  // multiplayer_stats (MultiplayerStatsKeys)
	writeU8U32Map(out, f.unk7)                             // unk7
	writeBadgeInfoList(out, f.badges)                      // badges: List<BadgeInfo>
	writeU8U32Map(out, f.unk8)                             // unk8
	writeU8U32Map(out, f.unk9)                             // unk9
	return frameStruct(s, 0, out.Bytes())
}

// syntheticUserInfoFromProfile builds a COMPLETE version-0 UserInfo for get_users(48)
// from a stored registration — every documented field present, populated from the
// profile's tracked stats where we know the wire encoding, and explicitly empty
// (not MISSING) where we don't. Resolves fields from the profile, then writes
// the wire envelope.
func syntheticUserInfoFromProfile(s *nex.Settings, pid uint64, r *registeredProfile) []byte {
	return writeUserInfo(s, pid, resolveUserInfoFields(pid, r))
}

// writeBadgeInfoList serialises a List<BadgeInfo> per the documented kinnay/
// nintendoclients BadgeInfo shape (u16 unk1, u8 unk2). Pass nil for an empty list.
func writeBadgeInfoList(out *nex.StreamOut, list []badgeInfo) {
	if len(list) == 0 {
		out.U32(0)
		return
	}
	out.U32(uint32(len(list)))
	for _, b := range list {
		out.U16(b.Unk1)
		out.U8(b.Unk2)
	}
}

// syntheticSyncProfileResult builds a minimal SyncUserProfileResult for
// sync_user_profile(49) from a stored registration. Real shape per kinnay's wiki:
// PID, username, UnknownStruct1, qBuffer, Uint8, country_code, Uint8, Bool, Bool.
func syntheticSyncProfileResult(s *nex.Settings, pid uint64, r *registeredProfile) []byte {
	name := pseudoOr(pid)
	country := ""
	unk1 := make([]byte, 8) // 8 zero bytes: below this the client aborts avatar creation
	if r != nil {
		if r.Username != "" {
			name = r.Username
		}
		country = r.CountryCode
		if len(r.unk1Bytes()) > 0 {
			unk1 = r.unk1Bytes()
		}
	}
	out := nex.NewStreamOut(s)
	out.PID(pid)
	out.String(name)
	out.Write(frameStruct(s, 0, unk1)) // UnknownStruct1: real if we have it
	out.QBuffer(r.miiBytes())          // real Mii bytes if registered
	out.U8(0)                                   // unknown
	out.String(country)
	out.U8(0)       // unknown
	out.Bool(false) // unknown
	out.Bool(false) // unknown
	return frameStruct(s, 0, out.Bytes())
}

// lookupProfileForGetUsers resolves a single requested pid to (profile, lookupPID).
// When exactly one pid is requested and the lookup misses, also tries connPID —
// the server stored the registration under the hashed anonymous identity
// resolveUser derived for it (per main.go), which is often different from the
// raw pid the CLIENT asks with (e.g. a test username like "51966"). Returning
// (nil, pid) on a complete miss tells the caller to emit the empty-UserInfo
// placeholder.
func lookupProfileForGetUsers(pid uint64, pids []uint64, connPID uint64) (*registeredProfile, uint64) {
	r := profiles.get(pid)
	if r != nil {
		return r, pid
	}
	if len(pids) == 1 && pid != connPID {
		if r2 := profiles.get(connPID); r2 != nil {
			return r2, connPID
		}
	}
	return nil, pid
}

// writeUserInfoListWithResultsResponse emits the list<UserInfo> + list<u32 result>
// envelope used by get_users(48). users is the pre-built UserInfo bytes (one
// per entry). result is currently 0 for every entry — the server doesn't track
// per-user "result" codes yet (confirmed via measured_live.txt: the result
// bytes are always 00 00 00 00 in real traffic).
func writeUserInfoListWithResultsResponse(s *nex.Settings, users [][]byte) []byte {
	out := nex.NewStreamOut(s)
	out.U32(uint32(len(users)))
	for _, u := range users {
		out.Write(u)
	}
	out.U32(uint32(len(users)))
	for range users {
		out.Write([]byte{0, 0, 0, 0})
	}
	return out.Bytes()
}

// smm2GetUsersFromProfiles answers get_users(48) using the registered-profile store.
//
// The requested pid doesn't always match conn.PID: for a username outside the
// Nextendo account range (e.g. a raw small test number like "51966"), the CLIENT
// queries itself using that raw number, while the SERVER registers/stores the
// profile under conn.PID (the hashed anonymous identity resolveUser derived for it,
// per main.go). Confirmed via measured_live.txt: RegisterUser succeeded and the
// profile was in profiles.json, yet every single get_users afterward still answered
// 0 users, because profiles.get(requestedPID) never matched what profiles.register
// stored under conn.PID. When exactly one pid is requested and it doesn't match, also
// try conn.PID — that covers the overwhelmingly common "check my own profile" case
// without guessing at anyone else's identity.
func smm2GetUsersFromProfiles(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	pids := parseGetUsersPIDs(conn, req)
	option := parseGetUsersOption(conn, req)

	users := make([][]byte, 0, len(pids))
	for _, pid := range pids {
		r, lookupPID := lookupProfileForGetUsers(pid, pids, conn.PID)
		if r != nil {
			users = append(users, syntheticUserInfoFromProfile(s, lookupPID, r))
		} else {
			// PID not registered: emit a complete-but-empty UserInfo placeholder so the
			// response count matches the request count. Without positional alignment the
			// client (e.g. when asking for the full author list of New/Hot Courses, ~114
			// PIDs in one call) ends up in an invalid session state and triggers a phantom
			// m=61 ack loop. syntheticUserInfoFromProfile(s, pid, nil) already produces
			// a version-0 UserInfo with every documented field present and empty, which
			// is the smallest wire-compatible answer for an unknown PID.
			users = append(users, syntheticUserInfoFromProfile(s, pid, nil))
		}
	}

	fmt.Printf("[SMM2 DataStore] get_users(48) opt=%#x -> %d/%d perfil(es) registrado(s) encontrado(s)\n", option, len(users), len(pids))
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, writeUserInfoListWithResultsResponse(s, users))
}
