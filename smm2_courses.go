package main

// CourseInfo wire layout (per NintendoClients/datastore_smm2.py:2158) — server-side
// builder for get_courses(70) and search_courses_latest(73) responses.
//
// Field order is byte-for-byte with the Python class `.save()` method; deviating from
// this order shifts where the client reads each field, which silently corrupts the
// `code` (the shareable Course ID SMM2 shows post-upload) and other fields.
//
// Substructs used:
//   - CourseTimeStats (line 2308): first_completion pid, world_record_holder pid,
//     world_record u32, upload_time u32
//   - RelationObjectReqGetInfo (line 2544): url string, data_type u8, size u32,
//     unk buffer, filename string   (NOT the headers/root_ca shape from the kinnay
//     wiki — kinnay was wrong here)

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"time"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

// courseInfoHash returns a short (first 8 hex chars of sha256) fingerprint of a
// CourseInfo blob — lets us confirm with certainty, not eyeballing, whether the
// SAME course's bytes are byte-for-byte identical across two different response
// paths (e.g. search_courses_latest(73) vs search_courses_posted_by(74)), which
// read the exact same buildCourseInfo() source but were never directly diffed
// against each other at the byte level for the SAME data_id in the SAME session.
func courseInfoHash(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:4])
}

// unixToDateTime converts a Unix timestamp (seconds) to a packed NEX DateTime u64.
// NEX DateTime packs year/month/day/hour/min/sec into a 64-bit value via
// nex.MakeDateTime; the lib then writes it as a u64 in DateTime().
func unixToDateTime(unix int64) uint64 {
	t := time.Unix(unix, 0).UTC()
	return nex.MakeDateTime(t.Year(), int(t.Month()), t.Day(), t.Hour(), t.Minute(), t.Second()).Value()
}

// courseCode derives the official SMM2-style Course ID from a data_id.
//
// Returns 9 RAW alphanumeric characters, NO dashes. Confirmed via a real "Upload
// complete" screenshot: an earlier version returned "XXX-XXX-XXX" (dashes baked into
// the string) and the client showed "000-13W--JV" — a double dash. The client inserts
// its OWN dashes at positions 3/6 when displaying a 9-char code; we just need to hand
// it 9 clean characters; it does the 3-3-3 formatting itself.
//
// Uses a 30-char confusable-free alphabet:
//
//	0123456789BCDFGHJKLMNPQRSTVWXY
//
// (omits A, E, I, O, U, Z because they look like 0/1/2/5/etc in SMM2's font).
// This is a deterministic placeholder derived from data_id, not Nintendo's real
// checksum algorithm — "search by code" in-game would reject it, but the post-upload
// display (which is what we needed) now shows it correctly formatted.
func courseCode(dataID uint64) string {
	const alpha = "0123456789BCDFGHJKLMNPQRSTVWXY"
	const base = uint64(len(alpha)) // 30
	x := (dataID ^ 0xdeadbeefcafe1234) * 0xff51afd7ed558ccd
	b := make([]byte, 9)
	for i := range b {
		b[i] = alpha[x%base]
		x = x/base + 0x9e3779b97f4a7c15
	}
	return string(b)
}

// --- Substructure types implementing nex.Structure --------------------------
//
// CourseInfo embeds 3 substructures (CourseTimeStats + 2× RelationObjectReqGetInfo)
// and the wire format frames each one with [u8 version][u32 length][body]. The
// lib's `out.Add(struct)` does that framing via the Structure/Level interface —
// writing the fields directly (as we did before this fix) made SMM2 reject the
// response because it couldn't tell where the substructure ended and the next
// field began. CRITICAL: these types must implement `Levels() []nex.Level`.

type courseTimeStatsOut struct {
	firstCompletion   uint64
	worldRecordHolder uint64
	worldRecord       uint32
	uploadTime        uint32
}

func (s *courseTimeStatsOut) Levels() []nex.Level {
	return []nex.Level{{
		Version: 0,
		Save: func(out *nex.StreamOut) {
			out.PID(s.firstCompletion)
			out.PID(s.worldRecordHolder)
			out.U32(s.worldRecord)
			out.U32(s.uploadTime)
		},
	}}
}

type relationObjectReqGetInfoOut struct {
	url      string
	dataType uint8
	size     uint32
	unk      []byte
	filename string
}

func (s *relationObjectReqGetInfoOut) Levels() []nex.Level {
	return []nex.Level{{
		Version: 0,
		Save: func(out *nex.StreamOut) {
			out.String(s.url)
			out.U8(s.dataType)
			out.U32(s.size)
			out.Buffer(s.unk)
			out.String(s.filename)
		},
	}}
}

// filenameFromURL extracts the last "/"-separated segment of url, or "" if empty.
func filenameFromURL(url string) string {
	if i := strings.LastIndex(url, "/"); i >= 0 {
		return url[i+1:]
	}
	return url
}

