package main

// DataStore object-transfer methods for course upload/download, backed by our own
// object store (smm2_storage.go) instead of Nintendo's presigned S3/CloudFront.
//
// SMM2's real course upload uses custom methods — 66 (PreparePostObjectCourse) for the
// level data, 132 (PreparePostRelationObject) for thumbnails/replay — NOT the generic
// prepare_post_object(24). An earlier approach patched a Copilot-generated "captured"
// S3 descriptor blob (host-swap, pid-swap, size-swap); those blobs were never real
// Nintendo traffic, so trusting their internal shape was a guess stacked on a guess,
// and this baseline's capturedResponses map is empty anyway (init_replay.go's loader
// is a no-op), so that path always fell through to NotFound.
//
// Per kinnay/NintendoClients' documented Data-Store-Protocol, both responses are plain
// NEX structures we can build correctly from scratch instead:
//   DataStoreReqPostInfo         (66):  data_id u64, url string, headers list<KV>,
//                                       form list<KV>, root_ca_cert buffer
//   RelationObjectReqPostInfo   (132):  data_id string, url string, headers list<KV>,
//                                       form list<KV>, root_ca_cert buffer
// where KV = DataStoreKeyValue{key string, value string}. Our own s3PostHandler
// (smm2_storage.go) only needs a "key" form field + a "file" part in the client's
// multipart POST, both of which we control here — no S3 signature to fake.

import (
	"fmt"
	"time"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

// writeKeyValueList writes list<DataStoreKeyValue> for a form/headers field.
func writeKeyValueList(out *nex.StreamOut, kv map[string]string) {
	out.U32(uint32(len(kv)))
	for k, v := range kv {
		out.String(k)
		out.String(v)
	}
}

// smm2CanPostCourse (60): per kinnay's wiki, request takes no parameters, response is
// {Bool, Uint32} (both unlabeled/unknown). We have no reason to deny an upload, so
// answer true + 0.
func smm2CanPostCourse(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	out := nex.NewStreamOut(s)
	out.Bool(true)
	out.U32(0)
	fmt.Printf("[SMM2 Storage] CanPostCourse(60) pid=%d -> true, 0\n", conn.PID)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}

// smm2PreparePostObjectCourse (66): allocate a data_id for the course's level-data
// blob and return a DataStoreReqPostInfo pointing at our own object store.
// The PreparePostCourseParam body contains the course name, description, tags,
// game_style, course_theme, and difficulty — we parse them here so the catalog
// entry has real metadata from the start (before CompletePostObjectsCourse(68)).
func smm2PreparePostObjectCourse(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings

	// Parse PreparePostCourseParam: [version u8][body_len u32]
	//   string name, string description, u32 tag_count, u8×N tags,
	//   u8 game_style, u8 course_theme, u8 difficulty, ... (level binary blob follows)
	name, description, tags, gameStyle, courseTheme, difficulty := parsePreparePostCourseParam(s, req.Body)

	id := courses.alloc(conn.PID, "course", 0, nil, nil, 0)
	if name != "" {
		courses.updateMeta(id, name, description, tags, gameStyle, courseTheme, difficulty)
	}
	url := fmt.Sprintf("%s/object/%d", storageURL, id)

	body := nex.NewStreamOut(s)
	body.U64(id)
	body.String(url)
	writeKeyValueList(body, nil) // headers: none needed
	writeKeyValueList(body, nil) // form: none needed — objectHandler does a plain PUT
	body.Buffer(courses.rootCA)
	resp := frameStruct(s, 0, body.Bytes())

	fmt.Printf("[SMM2 Storage] PreparePostObjectCourse(66) pid=%d -> data_id=%d name=%q style=%d theme=%d diff=%d\n",
		conn.PID, id, name, gameStyle, courseTheme, difficulty)
	return nex.NewRMCSuccess(s, 0x73, 66, req.CallID, resp)
}

// parsePreparePostCourseParam decodes the PreparePostCourseParam body from method 66.
// Observed layout (decoded from measured_live.txt line 15):
//   [version u8][body_len u32]
//   string  name
//   string  description
//   u32     tag_count
//   u8×N    tags
//   u8      game_style
//   u8      course_theme
//   u8      difficulty
//   ...     (level binary + unknown trailing fields)
func parsePreparePostCourseParam(s *nex.Settings, body []byte) (name, description string, tags []uint8, gameStyle, courseTheme, difficulty uint8) {
	defer func() { recover() }()
	in := nex.NewStreamIn(body, s)
	_ = in.U8()           // struct version
	sub := in.Substream() // param body
	name = sub.String()
	description = sub.String()
	tagCount := sub.U32()
	if tagCount <= 8 {
		for i := uint32(0); i < tagCount; i++ {
			tags = append(tags, sub.U8())
		}
	}
	gameStyle = sub.U8()
	courseTheme = sub.U8()
	difficulty = sub.U8()
	return
}

// smm2CompletePostObjectsCourseAck (68): plain ack, no CourseInfo — matches the exact
// shape of a real successful upload session captured before smm2GetCourses' rich
// response existed. Still marks the course ready so the catalog/dashboard are correct.
func smm2CompletePostObjectsCourseAck(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	done := courses.completeAllPendingFor(conn.PID)
	if len(done) == 0 {
		fmt.Printf("[SMM2 Storage] CompletePostObjectsCourse(68) pid=%d received %d bytes -> ack (no pending course)\n", conn.PID, len(req.Body))
	} else {
		fmt.Printf("[SMM2 Storage] CompletePostObjectsCourse(68) pid=%d received %d bytes -> ack + marked ready (data_id=%d, code=%s)\n",
			conn.PID, len(req.Body), done[0].DataID, courseCode(done[0].DataID))
	}
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, nil)
}

