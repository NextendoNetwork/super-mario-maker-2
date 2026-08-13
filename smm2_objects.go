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
// blob and return a DataStoreReqPostInfo pointing at our own object store — built from
// the documented structure, not a patched Copilot blob. PreparePostCourseParam's
// fields are almost entirely undocumented ("Unknown"), so we don't try to extract a
// declared size/name from it: our object store's courseMeta.Size self-corrects the
// moment the real PUT/POST arrives (see objectHandler in smm2_storage.go), so nothing
// downstream depends on knowing it up front.
func smm2PreparePostObjectCourse(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	id := courses.alloc(conn.PID, "course", 0, nil, nil, 0)
	url := fmt.Sprintf("%s/object/%d", storageURL, id)

	body := nex.NewStreamOut(s)
	body.U64(id)
	body.String(url)
	writeKeyValueList(body, nil) // headers: none needed
	writeKeyValueList(body, nil) // form: none needed — objectHandler does a plain PUT
	body.Buffer(courses.rootCA)
	resp := frameStruct(s, 0, body.Bytes())

	fmt.Printf("[SMM2 Storage] PreparePostObjectCourse(66) pid=%d -> data_id=%d (construit depuis le schéma documenté)\n", conn.PID, id)
	return nex.NewRMCSuccess(s, 0x73, 66, req.CallID, resp)
}

// smm2CompletePostObjectsCourse (68): per kinnay's wiki, "this method does not return
// anything". CompletePostObjectsCourseParam is a handful of undocumented strings plus
// a nested PreparePostCourseParam — we don't yet parse it for real course metadata
// (title, tags), so a course uploaded through this path will show up with placeholder
// metadata in our catalog until that param is reverse-engineered. Acknowledging it is
// still what lets the client consider the course "posted" instead of stalling here.
func smm2CompletePostObjectsCourse(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	fmt.Printf("[SMM2 Storage] CompletePostObjectsCourse(68) pid=%d received %d bytes -> ack (métadonnées non parsées)\n", conn.PID, len(req.Body))
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, nil)
}

// smm2PrepareRelationUpload (132): a course has FOUR relation-data uploads — selected
// by a type u32 in the request (1=one-screen thumbnail, 2=entire thumbnail,
// 3=report thumbnail, 5=clear-check replay). Each needs its own presigned target
// (distinct object key); returning the exact same descriptor for all four was the old
// approach's known failure ("hung the console mid-upload"). Now each call allocates its
// own key and builds a RelationObjectReqPostInfo from the documented structure.
func smm2PrepareRelationUpload(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	in := nex.NewStreamIn(req.Body, s)
	_ = in.U8()           // struct version
	sub := in.Substream() // body: [data_id string][type u32][size u32][...]
	_ = sub.String()      // data_id (as string) — echoes back what the client already knows
	relType := sub.U32()
	reqSize := sub.U32() // byte-size of the asset the console is about to upload

	key := fmt.Sprintf("relation_%d_%d_%d", conn.PID, relType, time.Now().UnixNano())
	url := storageURL // our s3PostHandler is a catch-all on "/", any path works

	body := nex.NewStreamOut(s)
	body.String(key) // data_id (RelationObjectReqPostInfo's is a STRING, unlike 66/24's u64)
	body.String(url)
	writeKeyValueList(body, nil)                            // headers: none
	writeKeyValueList(body, map[string]string{"key": key}) // form: the "key" our s3PostHandler reads
	body.Buffer(courses.rootCA)
	resp := frameStruct(s, 0, body.Bytes())

	fmt.Printf("[SMM2 Storage] PreparePostRelationObject(132) type=%d size=%d pid=%d key=%q -> construit depuis le schéma documenté\n",
		relType, reqSize, conn.PID, key)
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