// writeCourseTimeStats writes a CourseTimeStats substruct per NintendoClients:2308.
// All zero defaults (no completions, no world record) are a valid SMM2 state.
func writeCourseTimeStats(out *nex.StreamOut) {
	out.Add(&courseTimeStatsOut{})
}

// writeRelationObjectReqGetInfo writes a RelationObjectReqGetInfo per
// NintendoClients:2544. dataType=0 when there's no real url (empty string) — that's
// the "no thumbnail available" sentinel. When we DO have a real thumbnail on disk,
// dataType must be nonzero (1) or the client apparently treats data_type==0 as "no
// thumbnail" regardless of the URL/size being populated, and never even attempts the
// HTTP GET — a real capture confirmed the URL+size were byte-perfect (114688, matching
// the file on disk exactly) yet nothing rendered client-side, with data_type hardcoded
// to 0 unconditionally.
func writeRelationObjectReqGetInfo(out *nex.StreamOut, url string, size uint32) {
	dataType := uint8(0)
	if url != "" {
		dataType = 1
	}
	out.Add(&relationObjectReqGetInfoOut{
		url: url, dataType: dataType, size: size, unk: nil,
		filename: filenameFromURL(url),
	})
}

// relationSizeOnDisk returns the on-disk byte size of the relation blob for a given
// (dataID, relType) — used to populate RelationObjectReqGetInfo.size with the real
// upload size, not a hardcoded guess. Returns 0 if the file is missing OR if relType
// is not one we store (relationPath returns "" for those — e.g. relType 4).
func relationSizeOnDisk(dataID uint64, relType uint32) uint32 {
	p := relationPath(dataID, relType)
	if p == "" {
		return 0
	}
	st, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return uint32(st.Size())
}

// relationBytesOnDisk reads a relation blob's content from disk, or nil if missing
// or too large to embed. Used to test the hypothesis that CourseInfo.unk3 (an unused
// "bytes" field per NintendoClients — we'd been sending it empty the whole time) is
// actually meant to carry a small embedded thumbnail directly in the CourseInfo
// response, rather than the client fetching one_screen/entire_thumbnail over a
// separate HTTP GET — which, per real captures, the client NEVER attempts even with a
// fully correct RelationObjectReqGetInfo (right URL, right size, data_type=1, and
// method 134 answered successfully). A max size guards against embedding something
// absurdly large into a QBuffer (u16 length prefix, 65535-byte ceiling).
func relationBytesOnDisk(dataID uint64, relType uint32, maxSize int) []byte {
	p := relationPath(dataID, relType)
	if p == "" {
		return nil
	}
	b, err := os.ReadFile(p)
	if err != nil || len(b) > maxSize {
		return nil
	}
	return b
}

