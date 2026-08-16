package main

// SMM2 level storage — the object-storage half of DataStore.
//
// A course is two layers: (1) NEX DataStore RMC metadata (title, tags, stats — the
// search/browse layer), and (2) the level BLOB itself, transferred over plain HTTP(S)
// to a URL the server hands back from prepare_post_object / prepare_get_object. Nintendo
// returns presigned AWS-S3 / CloudFront URLs; we run our OWN object store and hand back
// URLs into it, so uploaded courses live on the Nextendo VPS instead of Nintendo's S3.
//
// Flow:
//   prepare_post_object(24)  -> allocate data_id, return {data_id, url=/object/<id>} ; stash pending meta
//   console PUTs the blob     -> stored at <dataDir>/<id>.bin
//   complete_post_object(26) -> mark the course ready in the catalog (persisted)
//   prepare_get_object(25)   -> return {url=/object/<id>, size} for the requested data_id
//   console GETs the blob     -> served from <dataDir>/<id>.bin
//
// The exact upload handshake the console performs (PUT vs multipart POST, required
// headers) is not in the measured — no level was uploaded during it — so the object
// endpoint accepts PUT and POST and logs what actually arrives, to verify on the first
// real upload (measured > guess).

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	storagePort = envOrInt("STORAGE_PORT", 60078)
	// storageURL is the PUBLIC base the console dials for blob transfer. Locally it is
	// the game host itself; on the server set STORAGE_URL to the routed https origin.
	storageURL = envOr("STORAGE_URL", fmt.Sprintf("https://%s:%d", nextendoHost, storagePort))
	// storageHostPort is the scheme-less host:port the console POSTs uploads to (the
	// measured S3 responses carry a scheme-less host and the console prepends https://).
	storageHostPort = envOr("STORAGE_HOSTPORT", fmt.Sprintf("%s:%d", nextendoHost, storagePort))
	storageDir = envOr("STORAGE_DIR", "smm2_objects")
	// storageCAFile, if set, is returned as root_ca_cert so a real console trusts our
	// object server's TLS cert. Empty = rely on the console's default trust (emulator).
	storageCAFile = os.Getenv("STORAGE_CA_FILE")
)