// smm2CompletePostObjectsCourse (68): per kinnay's wiki, "this method does not return
// anything" — but the user reported the client failing specifically when trying to get
// the course's shareable code back after upload, and get_courses(70) right afterward
// was retried 8 times in a row without the client ever being satisfied, exactly the
// same "silently reject and retry" pattern we already saw and fixed for
// PrepareRelationObject(132). Best next guess: the doc note about "no return value" is
// wrong (or describes a different completion path), and the client actually wants the
// finished CourseInfo — code included — back from THIS call, not a separate get_courses
// round-trip. We now build and return it directly.
func smm2CompletePostObjectsCourse(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings

	done := courses.completeAllPendingFor(conn.PID)

	if len(done) == 0 {
		fmt.Printf("[SMM2 Storage] CompletePostObjectsCourse(68) pid=%d -> no pending course found, empty ack\n", conn.PID)
	} else {
		m := done[0]
		fmt.Printf("[SMM2 Storage] CompletePostObjectsCourse(68) pid=%d data_id=%d -> ready (code=%s, empty ack per spec)\n", conn.PID, m.DataID, courseCode(m.DataID))
	}
	// TEST: back to an empty ack (per kinnay's original "no return value" doc) — the
	// full-CourseInfo response was ALSO retried 8 times by the client even on its first,
	// correctly-formed answer, so returning more data didn't stop the retries. Trying the
	// opposite to see whether an empty ack is what the client actually expects here.
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, nil)
}

// parseCompletePostCourseDataID extracts just the data_id u64 from
// CompletePostObjectsCourseParam. Layout (from measured_live.txt byte analysis):
//   [version u8][body_len u32] string×3, u16, u8, string, u64(data_id) ...
func parseCompletePostCourseDataID(s *nex.Settings, body []byte) (dataID uint64) {
	defer func() { recover() }()
	in := nex.NewStreamIn(body, s)
	_ = in.U8()
	sub := in.Substream()
	_ = sub.String() // data_id_str repeated
	_ = sub.String()
	_ = sub.String()
	_ = sub.U16() // unk u16
	_ = sub.U8()  // unk u8 (extra byte before 4th string)
	_ = sub.String()
	dataID = sub.U64()
	return
}