// buildCourseInfo serialises a courseMeta to a framed CourseInfo per the
// NintendoClients spec. The frameStruct wrapper matches what every other
// complex return type in this server uses (so 70/73 receive [u32 frame][body]).
func buildCourseInfo(s *nex.Settings, m *courseMeta) []byte {
	code := courseCode(m.DataID)
	name := m.Name
	if name == "" || name == "course" {
		name = "Untitled"
	}
	var tag1, tag2 uint8
	if len(m.Tags) > 0 {
		tag1 = m.Tags[0]
	}
	if len(m.Tags) > 1 {
		tag2 = m.Tags[1]
	}

	// Thumbnail URLs (empty if the file isn't on disk — SMM2 treats that as
	// "no thumbnail" rather than failing the whole CourseInfo). URL shape must match the
	// key produced by smm2PrepareRelationUpload (now "<dataID>/<relType>") so the GET
	// path's parseRelationKey recognises it and lands on the same relationPath() that
	// wrote it. An earlier version returned "/relation/thumb1_<id>" — which matched the
	// upload's old key but, because the upload has since been moved to <id>/<relType>,
	// would have hit a 404 + octet-stream even after a successful upload.
	thumb1URL := ""
	thumb2URL := ""
	// Build thumb URLs unconditionally — the client uses them to drive the next HTTP GET,
	// even when the on-disk blob is missing (in which case the HTTP handler returns 404
	// and the client shows a placeholder). Gating on sz>0 used to leave the URL empty,
	// which made the client skip the download entirely. The path matches the SMM2
	// thumbnail URL scheme ("/one_screen_thumbnail/<id>" and "/entire_thumbnail/<id>");
	// thumbnailHandler in smm2_storage.go serves both from
	// smm2_objects/courses/<id>/thumb{1,2}.jpg.
	thumb1URL, thumb2URL = thumbURLsForCourse(m)

	out := nex.NewStreamOut(s)
	out.U64(m.DataID)                  // data_id
	out.String(code)                   // code  ← THE COURSE ID SMM2 DISPLAYS
	out.PID(m.OwnerPID)                // owner_id
	out.String(name)                   // name
	out.String(m.Description)          // description
	out.U8(m.GameStyle)                // game_style (0-based: 0=SMB1, 1=SMB3, 2=SMW, 3=NSMBU)
	out.U8(m.CourseTheme)              // course_theme (0-based per style)
	out.DateTime(unixToDateTime(m.CreatedAt)) // upload_time
	// difficulty: CLAMPED to the documented 0-3 range (CourseDifficulty: EASY=0,
	// STANDARD=1, EXPERT=2, SUPER_EXPERT=3). catalog.json has several real entries
	// with difficulty=5 (from an earlier, still-unfixed parse bug in
	// parsePreparePostCourseParam) — an out-of-range enum value here is a real,
	// concrete candidate for a client-side crash while rendering the list (array
	// index out of bounds against a 4-entry difficulty-icon/name table), matching
	// exactly what was reported: spinner shows, then "communication error" with
	// NOTHING ever rendered — consistent with the client failing mid-render rather
	// than mid-network-call.
	difficulty := m.Difficulty
	if difficulty > 3 {
		difficulty = 0
	}
	out.U8(difficulty)                 // difficulty (0=Easy, 1=Normal, 2=Expert, 3=SuperExpert)
	out.U8(tag1)                       // tag1
	out.U8(tag2)                       // tag2
	out.U8(0)                          // unk1
	out.U32(0)                         // clear_condition
	out.U16(0)                         // clear_condition_magnitude
	out.U16(0)                         // unk2
	// unk3: TESTED AND REVERTED. Tried embedding the small entire_thumbnail (thumb2)
	// directly here as a hypothesis for how the client shows thumbnails without ever
	// issuing an HTTP GET for one_screen/entire_thumbnail (confirmed real JPEG bytes
	// landed in the wire — response size correctly grew to ~42KB for 15 courses — but
	// thumbnails still didn't render). Reverted to empty: no confirmed benefit, and it
	// was pure overhead (up to ~40KB per course) otherwise. This is now a known
	// limitation with no further untested, well-reasoned hypothesis — see memory notes.
	out.QBuffer(nil)
	writeU8U32Map(out, buildCoursePlayStatsMap(m))  // play_stats (PlayStatsKeys)
	writeU8U32Map(out, buildCourseRatingsMap(m))     // ratings (slot 0=like,1=heart,2=boo)
	writeU8U32Map(out, nil)            // unk4
	writeCourseTimeStats(out)          // time_stats (substruct)
	writeU8U32Map(out, m.CommentCounts) // comment_stats (per slot; empty if no comments)
	out.U8(0)                          // unk9
	out.U8(0)                          // unk10
	out.U8(0)                          // unk11
	out.U8(0)                          // unk12
	writeRelationObjectReqGetInfo(out, thumb1URL, relationSizeOnDisk(m.DataID, 1)) // one_screen_thumbnail
	writeRelationObjectReqGetInfo(out, thumb2URL, relationSizeOnDisk(m.DataID, 2)) // entire_thumbnail

	return frameStruct(s, 0, out.Bytes())
}

// buildCoursePlayStatsMap converts a courseMeta's play/clear/attempt/death
// counters into a wire Map<u8, u32> using the documented PlayStatsKeys
// (PLAYS=0, CLEARS=1, ATTEMPTS=2, DEATHS=3). Returns nil when all four are 0
// so the wire encoder writes a length-0 map.
func buildCoursePlayStatsMap(m *courseMeta) map[uint8]uint32 {
	if m.PlayCount == 0 && m.ClearCount == 0 && m.AttemptCount == 0 && m.DeathCount == 0 {
		return nil
	}
	return map[uint8]uint32{
		0: m.PlayCount,
		1: m.ClearCount,
		2: m.AttemptCount,
		3: m.DeathCount,
	}
}

// buildCourseRatingsMap converts a courseMeta's like/heart/boos counters into a
// wire Map<u8, u32> indexed by slot (0=like, 1=heart, 2=boo — same slot values
// the rate_object(15) handler writes to). Returns nil when all three are 0.
func buildCourseRatingsMap(m *courseMeta) map[uint8]uint32 {
	if m.LikeCount == 0 && m.HeartCount == 0 && m.BoosCount == 0 {
		return nil
	}
	return map[uint8]uint32{
		0: m.LikeCount,
		1: m.HeartCount,
		2: m.BoosCount,
	}
}

