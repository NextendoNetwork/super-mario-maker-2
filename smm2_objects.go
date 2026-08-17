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
	"os"
	"strconv"

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
// Per the fresh measured_live.txt capture (course "test 6" / data_id 1011), the body
// starts with TWO short NEX strings (u16 length prefix, not u32 — older SMM2 protocol),
// NOT with 4× data_id_str + u64 like CompletePostObjectsCourse(68) does:
//   u16     name_length
//   bytes   name (e.g. "test 6\0")
//   u16     desc_length
//   bytes   description
//   ...     game_style, course_theme, difficulty, level_binary (LAYOUT UNVERIFIED — see
//           hex dumps; no reliable field order from kinnay wiki or live capture yet)
//
// Important: the 4× data_id_str + u64 prefix is in the 68 RESPONSE payload (see
// parseCompletePostCourseParam if needed), NOT here. The two methods have different
// param shapes — copying 68's prefix into 66's parser is a category error.
//
// The legacy parse (name=first string, desc=second string, then tagCount/tags/style/
// theme/difficulty) was the closest documented match but also wrong: it reads
// tagCount from the 4 bytes after desc, and a u32 there is `00 01 00 00` = 0x100 = 256,
// which exceeds the 8-tag cap and produces an all-zero style/theme/difficulty. Kept
// here as the "best we have today" so the catalog still gets a usable name/desc —
// the wrong style/theme/diff defaults are filtered out by 70/73 returning empty
// anyway, so the user-visible impact is just a log warning, not a broken upload.
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

// smm2CompletePostObjectsCourse (68): per kinnay's wiki, "this method does not return
// anything" (void ack). We use this call as the trigger to assign the shareable Course
// ID to the just-uploaded course — the client immediately follows with
// get_courses(70), and the CourseInfo we return there includes that code, so SMM2
// can display it on the post-upload success screen.
//
// Data_id detection: CompletePostObjectsCourseParam is undocumented in detail, so
// instead of parsing it (risky), we generate codes for every not-yet-coded Ready
// course this PID owns. In practice a given connection only has one course mid-upload
// at a time, so the loop assigns the one new code and any older ones that were
// missed. The courseCode derivation is deterministic, so re-running it on a known
// data_id is idempotent.
func smm2CompletePostObjectsCourse(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	// The 66 alloc created the course but there is no equivalent of
	// complete_post_object(26) for the level-data path — the 68 IS the
	// completion. Mark the not-yet-Ready courses Ready and assign their
	// shareable code so the client's immediate get_courses(70) call can
	// return them with a code for the post-upload success screen.
	ready := courses.markReadyForPID(conn.PID)
	for _, m := range ready {
		courses.setCode(m.DataID, courseCode(m.DataID))
		// Mirror into the per-profile registry so UserInfo.maker_stats and
		// SearchCoursesPostedBy(74) see the new upload without waiting for
		// the next restart. recordUpload is a no-op for an unregistered PID
		// and idempotent on already-counted data_ids.
		if profiles.recordUpload(conn.PID, m.DataID) {
			fmt.Printf("[SMM2 Storage]   -> course data_id=%d marked Ready, code=%s, profile uploaded_count=%d\n",
				m.DataID, courseCode(m.DataID), profiles.get(conn.PID).UploadedCount)
		} else {
			fmt.Printf("[SMM2 Storage]   -> course data_id=%d marked Ready, code=%s\n", m.DataID, courseCode(m.DataID))
		}
	}
	fmt.Printf("[SMM2 Storage] CompletePostObjectsCourse(68) pid=%d -> ack\n", conn.PID)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, nil)
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

	// Key scheme: "<dataID>/<relType>" — matched by parseRelationKey on the upload side
	// AND by the CourseInfo thumbnail URL on the download side. Both sides reach the same
	// file on disk (relationPath), so a successful upload is fetchable without a second
	// URL translation.
	//
	// EARLIER, this method used "thumb1_<dataID>" etc. — which parseRelationKey rejected
	// (it expects exactly "<id>/<relType>"), so relationHandler fell through to a legacy
	// sanitizeKey("thumb1_<dataID>") = "obj_thumb1_<dataID>" path, and the GET side answered
	// octet-stream because contentTypeForPath looks at the .jpg extension that path didn't
	// have. Switching to the numeric key format writes the new files at the right path
	// directly, and the existing migrateFlatLayout will sweep the legacy stragglers
	// (including any uploaded before this change, like course 1019) into the new layout on
	// the next server start.
	dataID, errParse := strconv.ParseUint(requestedDataID, 10, 64)
	if errParse != nil || relationPath(dataID, relType) == "" {
		// Unknown relType or unparseable data_id — answer an error so the client stops
		// retrying, rather than build a URL we couldn't serve.
		fmt.Printf("[SMM2 Storage] PreparePostRelationObject(132) data_id=%q type=%d -> RELATION TYPE NO SOPORTADO (pid=%d)\n",
			requestedDataID, relType, conn.PID)
		return nex.NewRMCError(s, 0x73, req.CallID, 0x80690004) // DataStore::NotFound
	}
	key := relationKey(dataID, relType) // "<dataID>/<relType>"
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

