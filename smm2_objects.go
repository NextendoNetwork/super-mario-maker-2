package main

// DataStore object-transfer methods (prepare_post 24 / prepare_get 25 / complete_post
// 26) backed by our own object store (smm2_storage.go) instead of Nintendo's presigned
// S3/CloudFront. These are what actually upload/download a course's level BLOB.

import (
	"os"
	"bytes"
	"fmt"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

// SMM2's real upload flow is NOT prepare_post_object(24) — it is a set of custom
// methods (66 = level data, 132 = thumbnails) that hand back an AWS-S3 presigned-POST
// descriptor (url + policy/signature form fields). The console then does a multipart
// POST of the blob to that bucket. We keep the measured descriptor verbatim but swap
// the bucket host for our own object store, so the blob is POSTed to us instead.
//
// The upload structures are custom, nested and undocumented, so rather than decode and
// rebuild them we replace the S3 host STRING with one of the EXACT same byte length —
// a same-length swap needs no struct/list length fixups anywhere in the blob.
var s3UploadHost = []byte("626727242799-datastore-nex-ecs.s3.amazonaws.com/")

// ourUploadHost is our object-store host+path padded to len(s3UploadHost). The padding
// is a throwaway path segment (the console prepends https://, POSTs there; our catch-all
// handler reads the `key` form field, not the path).
func ourUploadHost() []byte {
	base := storageHostPort + "/"
	if len(base) >= len(s3UploadHost) {
		return []byte(base[:len(s3UploadHost)])
	}
	return append([]byte(base), bytes.Repeat([]byte("a"), len(s3UploadHost)-len(base))...)
}

// rewriteUploadHost swaps the measured S3 bucket host for ours in an upload descriptor.
func rewriteUploadHost(body []byte) []byte {
	return bytes.ReplaceAll(body, s3UploadHost, ourUploadHost())
}

// capturedRelationPID is the pid embedded in every relation object key/name of the
// mesure sur un compte de reference. The console builds
// its own asset under ITS pid, so a descriptor carrying a foreign pid is inconsistent
// with what the console expects and it refuses to POST the relation (the course-data
// key has no pid, which is why THAT upload goes through). We rewrite it to the caller's
// pid — a same-length swap (u64 hex is always 16 chars), so no length fixups.
// Identifiant du compte de reference sur lequel la mesure a ete faite. Il ne vit pas
// dans le code : sans la variable, la reecriture du descripteur est simplement sautee.
var capturedRelationPID = os.Getenv("SMM2_RELATION_PID_TOKEN")

// capturedRelationSize is the asset byte-size baked into each measured relation
// descriptor's object name/key (as lowercase hex, e.g. "..._1ba5_..."). The console
// rejects a descriptor whose size doesn't match the asset it is about to upload (it
// asked for a specific size in the request), so we rewrite the measured size to the
// size the console actually requested. Keys: 1=one-screen 2=entire 3=report 5=clear-check.
var capturedRelationSize = map[uint32]uint32{1: 0x1c000, 2: 0x1ba5, 3: 0x1697, 5: 0xd1a}

// rewriteRelationDescriptor rewrites a method-132 (relation) descriptor for the caller:
// the requested asset size, the embedded pid -> caller's pid, then S3 host -> our store.
func rewriteRelationDescriptor(body []byte, relType uint32, reqSize uint32, pid uint64) []byte {
	if capSize, ok := capturedRelationSize[relType]; ok && reqSize != 0 && reqSize != capSize {
		oldTok := []byte(fmt.Sprintf("_%x_", capSize))
		newTok := []byte(fmt.Sprintf("_%x_", reqSize))
		if len(oldTok) == len(newTok) {
			body = bytes.ReplaceAll(body, oldTok, newTok)
		} else {
			// Differing hex length would shift the enclosing string/struct lengths; a
			// length-aware rebuild is needed. Log so we notice which levels hit this.
			fmt.Printf("[SMM2 Storage] ⚠ relation type=%d size 0x%x->0x%x (len diff, non réécrit)\n", relType, capSize, reqSize)
		}
	}
	body = bytes.ReplaceAll(body, []byte(capturedRelationPID), []byte(fmt.Sprintf("%016x", pid)))
	return rewriteUploadHost(body)
}

// method 132 uploads a course's RELATION DATA, and a course has FOUR distinct ones —
// selected by a type u32 in the request: 1=one-screen thumbnail, 2=entire thumbnail,
// 3=report thumbnail, 5=clear-check replay. Nintendo returns a different presigned
// descriptor (distinct object key) per type; replaying ONE for all four left three
// objects with no valid upload target and hung the console mid-upload. We keep the
// measured descriptor for each type and hand back the matching one.
var m132ByType = map[uint32][]byte{}

var m132TypeName = map[uint32]string{1: "onescreen", 2: "entire", 3: "report", 5: "clearcheck"}

// loadM132Types reads the per-type method-132 descriptors embedded under measured/.
func loadM132Types() {
	for t, name := range m132TypeName {
		if b, err := capturedFS.ReadFile("measured/resp_0x73_m132_" + name + ".bin"); err == nil {
			m132ByType[t] = b
		}
	}
	fmt.Printf("[SMM2 Storage] %d descripteurs relation-data (method 132) chargés\n", len(m132ByType))
}

// smm2PrepareRelationUpload (method 132) returns the presigned upload descriptor for
// the requested relation-data type, with the bucket host rewritten to our object store.
func smm2PrepareRelationUpload(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	in := nex.NewStreamIn(req.Body, s)
	_ = in.U8()           // struct version
	sub := in.Substream() // body: [data_id string][type u32][size u32][...]
	_ = sub.String()      // data_id (as string)
	relType := sub.U32()
	reqSize := sub.U32() // the byte-size of the asset the console will upload

	tmpl := m132ByType[relType]
	if tmpl == nil {
		tmpl = capturedResponses[replayKey(0x73, 132)] // fallback: any measured 132
	}
	if tmpl == nil {
		return nex.NewRMCError(s, 0x73, req.CallID, 0x80690004)
	}
	body := rewriteRelationDescriptor(tmpl, relType, reqSize, conn.PID)
	fmt.Printf("[SMM2 Storage] prepare-relation(132) type=%d(%s) size=0x%x pid=%d -> réécrit (%do)\n", relType, m132TypeName[relType], reqSize, conn.PID, len(body))
	return nex.NewRMCSuccess(s, 0x73, 132, req.CallID, body)
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
	body.U64(id)                // data_id
	body.String(url)            // url
	body.U32(0)                 // headers: none required
	body.U32(0)                 // form: none (simple PUT, not multipart)
	body.Buffer(courses.rootCA) // root_ca_cert (empty on emulator; Nextendo CA in prod)
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
		body.String(url)            // url
		body.U32(0)                 // headers: none
		body.U32(m.Size)            // size
		body.Buffer(courses.rootCA) // root_ca_cert
		body.U64(dataID)            // data_id
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