// smm2GetCourses handles get_courses(70). Response: list<CourseInfo> + list<result>.
//
// Per NintendoClients' GetCoursesParam: data_ids: list[int], option: int = 0 — the
// client asks for SPECIFIC data_ids (typically just the one it just uploaded), not
// "give me everything this PID owns". Ignoring the request and returning
// courses.listReady(conn.PID) (potentially a different set, count, or order than what
// was asked) was a real bug, not just cosmetic — the client correlates its request
// list to the response list positionally.
func smm2GetCourses(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	in := nex.NewStreamIn(req.Body, s)
	_ = in.U8()           // GetCoursesParam struct version
	sub := in.Substream() // body: [data_ids list<u64>][option u32]
	n := sub.U32()
	if n > 256 {
		n = 256
	}
	dataIDs := make([]uint64, 0, n)
	for i := uint32(0); i < n; i++ {
		dataIDs = append(dataIDs, sub.U64())
	}

	out := nex.NewStreamOut(s)
	var infos [][]byte
	var results []uint32
	for _, id := range dataIDs {
		if m := courses.get(id); m != nil && m.Ready {
			infos = append(infos, buildCourseInfo(s, m))
			results = append(results, 0) // Result: Success
		}
	}

	out.U32(uint32(len(infos)))
	for _, ci := range infos {
		// FIX: buildCourseInfo already returns a self-framed [ver][len][body] blob —
		// wrapping it AGAIN in out.Buffer() (its own length-prefix) added an extra
		// length field the client's parser never expected, desyncing everything after
		// the first list entry. Write it directly; frameStruct already delimits it.
		out.Write(ci)
	}
	out.U32(uint32(len(results))) // FIX: was hardcoded to 0 even when courses were returned
	for _, r := range results {
		out.U32(r)
	}

	fmt.Printf("[SMM2 Courses] get_courses(70) pid=%d requested=%d found=%d\n", conn.PID, len(dataIDs), len(infos))
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}

// smm2SearchCoursesLatest handles search_courses_latest(73) — "New Courses" in
// Course World. Per NintendoClients: SearchCoursesLatestParam{option, range}, response
// courses: list[CourseInfo], result: bool. Global browsing (every uploaded course,
// not just conn.PID's own), newest first — same buildCourseInfo used everywhere else,
// now confirmed working (real Course ID showed on a live "Upload complete" screen).
func smm2SearchCoursesLatest(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	// Not parsing option/range: SearchCoursesLatestParam's range is a pagination
	// window (offset/size) we don't need yet at this catalog size — return newest 100.
	list := courses.listAllReady(100)

	out := nex.NewStreamOut(s)
	out.U32(uint32(len(list))) // list<CourseInfo>
	for _, m := range list {
		ci := buildCourseInfo(s, m)
		out.Write(ci)
		// DEBUG: fingerprint each CourseInfo so we can directly compare against the
		// SAME data_id's bytes when it also appears in search_courses_posted_by(74) —
		// same source code, never actually byte-diffed against each other before.
		fmt.Printf("[SMM2 Courses]   73 data_id=%d hash=%s len=%d\n", m.DataID, courseInfoHash(ci), len(ci))
	}
	out.Bool(true) // result

	fmt.Printf("[SMM2 Courses] search_courses_latest(73) pid=%d -> %d course(s)\n", conn.PID, len(list))
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}

// smm2SearchCoursesHot handles search_courses_hot(84) — the "Hot/Popular Courses"
// tab in Course World. NOT documented in NintendoClients (same undocumented
// territory as 58/72/83, which all populate the same Course World Hub).
//
// Request shape is unknown. Observed in a real capture (call=66, len=14):
//   [u8 ver=0] [u32 substream_len=9] [9 bytes: ff 01 00 00 64 00 00 00 04]
// Best guess: u32 option/filter=0x1ff, u32 count=100, u8 difficulty=4
// (or game_style=4, or some other filter — we don't act on any of them
// here, we just consume the body to advance the stream).
//
// Response shape mirrors 73/74: list<CourseInfo> + bool result. Courses are
// sorted by a coarse "hotness" score (likes + hearts + plays, descending),
// newest first as tiebreaker. The listAllReadyByHotness helper in
// smm2_storage.go does the actual sort under the courseStore mutex.
//
// Previously this was a stub returning an empty list, so the "Hot Courses"
// tab was always empty (no error, just nothing to show). Now wired with
// real data and the same buildCourseInfo that 73 has already confirmed
// working (byte-for-byte identical CourseInfo blobs).
func smm2SearchCoursesHot(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	// Consume the unknown request body so the stream stays aligned if the
	// client ever sends a longer body (defensive). The exact fields don't
	// matter for our response — we return ALL Ready courses sorted by
	// hotness, with a hard cap of 100 (same limit as 73).
	in := nex.NewStreamIn(req.Body, s)
	_ = in.U8() // param struct version
	_ = in.Substream() // unknown shape, just consume

	list := courses.listAllReadyByHotness(100)

	out := nex.NewStreamOut(s)
	out.U32(uint32(len(list))) // list<CourseInfo>
	for _, m := range list {
		ci := buildCourseInfo(s, m)
		out.Write(ci)
	}
	out.Bool(true) // result

	fmt.Printf("[SMM2 Courses] search_courses_hot(84) pid=%d -> %d course(s) sorted by hotness\n", conn.PID, len(list))
	for _, m := range list {
		thumb1, thumb2 := thumbURLsForCourse(m)
		sz1 := relationSizeOnDisk(m.DataID, 1)
		sz2 := relationSizeOnDisk(m.DataID, 2)
		fmt.Printf("[SMM2 Courses]   data_id=%d thumb1=%s (size=%d) thumb2=%s (size=%d)\n",
			m.DataID, thumb1, sz1, thumb2, sz2)
	}
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}