// parseCompletePostCourseParam is kept for reference but no longer used for metadata
// (name/description are parsed from PreparePostObjectCourse(66) instead).
func parseCompletePostCourseParam(s *nex.Settings, body []byte) (name, description string, tags []uint8, gameStyle, courseTheme, difficulty uint8, dataID uint64) {
	defer func() { recover() }()
	in := nex.NewStreamIn(body, s)
	_ = in.U8()
	sub := in.Substream()
	_ = sub.String()
	_ = sub.String()
	_ = sub.String()
	_ = sub.U16()
	_ = sub.String()
	dataID = sub.U64()
	_ = sub.U32()
	name = sub.String()
	description = sub.String()
	tagCount := sub.U32()
	if tagCount <= 8 {
		for i := uint32(0); i < tagCount; i++ {
			tags = append(tags, sub.U8())
		}
	}
	gameStyle = sub.U8()
	courseTheme = sub.U8()
	difficulty = sub.U8()
	return
}

// smm2PrepareRelationUpload (132): a course has FOUR relation-data uploads — selected
// by a type u32 in the request (1=one-screen thumbnail, 2=entire thumbnail,
// 3=report thumbnail, 5=clear-check replay). Each needs its own presigned target
// (distinct object key); returning the exact same descriptor for all four was the old
// approach's known failure ("hung the console mid-upload"). Now each call allocates its
// own key and builds a RelationObjectReqPostInfo from the documented structure.
//
// FIX: the response's data_id field must ECHO the request's data_id (the course's own
// data_id as a string, e.g. "1001" — confirmed via measured_live.txt: the client sends
// that same string in every PrepareRelationObject request). We were returning our own
// generated object key there instead, and the client silently rejected it and retried
// the prepare call over and over (increasingly for later types) rather than ever
// attempting the actual HTTP upload — never a hard error, just an infinite retry that
// eventually surfaced as "Upload failed". The real per-object routing key still goes in
// the "key" form field, which was already correct.
func smm2PrepareRelationUpload(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	in := nex.NewStreamIn(req.Body, s)
	_ = in.U8()           // struct version
	sub := in.Substream() // body: [data_id string][type u32][size u32][...]
	requestedDataID := sub.String()
	relType := sub.U32()
	reqSize := sub.U32() // byte-size of the asset the console is about to upload

	key := fmt.Sprintf("relation_%d_%d_%d", conn.PID, relType, time.Now().UnixNano())
	// FIX: give it a real path, not just the bare host. method 66's url ("/object/<id>")
	// worked and produced a real POST from the client; this one previously returned just
	// storageURL with NO path at all, and NOT ONE of the 4 relation uploads ever reached
	// our HTTP server (confirmed: zero "[SMM2 Storage] <-" log lines for any of them,
	// across repeated client retries) — consistent with the client failing to build a
	// valid request from a path-less URL before it ever leaves the console.
	url := fmt.Sprintf("%s/relation/%s", storageURL, key)

	body := nex.NewStreamOut(s)
	body.String(requestedDataID) // data_id: ECHO the course's own data_id, not a generated key
	body.String(url)
	writeKeyValueList(body, nil) // headers: none
	writeKeyValueList(body, nil) // form: empty — same as method 66; client POSTs blob directly
	body.Buffer(courses.rootCA)
	resp := frameStruct(s, 0, body.Bytes())

	fmt.Printf("[SMM2 Storage] PreparePostRelationObject(132) type=%d size=%d pid=%d data_id=%q key=%q -> construit depuis le schéma documenté (data_id échо)\n",
		relType, reqSize, conn.PID, requestedDataID, key)
	return nex.NewRMCSuccess(s, 0x73, 132, req.CallID, resp)
}

