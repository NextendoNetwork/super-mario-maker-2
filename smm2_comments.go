package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

// smm2_comments.go — Course comments (SMM2 DataStore protocol).
//
// Comments are the player-authored text/face/picture posts attached to a
// course. They live per-course (a comment is owned by a course, not a
// user) and are queried via two documented methods:
//
//	m=94  search_comments_in_order  paginated (ResultRange) list, oldest→newest
//	m=95  search_comments           all-in-one list, no pagination
//
// The wire format is list<CommentInfo> (m=94 also has a trailing bool
// "result" — true if there's more, false if you've reached the end).
//
// Per kinnay/datastore_smm2.py:2032, CommentInfo has 17 fields, most of
// which are "unk" because the SMM2 binary doesn't expose their semantics
// and kinnay never received a name from Nintendo. We persist the ones we
// can give meaning to (comment_id, body, author PID, author nickname,
// timestamp, face/picture) and emit zeros for the rest — the client is
// defensive about unknown fields and only renders the ones it knows.
//
// Comments are seeded once at startup (defaultCommentsFor) so every course
// shows a non-empty comments list out of the box. A future "post_comment"
// RMC method can be added by calling comments.add(); for now there's no
// documented method number (kinnay indexes are 1-49, 53-95, 103, 131, 134,
// 153-157, 160-162 — none of those is "create a comment"). The closest
// undocumented candidates would be m=11 (CompleteUpdateObject) or a new
// private method.

// CommentPictureReqGetInfoWithoutHeaders is the sub-struct that carries
// a comment's attached image. Identical to RelationObjectReqGetInfo except
// it's embedded in a CommentInfo (no outer KV headers) — see
// kinnay/datastore_smm2.py:2128.
type commentPictureReqGetInfoWithoutHeadersOut struct {
	url      string
	dataType uint8
	size     uint32
	unk      []byte
	filename string
}