// thumbURLsForCourse returns the one_screen_thumbnail and entire_thumbnail URLs
// for a course. Both URLs are always populated — the thumbnailHandler in
// smm2_storage.go returns 404 if the underlying file is missing, but the client
// expects to issue the GET regardless. Single source of truth so buildCourseInfo
// and the per-method log lines stay in sync.
func thumbURLsForCourse(m *courseMeta) (string, string) {
	return fmt.Sprintf("%s/one_screen_thumbnail/%d", storageURL, m.DataID),
		fmt.Sprintf("%s/entire_thumbnail/%d", storageURL, m.DataID)
}

// smm2SearchCoursesByMethod72 handles the undocumented method 72 — the third
// Course World tab (between "New" and "Hot"). Same response shape as 73/74:
// list<CourseInfo> + bool result.
//
// NOT documented in NintendoClients (same undocumented territory as 58/83/84,
// all of which populate the Course World Hub). Live capture of two consecutive
// page requests (47-byte body):
//   body[8]  = u8  offset  (0x00 → 0x64 between pages = 0 → 100)
//   body[12] = u8  limit   (0x64 = 100, constant)
// All other body bytes are filter/tag bitmasks, constant between requests.
//
// Pagination: client sends offset=0,limit=100 for first page, then
// offset=100,limit=100 for next, and so on. Server returns the page and a
// trailing bool: false = "has more pages", true = "last page" (inverted from
// the hasMore logic so the server returns bool=false when more exist).
//
// Previously a stub returning `u32 0; u8 true` — the tab always showed nothing
// (no error, just no courses). Now wired with real data.
func smm2SearchCoursesByMethod72(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	in := nex.NewStreamIn(req.Body, s)
	_ = in.U8() // param struct version (0x01)
	_ = in.Substream() // consume the rest, keep stream aligned

	// req.Body layout: [u8 ver][u32 sub_len][47 bytes payload]
	// payload[8] = u8 offset (0→100→200...), payload[12] = u8 limit (0x64=100)
	payload := req.Body[5:]
	offset := int(payload[8])
	limit := int(payload[12])
	if limit == 0 {
		limit = 100 // safety default
	}

	list, total := courses.listAllReadyPaginated(offset, limit)
	hasMore := (offset + len(list)) < total

	out := nex.NewStreamOut(s)
	out.U32(uint32(len(list)))
	for _, m := range list {
		out.Write(buildCourseInfo(s, m))
	}
	out.Bool(!hasMore) // bool=false means "more pages exist" (inverted)

	fmt.Printf("[SMM2 Courses] search_courses_method72(72) pid=%d offset=%d limit=%d -> %d course(s) [total=%d, has_more=%v]\n",
		conn.PID, offset, limit, len(list), total, hasMore)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}

// smm2SearchCoursesLeaderboard handles the undocumented method 58 — the
// "Leaderboards / Course Markers" tab in Course World. NOT documented in
// NintendoClients (same undocumented territory as 72/83/84).
//
// Response shape is the wider "ranking" format: list<CourseInfo> +
// list<u32> ranks + bool result. Each rank u32 corresponds 1:1 with a
// CourseInfo — the client uses them to render the leaderboard position
// next to each course row.
//
// Sort: by hotness (likes + hearts + plays, descending), same helper as
// 84. Ranks: 1-indexed position in the sorted list, so the top course
// gets rank=1, the next gets rank=2, etc. (We don't have an actual play-
// time / score ranking system, so "popularity rank" is a reasonable
// proxy for "leaderboard position" until a real one gets implemented.)
//
// Previously a stub returning `u32 0; u32 0; u8 true` — the tab always
// showed nothing (no error, just no courses). Now wired with real data.
func smm2SearchCoursesLeaderboard(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	// Consume the unknown request body to keep the stream aligned.
	in := nex.NewStreamIn(req.Body, s)
	_ = in.U8() // param struct version
	_ = in.Substream() // unknown shape, just consume

	list := courses.listAllReadyByHotness(100)

	out := nex.NewStreamOut(s)
	out.U32(uint32(len(list))) // list<CourseInfo>
	for _, m := range list {
		out.Write(buildCourseInfo(s, m))
	}
	out.U32(uint32(len(list))) // list<u32> ranks, 1:1 with CourseInfo
	for range list {
		// We don't have a real ranking system (no play times, no scores),
		// so every course's "rank" is 0 ("no rank assigned yet"). The client
		// can use this to render an em-dash or "—" next to each entry, or
		// to fall back to the order in the courses list. 1-indexed ranks
		// (1=top) was tried first but the client returned a "communication
		// error" — the rank value 0 is more conservative and matches the
		// semantic of "no leaderboard activity for this course".
		out.U32(0)
	}
	out.Bool(true) // result

	fmt.Printf("[SMM2 Courses] search_courses_leaderboard(58) pid=%d -> %d course(s) with ranks\n", conn.PID, len(list))
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}