// courseMeta is the catalog entry for one uploaded course.
type courseMeta struct {
	DataID      uint64   `json:"data_id"`
	OwnerPID    uint64   `json:"owner_pid"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	DataType    uint16   `json:"data_type"`
	MetaHex     string   `json:"meta_hex"` // course header (SMM2 meta_binary), hex
	Tags        []uint8  `json:"tags"`
	GameStyle   uint8    `json:"game_style"`
	CourseTheme uint8    `json:"course_theme"`
	Difficulty  uint8    `json:"difficulty"`
	Size        uint32   `json:"size"`
	Ready       bool     `json:"ready"` // set by complete_post_object
	Code        string   `json:"code"`  // SMM2 Course ID, e.g. "ABCD-1234-EFGH-5678"
	CreatedAt   int64    `json:"created_at"`

	// --- Per-course stats (the activity this course has received from players).
	//
	// PlayStats:  per CourseInfo.play_stats / PlayStatsKeys
	//             (PLAYS=0, CLEARS=1, ATTEMPTS=2, DEATHS=3). All four bumped by
	//             future play/clear/death event handlers (none wired today —
	//             SMM2 doesn't expose a documented "I played this course" call
	//             that we can hook).
	//
	// Ratings:    per CourseInfo.ratings — Map<u8, u32> indexed by slot.
	//             Slot 0 = like, 1 = heart, 2 = boo. The rating_value 0 means
	//             "clear previous rating" (no bump); > 0 bumps. Set by
	//             rate_object(15).
	//
	// CommentStats: per CourseInfo.comment_stats — Map<u8, u32> by slot.
	//              Slot-to-meaning undocumented. No event handler bumps these
	//              yet (no comment-post handler is implemented).
	PlayCount     uint32            `json:"play_count"`
	ClearCount    uint32            `json:"clear_count"`
	AttemptCount  uint32            `json:"attempt_count"`
	DeathCount    uint32            `json:"death_count"`
	LikeCount     uint32            `json:"like_count"`
	HeartCount    uint32            `json:"heart_count"`
	BoosCount     uint32            `json:"boos_count"`
	RatingInitial map[uint8]int64   `json:"rating_initial"` // initial_value per slot, set on first rate
	CommentCounts map[uint8]uint32  `json:"comment_counts"` // comment_stats per slot
}

type courseStore struct {
	mu      sync.Mutex
	nextID  uint64
	byID    map[uint64]*courseMeta
	catalog string // JSON path
	rootCA  []byte
}

var courses = &courseStore{byID: map[uint64]*courseMeta{}, nextID: 1000}

// loadStore restores the catalog + data_id counter from disk.
func (c *courseStore) load() {
	_ = os.MkdirAll(storageDir, 0o755)
	c.catalog = filepath.Join(storageDir, "catalog.json")
	if b, err := os.ReadFile(c.catalog); err == nil {
		var saved struct {
			NextID  uint64        `json:"next_id"`
			Courses []*courseMeta `json:"courses"`
		}
		if json.Unmarshal(b, &saved) == nil {
			if saved.NextID > c.nextID {
				c.nextID = saved.NextID
			}
			for _, m := range saved.Courses {
				c.byID[m.DataID] = m
			}
		}
	}
	if storageCAFile != "" {
		if b, err := os.ReadFile(storageCAFile); err == nil {
			c.rootCA = b
		}
	}
	fmt.Printf("[SMM2 Storage] catalogue chargé: %d cours, nextID=%d, dir=%s, url=%s\n",
		len(c.byID), c.nextID, storageDir, storageURL)
	c.migrateFlatLayout()
}

// persist writes the catalog back to disk (called under lock).
func (c *courseStore) persistLocked() {
	out := struct {
		NextID  uint64        `json:"next_id"`
		Courses []*courseMeta `json:"courses"`
	}{NextID: c.nextID}
	for _, m := range c.byID {
		out.Courses = append(out.Courses, m)
	}
	if b, err := json.MarshalIndent(out, "", "  "); err == nil {
		tmp := c.catalog + ".tmp"
		if os.WriteFile(tmp, b, 0o644) == nil {
			_ = os.Rename(tmp, c.catalog)
		}
	}
}

// alloc reserves a new data_id and stashes the pending metadata.
func (c *courseStore) alloc(ownerPID uint64, name string, dataType uint16, metaBin []byte, tags []uint8, size uint32) uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := c.nextID
	c.nextID++
	c.byID[id] = &courseMeta{
		DataID: id, OwnerPID: ownerPID, Name: name, DataType: dataType,
		MetaHex: fmt.Sprintf("%x", metaBin), Tags: tags, Size: size,
		CreatedAt: nowUnix(),
	}
	c.persistLocked()
	return id
}

// complete marks a course ready (or drops it if the upload was cancelled).
func (c *courseStore) complete(dataID uint64, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m := c.byID[dataID]
	if m == nil {
		return
	}
	if ok {
		m.Ready = true
	} else {
		delete(c.byID, dataID)
	}
	c.persistLocked()
}

func (c *courseStore) get(dataID uint64) *courseMeta {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.byID[dataID]
}

// listReady returns a snapshot of every Ready course owned by `ownerPID`, sorted
// newest first (by CreatedAt descending). Used by get_courses(70) to build the
// CourseInfo list the client shows post-upload and in the maker UI.
func (c *courseStore) listReady(ownerPID uint64) []*courseMeta {
	c.mu.Lock()
	ready := make([]*courseMeta, 0, len(c.byID))
	for _, m := range c.byID {
		if m.Ready && m.OwnerPID == ownerPID {
			ready = append(ready, m)
		}
	}
	c.mu.Unlock()
	// Sort newest first (insertion sort, fine for the catalog sizes we expect).
	for i := 1; i < len(ready); i++ {
		for j := i; j > 0 && ready[j-1].CreatedAt < ready[j].CreatedAt; j-- {
			ready[j-1], ready[j] = ready[j], ready[j-1]
		}
	}
	return ready
}

// listAllReady returns every Ready course from every owner, newest first — used
// by search_courses_latest(73) ("New Courses" in Course World), which is global
// browsing, not scoped to the requesting player like listReady/listByOwner are.
func (c *courseStore) listAllReady(limit int) []*courseMeta {
	c.mu.Lock()
	ready := make([]*courseMeta, 0, len(c.byID))
	for _, m := range c.byID {
		if m.Ready {
			ready = append(ready, m)
		}
	}
	c.mu.Unlock()
	for i := 1; i < len(ready); i++ {
		for j := i; j > 0 && ready[j-1].CreatedAt < ready[j].CreatedAt; j-- {
			ready[j-1], ready[j] = ready[j], ready[j-1]
		}
	}
	if limit > 0 && len(ready) > limit {
		ready = ready[:limit]
	}
	return ready
}

// listAllReadyPaginated returns a page of Ready courses (newest first), starting
// at byte offset `offset` with up to `limit` results, plus the total count of all
// Ready courses. Used by method 72 (third Course World tab) which sends explicit
// offset/limit in its 47-byte request body (body[8]=offset, body[12]=limit).
func (c *courseStore) listAllReadyPaginated(offset, limit int) ([]*courseMeta, int) {
	all := c.listAllReady(0) // 0 = no cap, get everything to sort
	total := len(all)
	if offset >= total {
		return []*courseMeta{}, total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return all[offset:end], total
}

// listAllReadyByHotness returns every Ready course from every owner, sorted by a
// simple "hotness" score (likes + hearts + plays, descending), newest as tiebreaker.
// Used by the undocumented method 84 ("Hot Courses" in Course World) — not in the
// official datastore_smm2 method list at all (same undocumented territory as 58/72/83,
// which populate the same Hub), but structurally we're reusing the same buildCourseInfo
// already confirmed working via search_courses_latest(73).
func (c *courseStore) listAllReadyByHotness(limit int) []*courseMeta {
	c.mu.Lock()
	ready := make([]*courseMeta, 0, len(c.byID))
	for _, m := range c.byID {
		if m.Ready {
			ready = append(ready, m)
		}
	}
	c.mu.Unlock()
	hotness := func(m *courseMeta) uint64 {
		return uint64(m.LikeCount) + uint64(m.HeartCount) + uint64(m.PlayCount)
	}
	for i := 1; i < len(ready); i++ {
		for j := i; j > 0; j-- {
			hj, hj1 := hotness(ready[j]), hotness(ready[j-1])
			if hj1 > hj || (hj1 == hj && ready[j-1].CreatedAt >= ready[j].CreatedAt) {
				break
			}
			ready[j-1], ready[j] = ready[j], ready[j-1]
		}
	}
	if limit > 0 && len(ready) > limit {
		ready = ready[:limit]
	}
	return ready
}

// listByOwnerReady returns every Ready course owned by ownerPID, sorted newest
// first. Used by SearchCoursesPostedBy(74) — "courses posted by player X", which is
// what the maker profile's "My courses" tab (or another player's profile page)
// calls. Pagination is the caller's responsibility (listByOwnerReadyPaginated below
// if a bounded scan is needed; today 74 just does the whole slice).
func (c *courseStore) listByOwnerReady(ownerPID uint64) []*courseMeta {
	c.mu.Lock()
	ready := make([]*courseMeta, 0, len(c.byID))
	for _, m := range c.byID {
		if m.Ready && m.OwnerPID == ownerPID {
			ready = append(ready, m)
		}
	}
	c.mu.Unlock()
	for i := 1; i < len(ready); i++ {
		for j := i; j > 0 && ready[j-1].CreatedAt < ready[j].CreatedAt; j-- {
			ready[j-1], ready[j] = ready[j], ready[j-1]
		}
	}
	return ready
}

// setCode assigns the shareable Course ID string ("XXXX-XXXX-XXXX-XXXX") that
// SMM2 displays post-upload. Called from CompletePostObjectsCourse(68) once the
// upload is confirmed; persisted so the code survives server restarts.
func (c *courseStore) setCode(dataID uint64, code string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m := c.byID[dataID]
	if m == nil {
		return
	}
	m.Code = code
	c.persistLocked()
}

// markReadyForPID flips Ready=true on every not-yet-Ready course owned by ownerPID
// and returns the list. Used by CompletePostObjectsCourse(68): the 66 alloc created
// the course but there is no separate "complete" call for it (no equivalent of
// complete_post_object(26) for the level-data path), so the 68 is the only place
// to mark it Ready before the client's get_courses(70) call asks for the catalog.
// All courses for this PID are flipped, not just the newest, so a stale entry from
// a previous failed upload also gets cleaned up.
//
// Also mirrors the upload into the per-profile registry (profiles.recordUpload) so
// UserInfo.maker_stats and SearchCoursesPostedBy(74) see fresh counts without a restart.
// recordUpload is idempotent (returns false on duplicates), so re-firing this method
// across retries doesn't double-count.
func (c *courseStore) markReadyForPID(ownerPID uint64) []*courseMeta {
	c.mu.Lock()
	defer c.mu.Unlock()
	var updated []*courseMeta
	for _, m := range c.byID {
		if m.OwnerPID == ownerPID && !m.Ready {
			m.Ready = true
			updated = append(updated, m)
		}
	}
	if len(updated) > 0 {
		c.persistLocked()
	}
	// Note: profiles.recordUpload is called from the caller (smm2CompletePostObjectsCourse
	// in smm2_objects.go) after we return, so the registry update is in one place with
	// the rest of the post-upload logging. Keeping it out of this method avoids a
	// mu-on-mu deadlock if a future change reorders calls between the two registries.
	return updated
}

// updateMeta writes the parsed name/description/tags/style/theme/difficulty from
// CompletePostObjectsCourse(68) into the catalog entry.
func (c *courseStore) updateMeta(dataID uint64, name, description string, tags []uint8, gameStyle, courseTheme, difficulty uint8) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m := c.byID[dataID]
	if m == nil {
		return
	}
	if name != "" {
		m.Name = name
	}
	m.Description = description
	m.Tags = tags
	m.GameStyle = gameStyle
	m.CourseTheme = courseTheme
	m.Difficulty = difficulty
	c.persistLocked()
}

// setSize records the byte size once a blob PUT completes.
func (c *courseStore) setSize(dataID uint64, size uint32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if m := c.byID[dataID]; m != nil {
		m.Size = size
		c.persistLocked()
	}
}

// recordRating applies a rate_object(15) event to a course. Bumps the per-slot
// counter and records the first-seen rating value as the slot's initial_value
// (kinnay's DataStoreRatingInfo.initial_value, the seed for future sum/count
// aggregates). ratingValue of 0 means "clear previous rating" — no counter bump.
//
// Slot mapping (SMM2's DataStoreRatingTarget.slot):
//   0 = like  → LikeCount++
//   1 = heart → HeartCount++
//   2 = boo   → BoosCount++
//
// Returns the Owner's PID (so the caller can also credit the per-profile
// MakerStats counters in profiles.recordRating), or 0 if the course is unknown.
func (c *courseStore) recordRating(dataID uint64, slot uint8, ratingValue int64) uint64 {
	if ratingValue <= 0 {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	m := c.byID[dataID]
	if m == nil {
		return 0
	}
	switch slot {
	case 0:
		m.LikeCount++
	case 1:
		m.HeartCount++
	case 2:
		m.BoosCount++
	}
	if m.RatingInitial == nil {
		m.RatingInitial = map[uint8]int64{}
	}
	if _, ok := m.RatingInitial[slot]; !ok {
		m.RatingInitial[slot] = ratingValue
	}
	c.persistLocked()
	return m.OwnerPID
}

// applyPlayed adds play/clear/attempt/death deltas to a course. Called by
// future play-event handlers. Unknown dataIDs are silently ignored (the
// course may have been deleted between event emission and handling).
func (c *courseStore) applyPlayed(dataID uint64, plays, clears, attempts, deaths uint32) {
	if plays == 0 && clears == 0 && attempts == 0 && deaths == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	m := c.byID[dataID]
	if m == nil {
		return
	}
	m.PlayCount += plays
	m.ClearCount += clears
	m.AttemptCount += attempts
	m.DeathCount += deaths
	c.persistLocked()
}

func blobPath(dataID uint64) string { return filepath.Join(courseDir(dataID), "level.bin") }

// --- Per-course file layout ------------------------------------------------------
//
// Everything about one course now lives under its own folder instead of being
// scattered flat across storageDir with ad-hoc prefixes ("obj_thumb1_<id>",
// "<id>.bin", ...) built independently in three different files. One source of
// truth here; smm2_objects.go (upload) and smm2_courses.go (CourseInfo/read) both
// call into it instead of re-deriving names themselves.
//
//   data/
//     catalog.json
//     profiles.json
//     courses/
//       <dataID>/
//         level.bin
//         thumb1.jpg   (one_screen_thumbnail, relType 1)
//         thumb2.jpg   (entire_thumbnail,     relType 2)
//         thumb3.jpg   (report thumbnail,      relType 3)
//         replay.bin   (clear-check replay,    relType 5)

// courseDir returns the per-course directory (not guaranteed to exist yet).
func courseDir(dataID uint64) string {
	return filepath.Join(storageDir, "courses", strconv.FormatUint(dataID, 10))
}

// relationBaseName maps a PrepareRelationUpload "type" to its filename within a
// course's directory. Returns "" for an unrecognized type.
func relationBaseName(relType uint32) string {
	switch relType {
	case 1:
		return "thumb1.jpg"
	case 2:
		return "thumb2.jpg"
	case 3:
		return "thumb3.jpg"
	case 5:
		return "replay.bin"
	default:
		return ""
	}
}

// relationPath returns the on-disk path for a course's relation blob, or "" if
// relType is unrecognized.
func relationPath(dataID uint64, relType uint32) string {
	name := relationBaseName(relType)
	if name == "" {
		return ""
	}
	return filepath.Join(courseDir(dataID), name)
}

// relationKey returns the "/relation/<key>" URL key for a course's relation blob —
// used both when building the upload URL (smm2PrepareRelationUpload) and when
// parsing an incoming request back into (dataID, relType) in relationHandler.
func relationKey(dataID uint64, relType uint32) string {
	return fmt.Sprintf("%d/%d", dataID, relType)
}

// parseRelationKey parses a "<dataID>/<relType>" key. ok is false if the key isn't
// in that shape (e.g. a stale key from before this layout, still in flight).
func parseRelationKey(key string) (dataID uint64, relType uint32, ok bool) {
	parts := strings.SplitN(key, "/", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	id, err1 := strconv.ParseUint(parts[0], 10, 64)
	typ, err2 := strconv.ParseUint(parts[1], 10, 32)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return id, uint32(typ), true
}

// contentTypeForPath derives a Content-Type from a stored file's extension — the
// thumbnails are real JPEGs (confirmed: raw ffd8ffe0... JFIF bytes in every capture),
// the replay isn't an image, so it keeps the generic type.
func contentTypeForPath(path string) string {
	if strings.HasSuffix(path, ".jpg") {
		return "image/jpeg"
	}
	return "application/octet-stream"
}

// migrateFlatLayout moves any pre-reorg files (top-level "<id>.bin" and
// "obj_thumb1_<id>" etc, one flat pile in storageDir) into the new courses/<id>/
// structure, for every course already in the catalog. Safe to run every startup:
// only touches an old path that still exists and only when the new path doesn't
// exist yet, so it's a no-op once everything's migrated. This is what lets the
// reorganization ship without re-uploading every course already on disk.
func (c *courseStore) migrateFlatLayout() {
	legacyRelNames := map[uint32]string{1: "obj_thumb1_", 2: "obj_thumb2_", 3: "obj_thumb3_", 5: "obj_replay_"}
	moved := 0
	for id := range c.byID {
		idStr := strconv.FormatUint(id, 10)
		newDir := courseDir(id)

		old := filepath.Join(storageDir, idStr+".bin")
		newP := filepath.Join(newDir, "level.bin")
		if fileExists(old) && !fileExists(newP) {
			_ = os.MkdirAll(newDir, 0o755)
			if os.Rename(old, newP) == nil {
				moved++
			}
		}
		for relType, prefix := range legacyRelNames {
			old := filepath.Join(storageDir, prefix+idStr)
			newP := relationPath(id, relType)
			if fileExists(old) && !fileExists(newP) {
				_ = os.MkdirAll(newDir, 0o755)
				if os.Rename(old, newP) == nil {
					moved++
				}
			}
		}
	}
	if moved > 0 {
		fmt.Printf("[SMM2 Storage] migration: %d fichier(s) déplacé(s) vers courses/<id>/\n", moved)
	}
	// After the catalog is loaded AND files are in their new layout, ask the profile
	// registry to rebuild its UploadedCount/UploadedIDs from the catalog ground truth.
	// Catches three classes of drift: a PID uploaded before calling RegisterUser(47),
	// a profile.json that pre-dated the per-course-dir refactor, and a profile that
	// went out of sync after a manual edit / older server / rolled-back commit.
	profiles.reconcileFromCatalog(c.byID)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// startStorageServer serves blob PUT/POST/GET over HTTPS on storagePort.
func startStorageServer() {
	courses.load()
	mux := http.NewServeMux()
	mux.HandleFunc("/object/", objectHandler)
	mux.HandleFunc("/relation/", relationHandler)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	// S3-style presigned-POST upload: SMM2 uploads a course (method 66 = level data,
	// 132 = thumbnails) as a multipart/form-data POST carrying a `key` + `file`, exactly
	// like AWS S3 browser uploads. We rewrite the bucket host to ourselves and accept it.
	mux.HandleFunc("/", s3PostHandler)
	// Log EVERY inbound request (method, path, content-type, length) before dispatch,
	// so an upload that never reaches a handler still shows what the console attempted.
	logged := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Printf("[SMM2 Storage] <- %s %s ct=%q len=%s from %s\n",
			r.Method, r.URL.Path, r.Header.Get("Content-Type"), r.Header.Get("Content-Length"), r.RemoteAddr)
		mux.ServeHTTP(w, r)
	})
	srv := &http.Server{Addr: fmt.Sprintf(":%d", storagePort), Handler: logged}
	// Keep-alive off: the SMM2 client (Ryujinx capture) opens a connection, does
	// the TLS handshake, fires the GET, receives the blob — then appears to hang
	// waiting for the connection to close. With Go's default keep-alive, the
	// server holds the socket open for the next request; the SMM2 client doesn't
	// issue another one and never unblocks. Closing after every response matches
	// what the real Nintendo storage CDN does (single-shot per asset) and the
	// extra per-request TLS handshake is cheap against localhost. The earlier
	// note in this block about keep-alive was about the UPLOAD path (multipart
	// POST sequence) which is a separate problem we already fixed via the form
	// 'key' field — that comment is stale.
	srv.SetKeepAlivesEnabled(false)
	fmt.Printf("[SMM2 Storage] listening HTTPS :%d (blob store)\n", storagePort)
	if err := srv.ListenAndServeTLS(certFile, keyFile); err != nil {
		fmt.Printf("[SMM2 Storage] stopped: %v\n", err)
	}
}

// objectHandler stores (PUT/POST) and serves (GET) course blobs by data_id.
func objectHandler(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/object/")
	if i := strings.IndexAny(idStr, "/?"); i >= 0 {
		idStr = idStr[:i]
	}
	dataID, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		http.Error(w, "bad data_id", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodPut, http.MethodPost:
		body, err := extractBlobBody(r)
		if err != nil {
			fmt.Printf("[SMM2 Storage] %s /object/%d FAILED reading body: %v\n", r.Method, dataID, err)
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		if err := os.MkdirAll(courseDir(dataID), 0o755); err != nil {
			fmt.Printf("[SMM2 Storage] PUT %d FAILED (mkdir): %v\n", dataID, err)
			http.Error(w, "store failed", http.StatusInternalServerError)
			return
		}
		if err := os.WriteFile(blobPath(dataID), body, 0o644); err != nil {
			fmt.Printf("[SMM2 Storage] PUT %d FAILED: %v\n", dataID, err)
			http.Error(w, "store failed", http.StatusInternalServerError)
			return
		}
		courses.setSize(dataID, uint32(len(body)))
		fmt.Printf("[SMM2 Storage] %s /object/%d <- %d bytes (ct=%q)\n", r.Method, dataID, len(body), r.Header.Get("Content-Type"))
		w.WriteHeader(http.StatusOK)
	case http.MethodGet, http.MethodHead:
		b, err := os.ReadFile(blobPath(dataID))
		if err != nil {
			fmt.Printf("[SMM2 Storage] GET %d -> 404\n", dataID)
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.Itoa(len(b)))
		fmt.Printf("[SMM2 Storage] GET /object/%d -> %d bytes\n", dataID, len(b))
		if r.Method == http.MethodGet {
			w.Write(b)
		}
	default:
		http.Error(w, "method", http.StatusMethodNotAllowed)
	}
}

// relationHandler stores relation-object blobs (thumbnails + clear-check replay)
// sent by the console to the URL returned by PreparePostRelationObject(132).
// The URL path is /relation/<dataID>/<relType> (see relationKey/parseRelationKey in
// smm2_storage.go) and the body is the same multipart/form-data envelope used for
// /object/ uploads. On success we reply 204 with an ETag like S3 does.
func relationHandler(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.URL.Path, "/relation/")
	if key == "" {
		http.Error(w, "missing key", http.StatusBadRequest)
		return
	}

	dataID, relType, ok := parseRelationKey(key)
	var path string
	if ok {
		path = relationPath(dataID, relType)
	}
	if path == "" {
		// Fallback for a key in the OLD flat "prefix_dataID" shape, still in flight from
		// before this reorg (e.g. a request queued client-side across a server restart).
		path = filepath.Join(storageDir, sanitizeKey(key))
	}

	switch r.Method {
	case http.MethodPost, http.MethodPut:
		blob, err := extractBlobBody(r)
		if err != nil {
			fmt.Printf("[SMM2 Storage] relation %s FAILED reading body: %v\n", key, err)
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			fmt.Printf("[SMM2 Storage] relation %s STORE FAIL (mkdir): %v\n", key, err)
			http.Error(w, "store failed", http.StatusInternalServerError)
			return
		}
		if err := os.WriteFile(path, blob, 0o644); err != nil {
			fmt.Printf("[SMM2 Storage] relation %s STORE FAIL: %v\n", key, err)
			http.Error(w, "store failed", http.StatusInternalServerError)
			return
		}
		sum := md5.Sum(blob)
		w.Header().Set("ETag", fmt.Sprintf("%q", hex.EncodeToString(sum[:])))
		w.Header().Set("Server", "AmazonS3")
		w.Header().Set("x-amz-request-id", "NEXTENDO0000000000")
		fmt.Printf("[SMM2 Storage] relation %s <- %d bytes (etag=%x, path=%s)\n", key, len(blob), sum[:4], path)
		w.WriteHeader(http.StatusNoContent) // 204, like S3
	case http.MethodGet, http.MethodHead:
		b, err := os.ReadFile(path)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", contentTypeForPath(path))
		w.Header().Set("Content-Length", strconv.Itoa(len(b)))
		if r.Method == http.MethodGet {
			w.Write(b)
		}
	default:
		http.Error(w, "method", http.StatusMethodNotAllowed)
	}
}

// s3PostHandler accepts the console's S3-style multipart upload (an alias for what
// Nintendo routes to AWS S3). It reads the `key` (the object path, e.g.
// ".../data/00059850236-00001") and the `file` part, stores the blob under the key,
// and answers 204 like S3. Signature/policy fields are ignored — we own the bucket.
func s3PostHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseMultipartForm(96 << 20); err != nil {
		fmt.Printf("[SMM2 Storage] POST %s: pas multipart (%v) — ct=%q\n", r.URL.Path, err, r.Header.Get("Content-Type"))
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	key := r.FormValue("key")
	// The console's `file` part carries no filename, so Go's parser files it under
	// MultipartForm.Value, not .File — read from whichever holds it.
	var blob []byte
	if f, _, err := r.FormFile("file"); err == nil {
		defer f.Close()
		blob, _ = io.ReadAll(io.LimitReader(f, 96<<20))
	} else if vals := r.MultipartForm.Value["file"]; len(vals) > 0 {
		blob = []byte(vals[0])
	} else {
		fmt.Printf("[SMM2 Storage] POST key=%q aucun champ 'file' — champs=%v\n", key, formFieldNames(r))
		http.Error(w, "no file", http.StatusBadRequest)
		return
	}
	name := sanitizeKey(key)
	if name == "" {
		http.Error(w, "no key", http.StatusBadRequest)
		return
	}
	if err := os.WriteFile(filepath.Join(storageDir, name), blob, 0o644); err != nil {
		fmt.Printf("[SMM2 Storage] UPLOAD key=%q STORE FAIL: %v\n", key, err)
		http.Error(w, "store failed", http.StatusInternalServerError)
		return
	}
	// Mirror S3's POST success headers: the console reads the ETag (the object's MD5)
	// to confirm/link the upload; a bare 204 with no ETag can stall the next step.
	sum := md5.Sum(blob)
	w.Header().Set("ETag", fmt.Sprintf("%q", hex.EncodeToString(sum[:])))
	w.Header().Set("Server", "AmazonS3")
	w.Header().Set("x-amz-request-id", "NEXTENDO0000000000")
	fmt.Printf("[SMM2 Storage] UPLOAD OK key=%q -> %s (%d bytes, etag=%x)\n", key, name, len(blob), sum[:4])
	w.WriteHeader(http.StatusNoContent) // 204, like S3
}

// extractBlobBody reads the uploaded course blob from a request to /object/<id>.
// The console was measured sending this as multipart/form-data (not a plain PUT body
// like the comment at the top of this file assumed "courses are small" for) — a real
// upload came back as one big multipart envelope, and treating that whole envelope as
// the level data would have written a corrupt (wrapped-in-boundaries) file. Parse the
// form and pull out the "file" field when the content-type says multipart; otherwise
// fall back to reading the raw body (a plain PUT with no form wrapping).
func extractBlobBody(r *http.Request) ([]byte, error) {
	ct := r.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "multipart/") {
		return readAllLimited(r, 64<<20)
	}
	if err := r.ParseMultipartForm(96 << 20); err != nil {
		return nil, fmt.Errorf("multipart parse: %w", err)
	}
	if f, _, err := r.FormFile("file"); err == nil {
		defer f.Close()
		return io.ReadAll(io.LimitReader(f, 96<<20))
	}
	if vals := r.MultipartForm.Value["file"]; len(vals) > 0 {
		return []byte(vals[0]), nil
	}
	return nil, fmt.Errorf("multipart body has no \"file\" field (fields=%v)", formFieldNames(r))
}

// sanitizeKey turns an S3 object key into a safe flat filename.
func sanitizeKey(key string) string {
	if key == "" {
		return ""
	}
	repl := strings.NewReplacer("/", "_", ":", "_", "\\", "_", "?", "_", "..", "_")
	return "obj_" + repl.Replace(key)
}

// formFieldNames lists the multipart field names present (for diagnosing an upload).
func formFieldNames(r *http.Request) []string {
	var out []string
	if r.MultipartForm != nil {
		for k := range r.MultipartForm.Value {
			out = append(out, k)
		}
		for k := range r.MultipartForm.File {
			out = append(out, k+"(file)")
		}
	}
	return out
}

func readAllLimited(r *http.Request, max int64) ([]byte, error) {
	defer r.Body.Close()
	buf := make([]byte, 0, 1<<16)
	tmp := make([]byte, 32<<10)
	var total int64
	for {
		n, err := r.Body.Read(tmp)
		if n > 0 {
			total += int64(n)
			if total > max {
				return buf, fmt.Errorf("too large")
			}
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			return buf, nil
		}
	}
}

// nowUnix returns the current unix time (isolated so the rest of the file has no
// direct time import churn).
func nowUnix() int64 { return time.Now().Unix() }
