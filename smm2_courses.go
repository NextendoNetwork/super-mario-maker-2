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
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

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
// upload size, not a hardcoded guess. Returns 0 if the file is missing.
func relationSizeOnDisk(dataID uint64, relType uint32) uint32 {
	st, err := os.Stat(filepath.Join(storageDir, relationFileName(dataID, relType)))
	if err != nil {
		return 0
	}
	return uint32(st.Size())
}

// relationFileName returns the on-disk filename for a relation blob, matching
// smm2_objects.go's PrepareRelationUpload key scheme ("thumb1_<id>" etc, sanitized
// with an "obj_" prefix by sanitizeKey in smm2_storage.go).
func relationFileName(dataID uint64, relType uint32) string {
	switch relType {
	case 1:
		return "obj_thumb1_" + strconv.FormatUint(dataID, 10)
	case 2:
		return "obj_thumb2_" + strconv.FormatUint(dataID, 10)
	default:
		return ""
	}
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
	name := relationFileName(dataID, relType)
	if name == "" {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(storageDir, name))
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
	// "no thumbnail" rather than failing the whole CourseInfo).
	thumb1URL := ""
	thumb2URL := ""
	if sz := relationSizeOnDisk(m.DataID, 1); sz > 0 {
		thumb1URL = fmt.Sprintf("%s/relation/thumb1_%d", storageURL, m.DataID)
	}
	if sz := relationSizeOnDisk(m.DataID, 2); sz > 0 {
		thumb2URL = fmt.Sprintf("%s/relation/thumb2_%d", storageURL, m.DataID)
	}

	out := nex.NewStreamOut(s)
	out.U64(m.DataID)                  // data_id
	out.String(code)                   // code  ← THE COURSE ID SMM2 DISPLAYS
	out.PID(m.OwnerPID)                // owner_id
	out.String(name)                   // name
	out.String(m.Description)          // description
	out.U8(m.GameStyle)                // game_style (0-based: 0=SMB1, 1=SMB3, 2=SMW, 3=NSMBU)
	out.U8(m.CourseTheme)              // course_theme (0-based per style)
	out.DateTime(unixToDateTime(m.CreatedAt)) // upload_time
	out.U8(m.Difficulty)               // difficulty (0=Easy, 1=Normal, 2=Expert, 3=SuperExpert)
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
	writeU8U32Map(out, nil)            // play_stats
	writeU8U32Map(out, nil)            // ratings
	writeU8U32Map(out, nil)            // unk4
	writeCourseTimeStats(out)          // time_stats (substruct)
	writeU8U32Map(out, nil)            // comment_stats
	out.U8(0)                          // unk9
	out.U8(0)                          // unk10
	out.U8(0)                          // unk11
	out.U8(0)                          // unk12
	writeRelationObjectReqGetInfo(out, thumb1URL, relationSizeOnDisk(m.DataID, 1)) // one_screen_thumbnail
	writeRelationObjectReqGetInfo(out, thumb2URL, relationSizeOnDisk(m.DataID, 2)) // entire_thumbnail

	return frameStruct(s, 0, out.Bytes())
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
		out.Write(buildCourseInfo(s, m))
	}
	out.Bool(true) // result

	fmt.Printf("[SMM2 Courses] search_courses_latest(73) pid=%d -> %d course(s)\n", conn.PID, len(list))
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
func smm2GetReqGetInfoHeadersInfo(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	var reqType uint8
	if len(req.Body) > 0 {
		reqType = req.Body[0]
	}

	out := nex.NewStreamOut(s)
	writeKeyValueList(out, nil) // headers: none needed for our own object store
	out.U32(0x7FFFFFFF)         // expiration: far future (we don't expire GET access)

	fmt.Printf("[SMM2 Courses] get_req_get_info_headers_info(134) pid=%d type=%d -> empty headers, no expiration\n", conn.PID, reqType)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}