// smm2GetReqGetInfoHeadersInfo handles get_req_get_info_headers_info(134). Per
// NintendoClients: takes a single "type" byte (matching RelationObjectReqGetInfo's
// data_type — the client sent 1 for our one_screen/entire thumbnails right after the
// data_type=1 fix), returns ReqGetInfoHeadersInfo{headers: list[DataStoreKeyValue],
// expiration: int}. This was completely unimplemented (falling to NotFound, showing
// as "S->C 0x73.0" in logs — an error response has no method field) — the client
// calls it as part of fetching a relation object (thumbnail) and, without a successful
// answer here, apparently never proceeds to the actual HTTP GET. Our own object store
// needs no special headers for a GET, so an empty header list + a far-future
// expiration is a valid, safe answer.
//
// Wire format (m=134 RES body = 57 bytes, derived from a real measured capture):
//
//	[u8 ver=0] [u32 structBody=52]            ← ReqGetInfoHeadersInfo struct header
//	  [u32 count=1]                           ← list<DataStoreKeyValue> count
//	  [u8 ver=0] [u32 elemBody=39]            ← per-element substream (DataStoreKeyValue is a Structure)
//	    [u16=2]['u'][0x00]                    ← String("u")
//	    [u16=33][32 hex chars][0x00]          ← String(u)
//	  [u32 expiration=60]                     ← ReqGetInfoHeadersInfo.expiration
//
// Earlier we wrote only the inner 47 bytes (count + KV strings + expiration, no
// per-element or outer struct framing), so the client failed to parse the list and
// disconnected ~8s later when it gave up waiting for the next message. The 10 missing
// bytes are the outer struct header (5) + the per-element substream header (5).
func smm2GetReqGetInfoHeadersInfo(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	var reqType uint8
	if len(req.Body) > 0 {
		reqType = req.Body[0]
	}

	// TEMP HARDCODED: Ryujinx-Nextendo currently doesn't send the "NEX" header in
	// the WebSocket upgrade request (commit 9a1eced's diag log showed
	// "Authorize stashed NEX=\"\" (len=0)"), so conn.NEXToken is empty and
	// MD5(conn.NEXToken) is MD5("") = d41d8cd9... which crashes the emulator
	// (client rejects any u != MD5(NEXToken)). Until Ryujinx-Nextendo is fixed
	// to actually emit the header, we hardcode the token the mod uses
	// (4If9rL9JRLMmEvD30GAxDl, MD5 = 69d38f81fb8d2b9979a64e47fbcc5524) — the
	// client computes the same value and accepts. Remove this and uncomment the
	// conn.NEXToken line once Ryujinx-Nextendo is fixed upstream.
	u := md5Hex("4If9rL9JRLMmEvD30GAxDl")
	// u := md5Hex(conn.NEXToken)

	// Layout math: String is u16(length+null) + bytes + null, so for "u" (1 char) we
	// emit 4 bytes total, and for the 32-char hex MD5 we emit 35 bytes total. The KV
	// element body is therefore 4 + 35 = 39, the struct body wraps that with the
	// 4-byte count, 5-byte element substream header and 4-byte expiration = 52.
	const kKey = "u"
	elemBody := uint32(2 + len(kKey) + 1 + 2 + len(u) + 1) // String(kKey) + String(u)
	structBody := uint32(4 + 5 + elemBody + 4)              // count + element substream header + element body + expiration

	out := nex.NewStreamOut(s)
	out.U8(0)              // ReqGetInfoHeadersInfo struct version
	out.U32(structBody)    // 52
	out.U32(1)             // list<DataStoreKeyValue> count
	out.U8(0)              // DataStoreKeyValue substream version
	out.U32(elemBody)      // 39
	out.String(kKey)       // "u"
	out.String(u)          // MD5(NEXToken) hex
	out.U32(60)            // expiration: 60s; we don't expire GET access

	fmt.Printf("[SMM2 Courses] get_req_get_info_headers_info(134) pid=%d type=%d -> u=%s expiration=60s (body=%d bytes, structBody=%d, elemBody=%d)\n",
		conn.PID, reqType, u, len(out.Bytes()), structBody, elemBody)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}