func (s *commentPictureReqGetInfoWithoutHeadersOut) Levels() []nex.Level {
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

// commentInfoOut is one SMM2 comment posted on a course. Wire shape per
// kinnay/datastore_smm2.py:2059 (load order = save order):
//
//	u64  comment_id          (unique per comment; we use (courseID<<32) | idx)
//	str  body                (the comment text)
//	u8   type_or_face        (face/emote, 0 = no face, 1-22 = SMM2 face IDs)
//	u8   unk4                (kind/source flag, 0)
//	u64  author_pid          (the PID who posted the comment)
//	u16  unk6, unk7          (unknown pairs; we send 0)
//	u8   unk8, unk9          (unknown flags; we send 0)
//	u16  unk10               (unknown; we send 0)
//	bool unk11, unk12        (unknown bools; we send false)
//	dt   timestamp           (UTC, when the comment was posted)
//	qbuf face                (raw face bytes if type_or_face != 0; we send empty)
//	str  author_nickname     (display name of the author)
//	pic  comment_picture     (URL/dataType/size/unk/filename of the picture)
//	u16  unk16, u8 unk17     (trailing unknown; we send 0)
type commentInfoOut struct {
	commentID      uint64
	body           string
	typeOrFace     uint8
	unk4           uint8
	authorPID      uint64
	unk6           uint16
	unk7           uint16
	unk8           uint8
	unk9           uint8
	unk10          uint16
	unk11          bool
	unk12          bool
	timestamp      uint64
	face           []byte
	authorNickname string
	picture        *commentPictureReqGetInfoWithoutHeadersOut
	unk16          uint16
	unk17          uint8
}

func (s *commentInfoOut) Levels() []nex.Level {
	return []nex.Level{{
		Version: 0,
		Save: func(out *nex.StreamOut) {
			out.U64(s.commentID)
			out.String(s.body)
			out.U8(s.typeOrFace)
			out.U8(s.unk4)
			out.U64(s.authorPID)
			out.U16(s.unk6)
			out.U16(s.unk7)
			out.U8(s.unk8)
			out.U8(s.unk9)
			out.U16(s.unk10)
			out.Bool(s.unk11)
			out.Bool(s.unk12)
			out.DateTime(s.timestamp)
			out.QBuffer(s.face)
			out.String(s.authorNickname)
			out.Add(s.picture)
			out.U16(s.unk16)
			out.U8(s.unk17)
		},
	}}
}

// comment is the on-disk JSON representation of one SMM2 comment. Much
// smaller than commentInfoOut — we only persist the fields that have
// meaning, and synthesise the rest at wire-emit time. Stored in
// smm2_objects/comments.json (one big array, all courses interleaved
// for simplicity; per-course queries are O(N) which is fine at our scale).
type comment struct {
	CommentID  uint64 `json:"comment_id"`
	CourseID   uint64 `json:"course_id"`
	AuthorPID  uint64 `json:"author_pid"`
	AuthorName string `json:"author_name"`
	Body       string `json:"body"`
	FaceID     uint8  `json:"face_id"`    // 0 = no face, 1-22 = SMM2 face IDs
	PictureURL string `json:"picture_url"` // empty = no picture
	CreatedAt  int64  `json:"created_at"` // unix seconds
}

// commentStore keeps every comment in memory (one slice, no per-course
// index) and persists to smm2_objects/comments.json on every mutation.
// We index by courseID at query time (linear scan; fine up to ~10k
// comments which is well above any realistic SMM2 load).
type commentStore struct {
	mu       sync.Mutex
	nextID   uint64
	all      []comment
	filepath string // path to comments.json
}

var comments = &commentStore{all: nil, nextID: 1}

const commentsFile = "smm2_objects/comments.json"

func (c *commentStore) load() {
	c.filepath = commentsFile
	if b, err := os.ReadFile(c.filepath); err == nil {
		var list []comment
		if json.Unmarshal(b, &list) == nil {
			c.all = list
			// nextID = max(existing comment_ids) + 1, so newly added comments
			// don't collide on restart. Encoding-safe: commentID is
			// (courseID<<32) | idx, so we take the lower 32 bits of the
			// max and add 1 to seed the per-course counter.
			var maxLow uint32
			for _, c := range c.all {
				if lo := uint32(c.CommentID); lo > maxLow {
					maxLow = lo
				}
			}
			c.nextID = uint64(maxLow) + 1
		}
	}
	// If the file doesn't exist (first run, or deleted), seed a few
	// starter comments per course so the UI shows something useful from
	// the first play.
	if len(c.all) == 0 {
		c.seedDefaults()
	}
}

// persistLocked writes the in-memory comment list to disk. MUST be called
// with c.mu held.
func (c *commentStore) persistLocked() {
	if c.filepath == "" {
		c.filepath = commentsFile
	}
	b, err := json.MarshalIndent(c.all, "", "  ")
	if err != nil {
		fmt.Printf("[SMM2 Comments] persist marshal err: %v\n", err)
		return
	}
	tmp := c.filepath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		fmt.Printf("[SMM2 Comments] persist write err: %v\n", err)
		return
	}
	if err := os.Rename(tmp, c.filepath); err != nil {
		fmt.Printf("[SMM2 Comments] persist rename err: %v\n", err)
	}
}

// forCourse returns every comment on dataID, ordered oldest→newest.
// Empty slice (not nil) if the course has no comments.
func (c *commentStore) forCourse(dataID uint64) []comment {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]comment, 0)
	for _, c := range c.all {
		if c.CourseID == dataID {
			out = append(out, c)
		}
	}
	return out
}

// add appends a new comment. Returns the assigned commentID.
func (c *commentStore) add(courseID, authorPID uint64, authorName, body string, faceID uint8, pictureURL string) uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	// comment_id = (courseID << 32) | lower32(nextID). Encoding two
	// fields into a u64 keeps the wire-side comment_id self-describing
	// (any caller can extract courseID from the high bits) and guarantees
	// uniqueness within a course. nextID is just a per-process counter.
	id := (courseID << 32) | (c.nextID & 0xFFFFFFFF)
	c.nextID++
	c.all = append(c.all, comment{
		CommentID:  id,
		CourseID:   courseID,
		AuthorPID:  authorPID,
		AuthorName: authorName,
		Body:       body,
		FaceID:     faceID,
		PictureURL: pictureURL,
		CreatedAt:  time.Now().Unix(),
	})
	c.persistLocked()
	return id
}