// smm2PrepareGetRelationObject (61): called by the client right after
// PrepareGetObject(25) for a course it's about to play. Request shape:
//   [u8 ver=0][u32 substream=12][u64 dataID][u32 relType]
// — matches the reference capture byte-for-byte (dataID 0x3B9BE979, relType=3,
// for an in-game level being played). The reference response is a [u8 ver=0]
// [u32 body=26] struct frame containing 26 bytes of zero. We don't know the
// exact field layout inside those 26 bytes — the only verified constraint is
// that the total is 31 bytes, byte-identical to reference, so the client's downstream
// parsing lands in the same state. The 26-zero body is most likely an empty
// "RelationObjectReqGetInfo-shaped" struct (no URL, no filename, all fields = 0):
// the client reads it as "no relation object available" and moves on, instead
// of stalling on a missing reference.
//
// EARLIER (kinnay guess): we returned a fully-populated RelationObjectReqGetInfo
// pointing at our own /relation/<id>/<type> — that came straight from the kinnay
// wiki and matched the upload-side m=132, but turned out to be wrong: the kinnay
// spec is incomplete here. The reference capture shows the real shape.
func smm2PrepareGetRelationObject(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	in := nex.NewStreamIn(req.Body, s)
	_ = in.U8()           // struct version
	sub := in.Substream() // u32 length + body
	dataID := sub.U64()
	relType := sub.U32()

	// 26 zero bytes inside a [u8 ver=0][u32 body=26] struct frame, matching the
	// reference response exactly. Don't try to populate fields from kinnay's spec
	// until we have a second capture to confirm the layout.
	resp := frameStruct(s, 0, make([]byte, 26))

	fmt.Printf("[SMM2 Storage] PrepareGetRelationObject(61) data_id=%d relType=%d -> 31 bytes (26 zero body, matches the reference)\n",
		dataID, relType)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, resp)
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
		// SOURCE OF TRUTH = on-disk file size, NOT m.Size. m.Size comes from
		// setSize, which fires at PUT time. If a course blob is replaced on disk
		// by any path that doesn't go through objectHandler (manual copy, sync
		// from another host, second PUT that errored before setSize ran, etc.),
		// m.Size becomes stale and the response here advertises a wrong size.
		// The SMM2 client then tries to download exactly that many bytes (or
		// trust the Content-Length mismatch) and the course refuses to load —
		// a real capture showed data_id=1008 served with size=1472 while
		// level.bin was 376832 bytes on disk, and the client fell off before
		// the play screen.
		//
		// We stat the file directly. If the size differs from m.Size we update
		// m.Size (and persist the catalog) so the next caller gets the right
		// value without a re-stat, and the on-disk content is what's served.
		diskSize := uint32(0)
		statErr := error(nil)
		if st, err := os.Stat(blobPath(dataID)); err == nil {
			diskSize = uint32(st.Size())
		} else {
			statErr = err
		}
		fmt.Printf("[SMM2 Storage] prepare_get(25) data_id=%d m.Size=%d diskSize=%d path=%s statErr=%v\n",
			dataID, m.Size, diskSize, blobPath(dataID), statErr)
		if diskSize != m.Size {
			courses.setSize(dataID, diskSize) // persists; updates in-memory m.Size too
			m.Size = diskSize
		}

		url := fmt.Sprintf("%s/object/%d", storageURL, dataID)
		body := nex.NewStreamOut(s)
		body.String(url) // url
		// headers: must include the same `u=md5(NEXToken)` the client gets from
		// m=134 — that's the auth token it attaches to the HTTP GET for /object/<id>.
		// Without it the GET 401s (or 200s with the wrong body on Nintendo-style
		// servers) and the course blob never reaches the client. Per-element
		// substream framing is mandatory — the reference response has
		// [u32 count=1][u8 ver=0][u32 body=39][String "u"][String hex32], the
		// same shape m=134 emits. Mirror that exactly.
		const kKey = "u"
		uHex := md5Hex("4If9rL9JRLMmEvD30GAxDl") // hardcoded for now, see m=134
		elemBody := uint32(2 + len(kKey) + 1 + 2 + len(uHex) + 1)
		body.U32(1)        // count
		body.U8(0)         // element substream version
		body.U32(elemBody) // element substream length
		body.String(kKey)
		body.String(uHex)
		body.U32(m.Size) // size
		// root_ca_cert is a Buffer (u32 length prefix + bytes) per
		// NintendoClients/datastore.py DataStoreReqGetInfo.load — matches
		// what methods 24/66/132 already emit. Earlier versions wrote
		// QBuffer (u16) here, which made the client parse the first 2
		// bytes of the dataID as the root_ca length: 0x0000f003 (with a
		// 0x00 in front of an actual 0x03f0 u64) reads back as ~66 MB,
		// Ryujinx falls into a null-deref when the alloc / read fails.
		// Buffer is correct; this comment is the receipts.
		body.Buffer(courses.rootCA) // root_ca_cert
		body.U64(dataID)            // data_id
		resp := frameStruct(s, 0, body.Bytes())
		fmt.Printf("[SMM2 Storage] prepare_get(25) data_id=%d -> %s (%d bytes%s)\n",
			dataID, url, m.Size,
			func() string {
				if diskSize == 0 {
					return ", FILE MISSING"
				}
				return ""
			}())
		return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, resp)
	}

	if body, ok := capturedResponses[replayKey(0x73, 25)]; ok {
		fmt.Printf("[SMM2 Storage] prepare_get(25) data_id=%d inconnu -> replay measured (boot)\n", dataID)
		return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, body)
	}
	return nex.NewRMCError(s, 0x73, req.CallID, 0x80690004) // DataStore::NotFound
}