// md5Hex returns the lowercase hex MD5 of s. Kept in this file (rather than the
// library) so the handler can be read in isolation.
func md5Hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// smm2SearchCoursesPostedBy handles search_courses_posted_by(74) — the "courses
// posted by player X" browse path that backs the maker profile's "My courses" tab
// AND another player's profile page (when you tap their Mii, SMM2 calls 74 with that
// PID in the request, not conn.PID).
//
// Request shape (NintendoClients:1607 SearchCoursesPostedByParam):
//
//	option u32       // filter flags (per the wiki, undocumented in detail)
//	range  ResultRange  // {offset u32, size u32} pagination window
//	pids   list<u64>  // one or more owners to query
//
// Response: list<CourseInfo> + bool result. We treat the first pid as the canonical
// owner (SMM2 sends one at a time in practice) and apply the offset/size window
// against the owner's Ready list, newest first.
//
// Was a stub in smm2EmptyBuilders returning an empty list — meaning the "courses
// posted by" call the client made when viewing a profile page was answered with 0
// courses even when that player had uploaded. Now wired.
func smm2SearchCoursesPostedBy(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	ownerPID, offset, size := parseSearchCoursesPostedByParam(s, req.Body)
	// If the request didn't name a pid (shouldn't happen — it's required per spec),
	// fall back to the connected player. Mirrors the get_users(48) "fallback to
	// conn.PID when the client asked for itself with a different number" trick.
	if ownerPID == 0 {
		ownerPID = conn.PID
	}

	list := courses.listByOwnerReady(ownerPID)
	// Apply pagination window.
	if offset > uint32(len(list)) {
		offset = uint32(len(list))
	}
	end := offset
	if size > 0 {
		end = offset + size
	}
	if end > uint32(len(list)) {
		end = uint32(len(list))
	}
	page := list[offset:end]

	out := nex.NewStreamOut(s)
	out.U32(uint32(len(page)))
	for _, m := range page {
		ci := buildCourseInfo(s, m)
		out.Write(ci)
		// DEBUG: same fingerprint as 73's, for direct cross-comparison of the SAME
		// data_id's bytes between the two paths within the same test session.
		fmt.Printf("[SMM2 Courses]   74 data_id=%d hash=%s len=%d\n", m.DataID, courseInfoHash(ci), len(ci))
	}
	// PROBADO Y DESCARTADO (4 variantes de contenido para 74, todas fallan igual):
	// ack totalmente vacío, U32(0) solo, U32(0)+Bool(true), U32(0)+Bool(false).
	// Repuesto Bool(true), la forma correcta según la doc oficial — ver memoria del
	// proyecto para el cierre completo de esta investigación.
	out.Bool(true)

	respBytes := out.Bytes()
	// DEBUG: full raw hex of the outgoing response body (pre-RMC-envelope), so it can
	// be pasted back for a byte-level review without needing another packet capture.
	fmt.Printf("[SMM2 Courses]   74 RAW RESPONSE HEX (%d bytes): %s\n", len(respBytes), hex.EncodeToString(respBytes))

	fmt.Printf("[SMM2 Courses] search_courses_posted_by(74) pid=%d owner=%d offset=%d size=%d -> %d/%d course(s)\n",
		conn.PID, ownerPID, offset, size, len(page), len(list))
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, respBytes)
}

// parseSearchCoursesPostedByParam decodes the SearchCoursesPostedByParam body.
// Returns (ownerPID, offset, size). ownerPID is the FIRST pid in the request list
// (a 0-length list yields 0; the caller can then fall back to conn.PID).
//
// Per NintendoClients/datastore_smm2.py:1619:
//
//	stream.u32(option)
//	stream.extract(ResultRange)  // FRAMED substructure: [u8 version][u32 length][u32 offset][u32 size]
//	stream.list(stream.u64)      // pids
//
// FIX: ResultRange is an embedded Structure, same as CourseTimeStats and
// RelationObjectReqGetInfo elsewhere in this file — it carries its OWN
// [version][length] framing on the wire, not just its two raw u32 fields. This
// parser was reading offset/size directly after option, skipping that 5-byte
// frame entirely. Confirmed via a real capture and manual decode: the actual
// bytes at that position were version=0, length=8 (0x00 08000000), THEN
// offset=0, size=100 — our old code read the length field's bytes AS offset
// (unpacking to 2048) and the real offset/size bytes as the pid-list count
// (25600), so the real pid (1800000001, an account that owns 6 Ready courses)
// was never even reached — explaining the spurious "0 courses" for
// SearchCoursesPostedBy(74) even for an account with real uploads.
func parseSearchCoursesPostedByParam(s *nex.Settings, body []byte) (ownerPID uint64, offset, size uint32) {
	defer func() { recover() }()
	in := nex.NewStreamIn(body, s)
	_ = in.U8() // SearchCoursesPostedByParam struct version
	sub := in.Substream()
	_ = sub.U32() // option (ignored: we don't filter on it)
	_ = sub.U8()  // ResultRange: version byte
	_ = sub.U32() // ResultRange: length (always 8 for {offset,size} — not used, we know the shape)
	offset = sub.U32()
	size = sub.U32()
	n := sub.U32()
	if n > 0 {
		ownerPID = sub.U64()
		// Drain the rest of the list even though we only act on the first pid; the
		// spec allows multiple pids in one request and a future feature may want them.
		for i := uint32(1); i < n; i++ {
			_ = sub.U64()
		}
	}
	return
}