// seedDefaults populates a few starter comments per course in the
// catalog so the comments UI isn't empty on first launch. Idempotent —
// only runs when the store is empty.
func (c *commentStore) seedDefaults() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.all) > 0 {
		return
	}
	now := time.Now().Unix()
	type seed struct {
		courseID uint64
		author   string
		authorPID uint64
		body     string
		face     uint8
	}
	seeds := []seed{
		// course 1000 "el salto" by beertest
		{1000, "Toad_42", 1800000002, "¡Buen nivel! Me costó la tercera vida 😅", 12},
		{1000, "MarioFan", 1800000003, "At the same time! That jump is so precise.", 1},
		{1000, "Luigi_99", 1800000004, "Add a checkpoint before the second piranha plant", 5},
		// course 1001 "test 1"
		{1001, "Toad_42", 1800000002, "test comment for course 1001", 0},
		{1001, "MarioFan", 1800000003, "Another test comment", 3},
		// course 1002 "tetst 55"
		{1002, "Wario_Best", 1800000005, "Coinhungry mode ON 🪙", 7},
		// course 1003 "test 56"
		{1003, "Toad_42", 1800000002, "Nice clear!", 14},
	}
	for i, s := range seeds {
		id := (s.courseID << 32) | uint64(i+1)
		c.all = append(c.all, comment{
			CommentID:  id,
			CourseID:   s.courseID,
			AuthorPID:  s.authorPID,
			AuthorName: s.author,
			Body:       s.body,
			FaceID:     s.face,
			PictureURL: "",
			CreatedAt:  now - int64((len(seeds)-i)*3600), // stagger timestamps by 1h each
		})
	}
	c.nextID = uint64(len(seeds)) + 1
	c.persistLocked()
	fmt.Printf("[SMM2 Comments] seeded %d starter comments across %d courses\n", len(c.all), len(seeds))
}

// commentToOut converts the in-memory comment (small) into the wire-side
// commentInfoOut (large, with all 17 fields). All "unk" fields go to
// zero values — we don't have any signal in the data to set them to
// anything meaningful, and the SMM2 client treats them as opaque.
func commentToOut(c comment) *commentInfoOut {
	pic := &commentPictureReqGetInfoWithoutHeadersOut{
		url: c.PictureURL, dataType: 0, size: 0, unk: nil,
		filename: filenameFromURL(c.PictureURL),
	}
	if c.PictureURL != "" {
		pic.dataType = 1 // mark "has picture" so the client issues the GET
	}
	return &commentInfoOut{
		commentID:      c.CommentID,
		body:           c.Body,
		typeOrFace:     c.FaceID,
		authorPID:      c.AuthorPID,
		authorNickname: c.AuthorName,
		timestamp:      unixToDateTime(c.CreatedAt),
		picture:        pic,
	}
}

// smm2SearchCommentsInOrder (94): per kinnay datastore_smm2.py:4979
//	request:  [u8 ver=0][u32 substream=20][u64 data_id][u32 offset][u32 length]
//	response: list<CommentInfo> + bool result
// The reference capture (OCW) returns 217 bytes for one comment on
// course 1000003046. Our seeded comments are smaller, but the structure
// matches. m=95 below is the same response minus the bool.
func smm2SearchCommentsInOrder(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	var dataID uint64
	var offset, length uint32
	if len(req.Body) >= 5 {
		defer func() { recover() }()
		in := nex.NewStreamIn(req.Body, s)
		_ = in.U8()
		sub := in.Substream()
		if sub != nil {
			dataID = sub.U64()
			offset = sub.U32()
			length = sub.U32()
		}
	}
	all := comments.forCourse(dataID)
	// Apply pagination (offset, length). ResultRange is inclusive on
	// both ends, so [offset, offset+length) is the documented window.
	if offset > uint32(len(all)) {
		offset = uint32(len(all))
	}
	end := offset + length
	if length == 0 || end > uint32(len(all)) {
		end = uint32(len(all))
	}
	page := all[offset:end]
	// Result bool: false when this is the last page (no more comments
	// after the returned window), true if there's more. SMM2's pagination
	// semantics — kinnay docstring says "result = true if more pages
	// exist", so the simplest is `end < len(all)`.
	more := end < uint32(len(all))

	out := nex.NewStreamOut(s)
	// list<CommentInfo>: u32 count followed by N flat CommentInfo
	// records (no per-comment framing — see writeCommentInfo).
	out.U32(uint32(len(page)))
	for _, c := range page {
		writeCommentInfo(out, commentToOut(c))
	}
	out.Bool(more)
	fmt.Printf("[SMM2 Comments] search_comments_in_order(94) pid=%d course=%d offset=%d length=%d -> %d comments, more=%v\n",
		conn.PID, dataID, offset, length, len(page), more)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}

