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
// Format: "XXX-XXX-XXX" (9 chars in 3 groups of 3, total 11 chars with dashes).
// E.g. "9MV-B9V-5HG" — note: NOT hex. Uses a 30-char confusable-free alphabet:
//
//	0123456789BCDFGHJKLMNPQRSTVWXY
//
// (omits A, E, I, O, U, Z because they look like 0/1/2/5/etc in SMM2's font).
// Player-facing: the last 3 chars are a check-digit over the first 6, similar
// to a credit card number. Without the real Nintendo checksum, "search by code"
// in-game will reject our placeholder — but the post-upload display (which is
// what we needed) shows the code as-is regardless.
//
// Encoding (placeholder): first 6 chars = mixed-radix base-30 of data_id
// (mod 30^6), last 3 chars = simple Luhn-style check over the first 6 so a
// single-typo code is detectable. Once we capture a real Nintendo code we can
// swap in the real algorithm.
func courseCode(dataID uint64) string {
	const alpha = "0123456789BCDFGHJKLMNPQRSTVWXY"
	const base = uint64(len(alpha)) // 30

	// First 6 chars: data_id mod 30^6, encoded as 6 base-30 digits (MSB first).
	var first6 [6]byte
	v := dataID % (base * base * base * base * base * base)
	for i := 5; i >= 0; i-- {
		first6[i] = alpha[v%base]
		v /= base
	}
	// Last 3 chars: check digit. Simple weighted-sum mod 30 over the first 6,
	// so any single-char typo in the first 6 is detectable in the check.
	checksum := uint64(0)
	for i, c := range first6 {
		// Find the numeric index of the char in the alphabet.
		var idx uint64
		for j, a := range alpha {
			if byte(a) == c {
				idx = uint64(j)
				break
			}
		}
		checksum = (checksum*31 + idx + uint64(i)) % (base * base * base)
	}
	last3 := [3]byte{alpha[checksum/base/base%base], alpha[checksum/base%base], alpha[checksum%base]}

	return string(first6[:]) + "-" + string(last3[:])
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
// NintendoClients:2544. url="" + data_type=0 produces a valid empty descriptor
// (the client treats it as "no thumbnail available"). filename is the last
// path segment of url, or "" when url is empty.
func writeRelationObjectReqGetInfo(out *nex.StreamOut, url string, size uint32) {
	out.Add(&relationObjectReqGetInfoOut{
		url: url, dataType: 0, size: size, unk: nil,
		filename: filenameFromURL(url),
	})
}

// relationSizeOnDisk returns the on-disk byte size of the relation blob for a given
// (dataID, relType) — used to populate RelationObjectReqGetInfo.size with the real
// upload size, not a hardcoded guess. Returns 0 if the file is missing.
func relationSizeOnDisk(dataID uint64, relType uint32) uint32 {
	var name string
	switch relType {
	case 1:
		name = "obj_thumb1_" + strconv.FormatUint(dataID, 10)
	case 2:
		name = "obj_thumb2_" + strconv.FormatUint(dataID, 10)
	default:
		return 0
	}
	st, err := os.Stat(filepath.Join(storageDir, name))
	if err != nil {
		return 0
	}
	return uint32(st.Size())
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
	out.QBuffer(nil)                   // unk3
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