// smm2RateObject handles rate_object(15) — the like/heart/boo path. Per
// NintendoClients/datastore_smm2.py:
//
//	rate_object(target: DataStoreRatingTarget, param: DataStoreRateObjectParam,
//	            fetch_ratings: bool) -> DataStoreRatingInfo
//
// Where:
//   target = { data_id: u64, slot: u8 }  // slot 0=like, 1=heart, 2=boo
//   param  = { rating_value: s32, access_password: u32 }
//   return = { total_value: s64, count: u32, initial_value: s64 }
//
// The total_value is a sum of all rating_value's ever assigned to this slot,
// count is the number of raters, and initial_value is the seed (commonly 0 or
// the first rating). We maintain per-course counters and a per-slot
// initial_value on first-seen; the aggregate returned to the client is
// (count * 1, count) — i.e. one vote per rater, since the SMM2 wire format
// doesn't differentiate multiple votes by the same pid here (that lives in
// get_rating_with_log / DataStoreRatingLog, unimplemented).
//
// Side effects:
//   - courses.recordRating: bumps LikeCount/HeartCount/BoosCount + records
//     first-seen rating as the slot's initial_value.
//   - profiles.recordRating: bumps the owner's MakerStats.{Likes,Hearts,Boos}Received
//     counter (the per-user aggregate that goes into UserInfo.maker_stats on
//     the wire).
//
// The body uses the same [u8 version][substream] framing the rest of this
// server uses for RMC params.
func smm2RateObject(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	dataID, slot, ratingValue, ok := parseRateObjectParam(s, req.Body)
	if !ok {
		fmt.Printf("[SMM2 Courses] rate_object(15) pid=%d -> param parse failed (%dB)\n", conn.PID, len(req.Body))
		return nex.NewRMCError(s, 0x73, req.CallID, 0x80690004) // DataStore::NotFound
	}

	// Apply to the course. recordRating returns the owner's PID so we can
	// credit the per-profile aggregate in the same call.
	ownerPID := courses.recordRating(dataID, slot, int64(ratingValue))
	// Mirror on the owner (if registered). Unregistered owners just see the
	// course's own LikeCount — their profile isn't materialised just to hold
	// a like, that would create ghost entries.
	profiles.recordRating(ownerPID, slot, int64(ratingValue))

	// Aggregate we return to the client. The kinnay doc says
	// (total_value, count, initial_value) — we approximate total_value as
	// the current count (each rater contributes +1 to the aggregate) and
	// count as the count of votes seen. initial_value is whatever we first
	// stored for the slot, or 0 if the rate was 0 (which we ignored above).
	m := courses.get(dataID)
	count := uint32(0)
	if m != nil {
		switch slot {
		case 0:
			count = m.LikeCount
		case 1:
			count = m.HeartCount
		case 2:
			count = m.BoosCount
		}
	}
	initial := int64(0)
	if m != nil && m.RatingInitial != nil {
		if v, has := m.RatingInitial[slot]; has {
			initial = v
		}
	}

	out := nex.NewStreamOut(s)
	out.S64(int64(count)) // total_value: sum approximation
	out.U32(count)        // count: # of raters (one per call here)
	out.S64(initial)      // initial_value: first-seen rating for this slot
	resp := frameStruct(s, 0, out.Bytes())

	fmt.Printf("[SMM2 Courses] rate_object(15) pid=%d data_id=%d slot=%d value=%d -> count=%d initial=%d (owner=%d)\n",
		conn.PID, dataID, slot, ratingValue, count, initial, ownerPID)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, resp)
}

// parseRateObjectParam decodes the rate_object(15) request body per
// NintendoClients/datastore_smm2.py:
//
//	stream.u8()           # RateObjectParam struct version
//	substream: {
//	  stream.u64()        # target.data_id
//	  stream.u8()         # target.slot
//	  stream.s32()        # param.rating_value
//	  stream.u32()        # param.access_password (ignored — we don't lock courses)
//	}
//	stream.bool()         # fetch_ratings (out-of-substream; ignored — we always return
//	                      # the single-slot aggregate, full-rating fetch is a separate
//	                      # method, get_rating(16))
//
// The substream is part of the param struct; fetch_ratings is a sibling arg,
// matching how every other documented DataStoreClientSMM2 method on the wiki
// receives its extra bools.
func parseRateObjectParam(s *nex.Settings, body []byte) (dataID uint64, slot uint8, ratingValue int32, ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	in := nex.NewStreamIn(body, s)
	_ = in.U8() // RateObjectParam struct version
	sub := in.Substream()
	dataID = sub.U64()
	slot = sub.U8()
	ratingValue = sub.S32()
	_ = sub.U32() // access_password (ignored)
	ok = true
	return
}