// smm2SearchComments (95): per kinnay datastore_smm2.py:4994
//	request:  [u8 ver=0][u32 substream=8][u64 data_id]  — OR raw u64 (kinnay
//	          uses input.u64() directly without a substream wrapper, but
//	          our dispatcher runs every method through the same NEX RMC
//	          header so a missing substream header would leave 4 extra
//	          bytes unparsed. The OCW capture we have shows
//	          len(req.Body)=8 with just a raw u64, so we accept both.)
//	response: list<CommentInfo> (no bool — m=95 is "all in one")
func smm2SearchComments(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	var dataID uint64
	if len(req.Body) >= 8 {
		defer func() { recover() }()
		in := nex.NewStreamIn(req.Body, s)
		// Try the substream-wrapped form first (mirrors every other
		// DataStore method's struct header). If that fails (no
		// substream bytes), fall back to a raw u64 — kinnay's spec
		// is bare u64, and the OCW capture we have uses that shape
		// (body_len=8, all 8 bytes are the data_id).
		if len(req.Body) > 8 {
			_ = in.U8()
			sub := in.Substream()
			if sub != nil && sub.Remaining() >= 8 {
				dataID = sub.U64()
			}
		} else {
			dataID = in.U64()
		}
	}
	all := comments.forCourse(dataID)
	out := nex.NewStreamOut(s)
	out.U32(uint32(len(all)))
	for _, c := range all {
		writeCommentInfo(out, commentToOut(c))
	}
	fmt.Printf("[SMM2 Comments] search_comments(95) pid=%d course=%d -> %d comments\n",
		conn.PID, dataID, len(all))
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}

// writeCommentInfo emits one CommentInfo to the OUTER stream WITHOUT
// a struct frame. Per kinnay/datastore_smm2.py:2079 (save), the outer
// comment is just a sequence of field writes — no version byte, no
// u32 size prefix. The picture IS a sub-struct so it gets framed
// separately via out.Add. CRITICAL: this differs from the
// courseTimeStatsOut pattern (which IS framed) — the difference is
// that CourseTimeStats is referenced via stream.extract(...) on the
// reader side, which reads a u8+u32 frame, while CommentInfo is read
// with stream.unk1/unk2/... which is a flat field read. A framed
// CommentInfo crashes the SMM2 client with a null-pointer deref
// partway through the field reads (verified: 207-byte m=95 response
// for 2 comments on course 1001 caused InvalidAccessHandler at
// virtual 0x00000000 right after the m=95 response was returned).
func writeCommentInfo(out *nex.StreamOut, c *commentInfoOut) {
	out.U64(c.commentID)
	out.String(c.body)
	out.U8(c.typeOrFace)
	out.U8(c.unk4)
	out.U64(c.authorPID)
	out.U16(c.unk6)
	out.U16(c.unk7)
	out.U8(c.unk8)
	out.U8(c.unk9)
	out.U16(c.unk10)
	out.Bool(c.unk11)
	out.Bool(c.unk12)
	out.DateTime(c.timestamp)
	out.QBuffer(c.face)
	out.String(c.authorNickname)
	// Picture IS a sub-struct — kinnay uses stream.add() here, which
	// writes [u8 ver=0][u32 len][body]. Mirror that with out.Add.
	out.Add(c.picture)
	out.U16(c.unk16)
	out.U8(c.unk17)
}

// init loads the persisted comment store from disk at startup. No
// main.go wiring needed (Go runs every init() in the package on binary
// load). When comments.json is missing the loader seeds 8 starter
// comments across the 4 catalog courses so the UI shows something
// useful from the first request.
func init() {
	comments.load()
}
