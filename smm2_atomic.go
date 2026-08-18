package main

// smm2_atomic.go — small atomic wire-format helpers for the SMM2 DataStore
// protocol (NEX 0x73). Each function emits or reads one logical field; the
// higher-level builders (syntheticUserInfoFromProfile, buildCourseInfo,
// writeCourseInfoListResponse, etc.) compose them.
//
// Convention:
//   - write*   emits to a *nex.StreamOut (no allocation if the payload
//              is constant-sized and zero).
//   - read*    reads from a *nex.StreamIn.
//   - parse*   opens a SMM2-style param body ([u8 version][u32 substream])
//              and returns a substream + ok flag.
//
// No state, no globals, no handler logic here — purely the byte shape. See
// REFACTOR.md (Phase 1) for what this enables.

import (
	nex "github.com/NextendoNetwork/nextendo-nex"
)

// ============================================================================
// Empty-envelope writers — used by smm2EmptyBuilders in smm2_datastore.go.
// The shape is the documented "valid empty response" for a given method;
// the client accepts an empty list without erroring.
// ============================================================================

// writeEmptyList emits a length-0 list of T. Use when a method's response
// is just `list<T>` with no trailing flags.
func writeEmptyList(o *nex.StreamOut) {
	o.U32(0)
}

// writeEmptyListBool emits `list<T>(0) + bool(true)` — the standard
// search-method response (e.g. m=73/74/75/76/80/81).
func writeEmptyListBool(o *nex.StreamOut) {
	o.U32(0)
	o.Bool(true)
}

// writeEmptyListList emits `list<T>(0) + list<u32>(0)` — point_ranking
// shape (m=71): courses + ranks.
func writeEmptyListList(o *nex.StreamOut) {
	o.U32(0)
	o.U32(0)
}

// writeEmptyListListBool emits `list<T>(0) + list<u32>(0) + bool(true)` —
// leaderboard shape (m=58, m=83): courses + ranks + result.
func writeEmptyListListBool(o *nex.StreamOut) {
	o.U32(0)
	o.U32(0)
	o.Bool(true)
}

// ============================================================================
// Param stream opener — used by every parseXxx() function.
// ============================================================================

// parseParamStream opens a SMM2-style param body and runs fn with the
// resulting substream. If the body is too short, substream() returns nil,
// or fn panics, returns the zero value of T and ok=false. The recover
// inside means field reads on a malformed body don't propagate panics
// to the caller — preserving the prior behaviour of every parseXxx's
// `defer func() { recover() }()`.
//
// Generic over T so each parseXxx returns its specific type. The closure
// should return T; the helper wraps it.
//
// Usage:
//   func parseFoo(s *nex.Settings, body []byte) (a, b uint32) {
//       parseParamStream(s, body, func(sub *nex.StreamIn) bool {
//           _ = sub.U32()           // option
//           a, b = sub.U32(), sub.U32()
//           return true
//       })
//       return
//   }
//
// The closure's return is a marker (true=parsed, false=invalid); the
// real values come from the outer named returns via closure capture.
func parseParamStream[T any](s *nex.Settings, body []byte, fn func(*nex.StreamIn) T) (T, bool) {
	var zero T
	defer func() {
		if r := recover(); r != nil {
			// swallow; zero value will be returned
		}
	}()
	if len(body) < 5 {
		return zero, false
	}
	in := nex.NewStreamIn(body, s)
	_ = in.U8()
	sub := in.Substream()
	if sub == nil {
		return zero, false
	}
	return fn(sub), true
}

// readResultRange reads the ResultRange sub-structure embedded in many
// search methods: [u8 version][u32 length=8][u32 offset][u32 size]. We
// ignore the version + length (always 8 for {offset, size}) and return
// the two values.
//
// The two callers (m=74 search_courses_posted_by and m=80/81 first_clear
// / best_time) both use this exact shape; consolidating here makes a
// future ResultRange variant (e.g. v1 with extra fields) a single-file
// change.
func readResultRange(sub *nex.StreamIn) (offset, size uint32) {
	_ = sub.U8()
	_ = sub.U32() // length, always 8
	offset = sub.U32()
	size = sub.U32()
	return
}

// ============================================================================
// Single-field atomic writers — the building blocks.
// ============================================================================

// writeBoolFalse3 writes three Bool(false) in a row. The UserInfo wire
// shape has 3 trailing bools (unk4/5/6) before the stat maps; this is
// the only place in the wire format that uses 3 back-to-back bools.
func writeBoolFalse3(o *nex.StreamOut) {
	o.Bool(false)
	o.Bool(false)
	o.Bool(false)
}

// writeDateTimeNow writes the current UTC datetime as a NEX DateTime
// (u64 nanoseconds since 2000 epoch). Used when the wire field is
// "last_active" or similar "current time" markers.
func writeDateTimeNow(o *nex.StreamOut) {
	o.DateTime(nex.NowDateTime().Value())
}

// writeStringCode writes a length-prefixed NEX String from a Go string.
// Thin wrapper kept here so all wire-format atomic writers live in one
// place; it documents that the "code" / "name" / "country" fields are
// all the same shape on the wire even though semantically they differ.
func writeString(o *nex.StreamOut, s string) {
	o.String(s)
}