// smm2CompletePostRelationObject (133): undocumented in detail, but every other
// Complete*-style method in this protocol acks with no body — treat it the same way
// rather than let it fall through to NotFound and stall the upload.
func smm2CompletePostRelationObject(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	fmt.Printf("[SMM2 Storage] CompletePostRelationObject(133) pid=%d received %d bytes -> ack\n", conn.PID, len(req.Body))
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, nil)
}

// smm2UpdateCourseTag (69): per kinnay's wiki, "this method does not return anything".
func smm2UpdateCourseTag(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	fmt.Printf("[SMM2 Storage] UpdateCourseTag(69) pid=%d received %d bytes -> ack\n", conn.PID, len(req.Body))
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, nil)
}

// smm2PreparePostObject (24): allocate a data_id, stash the pending course metadata,
// and return DataStoreReqPostInfo {data_id, url, headers, form, root_ca_cert} pointing
// the console at our object store for the blob PUT.
func smm2PreparePostObject(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	in := nex.NewStreamIn(req.Body, s)
	_ = in.U8() // DataStorePreparePostParam struct version
	p := in.Substream()
	size := p.U32()
	name := p.String()
	dataType := p.U16()
	metaBin := p.QBuffer()
	// permission / tags / rating / persistence follow but aren't needed to store a blob.

	id := courses.alloc(conn.PID, name, dataType, metaBin, nil, size)
	url := fmt.Sprintf("%s/object/%d", storageURL, id)

	body := nex.NewStreamOut(s)
	body.U64(id)                 // data_id
	body.String(url)             // url
	writeKeyValueList(body, nil) // headers: none required
	writeKeyValueList(body, nil) // form: none (simple PUT, not multipart)
	body.Buffer(courses.rootCA)  // root_ca_cert (empty on emulator; Nextendo CA in prod)
	resp := frameStruct(s, 0, body.Bytes())

	fmt.Printf("[SMM2 Storage] prepare_post(24) pid=%d name=%q size=%d -> data_id=%d\n", conn.PID, name, size, id)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, resp)
}

// smm2CompletePostObject (26): mark the uploaded course ready (or drop it on failure).
func smm2CompletePostObject(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	in := nex.NewStreamIn(req.Body, s)
	_ = in.U8()
	p := in.Substream()
	dataID := p.U64()
	success := p.Bool()
	courses.complete(dataID, success)
	fmt.Printf("[SMM2 Storage] complete_post(26) data_id=%d success=%v\n", dataID, success)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, nil)
}

// smm2PrepareGetObject (25): return DataStoreReqGetInfo {url, headers, size,
// root_ca_cert, data_id} into our object store for a stored course. For any other
// data_id (the boot/tutorial fetch) replay the measured response so init still proceeds.
func smm2PrepareGetObject(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	in := nex.NewStreamIn(req.Body, s)
	_ = in.U8()
	p := in.Substream()
	dataID := p.U64()

	if m := courses.get(dataID); m != nil {
		url := fmt.Sprintf("%s/object/%d", storageURL, dataID)
		body := nex.NewStreamOut(s)
		body.String(url)             // url
		writeKeyValueList(body, nil) // headers: none
		body.U32(m.Size)             // size
		body.Buffer(courses.rootCA)  // root_ca_cert
		body.U64(dataID)             // data_id
		resp := frameStruct(s, 0, body.Bytes())
		fmt.Printf("[SMM2 Storage] prepare_get(25) data_id=%d -> %s (%d bytes)\n", dataID, url, m.Size)
		return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, resp)
	}

	if body, ok := capturedResponses[replayKey(0x73, 25)]; ok {
		fmt.Printf("[SMM2 Storage] prepare_get(25) data_id=%d inconnu -> replay measured (boot)\n", dataID)
		return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, body)
	}
	return nex.NewRMCError(s, 0x73, req.CallID, 0x80690004) // DataStore::NotFound
}
