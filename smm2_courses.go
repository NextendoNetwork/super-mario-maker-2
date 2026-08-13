package main

// CourseInfo builder and get_courses(70) implementation.
//
// CourseInfo wire layout (version-0, per kinnay/NintendoClients datastore_smm2.py):
//   data_id u64, code string, owner_id pid, name string, description string,
//   game_style u8, course_theme u8, upload_time DateTime, difficulty u8,
//   tag1 u8, tag2 u8, unk1 u8,
//   clear_condition u32, clear_condition_magnitude u16, unk2 u16,
//   unk3 qbuffer,
//   play_stats Map<u8,u32>, ratings Map<u8,u32>, unk4 Map<u8,u32>,
//   time_stats CourseTimeStats,
//   comment_stats Map<u8,u32>,
//   unk9..unk12 u8,
//   one_screen_thumbnail RelationObjectReqGetInfo,
//   entire_thumbnail     RelationObjectReqGetInfo
//
// CourseTimeStats (Structure):
//   unk1 u32, unk2 u32
//
// RelationObjectReqGetInfo (Structure):
//   url string, headers Map<string,string>, size u32, unk1 u8, unk2 u8, filename string
//
// get_courses(70) response: list<CourseInfo>, list<result u32>

import (
	"fmt"
	"strings"
	"time"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

// unixToDateTime converts a Unix timestamp to a packed NEX DateTime u64.
func unixToDateTime(unix int64) uint64 {
	t := time.Unix(unix, 0).UTC()
	return nex.MakeDateTime(t.Year(), int(t.Month()), t.Day(), t.Hour(), t.Minute(), t.Second()).Value()
}

// courseCode derives a stable SMM2-style course code from a data_id.
// Nintendo uses a 9-char confusable-free code — we derive one from data_id
// deterministically so it is stable across server restarts.
// Format: "XXX-XXX-XXX"
func courseCode(dataID uint64) string {
	const alpha = "0123456789BCDFGHJKLMNPQRSTVWXY" // same 30-char alphabet as makerCode
	x := (dataID ^ 0xdeadbeefcafe1234) * 0xff51afd7ed558ccd
	b := make([]byte, 9)
	for i := range b {
		b[i] = alpha[x%30]
		x = x/30 + 0x9e3779b97f4a7c15
	}
	// Insert dashes: XXX-XXX-XXX
	return string(b[0:3]) + "-" + string(b[3:6]) + "-" + string(b[6:9])
}

// writeRelationObjectReqGetInfo writes a RelationObjectReqGetInfo structure.
// url is the public download URL for the thumbnail; empty = no thumbnail available.
func writeRelationObjectReqGetInfo(out *nex.StreamOut, url string, size uint32) {
	inner := nex.NewStreamOut(out.Settings)
	inner.String(url)
	inner.U32(0)                           // headers: Map<string,string> count=0
	inner.U32(size)                        // size
	inner.U8(1)                            // unk1
	inner.U8(0)                            // unk2
	// filename: last path segment of url, or empty
	filename := url
	if i := strings.LastIndex(url, "/"); i >= 0 {
		filename = url[i+1:]
	}
	inner.String(filename)
	out.Write(frameStruct(out.Settings, 0, inner.Bytes()))
}

// writeCourseTimeStats writes the CourseTimeStats sub-structure.
func writeCourseTimeStats(out *nex.StreamOut) {
	inner := nex.NewStreamOut(out.Settings)
	inner.U32(0) // unk1
	inner.U32(0) // unk2
	out.Write(frameStruct(out.Settings, 0, inner.Bytes()))
}

// buildCourseInfo serialises one courseMeta into a framed CourseInfo binary.
func buildCourseInfo(s *nex.Settings, m *courseMeta) []byte {
	code := courseCode(m.DataID)
	name := m.Name
	if name == "" || name == "course" {
		name = "Untitled"
	}

	// tag1/tag2: use first two stored tags, or 0 (None)
	var tag1, tag2 uint8
	if len(m.Tags) > 0 {
		tag1 = m.Tags[0]
	}
	if len(m.Tags) > 1 {
		tag2 = m.Tags[1]
	}

	// thumbnail URLs — served by our storage server
	thumb1URL := fmt.Sprintf("%s/relation/thumb1_%d", storageURL, m.DataID) // one-screen
	thumb2URL := fmt.Sprintf("%s/relation/thumb2_%d", storageURL, m.DataID) // entire

	body := nex.NewStreamOut(s)
	body.U64(m.DataID)                         // data_id
	body.String(code)                          // code  (shown as "Course ID" in-game)
	body.PID(m.OwnerPID)                       // owner_id
	body.String(name)                          // name
	body.String(m.Description)                 // description
	body.U8(m.GameStyle)                       // game_style
	body.U8(m.CourseTheme)                     // course_theme
	body.DateTime(unixToDateTime(m.CreatedAt)) // upload_time: packed NEX DateTime, not raw Unix
	difficulty := m.Difficulty
	if difficulty > 3 {
		// The client's own PreparePostCourseParam sent 5 here, outside the documented
		// 0-3 range (Easy/Normal/Expert/SuperExpert per TheGreatRambler's archived
		// dataset enum) — either that dataset's enum doesn't match this live protocol's
		// encoding, or our parse of the field is off by a slot. Clamping to a safe
		// in-range value to test whether an out-of-range difficulty is what the client
		// silently rejects the finished CourseInfo over.
		difficulty = 0
	}
	body.U8(difficulty)                        // difficulty
	body.U8(tag1)                              // tag1
	body.U8(tag2)                              // tag2
	body.U8(0)                                 // unk1
	body.U32(0)                                // clear_condition (Normal)
	body.U16(0)                                // clear_condition_magnitude
	body.U16(0)                                // unk2
	body.QBuffer(nil)                          // unk3
	writeU8U32Map(body, nil)                   // play_stats
	writeU8U32Map(body, nil)                   // ratings
	writeU8U32Map(body, nil)                   // unk4
	writeCourseTimeStats(body)                 // time_stats
	writeU8U32Map(body, nil)                   // comment_stats
	body.U8(0)                                 // unk9
	body.U8(0)                                 // unk10
	body.U8(0)                                 // unk11
	body.U8(0)                                 // unk12
	writeRelationObjectReqGetInfo(body, thumb1URL, 114688) // one_screen_thumbnail
	writeRelationObjectReqGetInfo(body, thumb2URL, 7243)   // entire_thumbnail

	return frameStruct(s, 0, body.Bytes())
}

// smm2GetCourses handles get_courses(70): parse list<data_id> from the request,
// look each one up in the catalog, and return list<CourseInfo> + list<result>.
// Unknown data_ids are silently skipped (result=NotFound for that slot).
func smm2GetCourses(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	in := nex.NewStreamIn(req.Body, s)
	_ = in.U8()           // GetCoursesParam struct version
	sub := in.Substream() // param body: [data_ids list<u64>][resultOption u32]
	n := sub.U32()
	if n > 256 {
		n = 256
	}
	dataIDs := make([]uint64, 0, n)
	for i := uint32(0); i < n; i++ {
		dataIDs = append(dataIDs, sub.U64())
	}

	var infos [][]byte
	var results []uint32
	for _, id := range dataIDs {
		m := courses.get(id)
		if m != nil && m.Ready {
			infos = append(infos, buildCourseInfo(s, m))
			results = append(results, 0) // Success
		}
		// Skip courses not found or not ready — don't add to either list
	}

	out := nex.NewStreamOut(s)
	out.U32(uint32(len(infos)))
	for _, ci := range infos {
		out.Write(ci)
	}
	out.U32(uint32(len(results)))
	for _, r := range results {
		out.U32(r)
	}

	fmt.Printf("[SMM2 Courses] get_courses(70) requested %d ids -> %d found\n", len(dataIDs), len(infos))
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}
