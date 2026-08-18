# SMM2 Server — Refactor Plan

> Audit of duplicated code and proposal for a standardised, atomic-function
> layout. **No code has been changed yet** — this document is the
> pre-implementation plan. Each section lists what is duplicated, what the
> canonical version should look like, and which files it touches.

## Guiding principles

1. **One function, one purpose** — atomic: read one field, write one
   field, build one sub-struct.
2. **Builders compose** — `buildUserInfo` should call `writePlayStats`,
   which should call `writeU8U32Map`. No god-functions that emit 300
   bytes inline.
3. **Same input shape → same output shape** — when two handlers
   produce the same wire envelope, they should call the same helper
   to build it.
4. **Same input fields → same parser** — when two params have the
   same wire shape, they should share a parser, not reimplement it.
5. **No behaviour change** — the refactor must produce byte-exact
   identical output for every handler. Verified by `go build` plus
   a smoke test against a real capture if possible.

## 1. Duplications found

### 1.1. `buildXxxMap` family — 5 functions, same pattern

| Function | File:line | Pattern |
|---|---|---|
| `buildPlayStatsMap` | smm2_users.go:774 | "if all-zero → nil, else map[u8]u32 with kinnay key" |
| `buildMultiplayerStatsMap` | smm2_users.go:786 | same pattern, different keys |
| `buildMakerStatsMap` | smm2_users.go:814 | same pattern, only emits verified key 0 |
| `buildCoursePlayStatsMap` | smm2_courses.go:302 | same pattern, course-side keys |
| `buildCourseRatingsMap` | smm2_courses.go:321 | same pattern, rating-slot keys |

Each one has the same shape:
```go
if allValuesAreZero(...) { return nil }
return map[uint8]uint32{...}
```

**Canonical version** (single helper + thin key-mapping funcs):
```go
// buildU8U32MapIfAny returns nil if entries is empty, else entries itself.
// Lets every buildXxxMap shrink to a single literal map + this call.
func buildU8U32MapIfAny(entries map[uint8]uint32) map[uint8]uint32 {
    if len(entries) == 0 { return nil }
    return entries
}

// Plus a key-typed constructor per stat kind:
func playStatsToMap(p playStats) map[uint8]uint32 { ... }      // 4 keys
func multiplayerStatsToMap(m multiplayerStats) map[uint8]uint32 { ... }  // 5 keys
func makerStatsToMap(m makerStats) map[uint8]uint32 { ... }    // 1+ keys (verified set)
func coursePlayStatsToMap(m *courseMeta) map[uint8]uint32 { ... }  // 4 keys from m
func courseRatingsToMap(m *courseMeta) map[uint8]uint32 { ... }      // 3 keys from m
```

Each thin constructor has the all-zero guard, and the helper handles
"nil if empty". Eliminates 5 near-duplicate "if all zero" checks.

### 1.2. `parseXxx` family — 8 functions, all `defer recover` + open substream

| Function | File:line |
|---|---|
| `parseSearchCoursesPostedByParam` | smm2_courses.go:707 |
| `parseSearchCoursesByPIDParam` | smm2_courses.go:734 |
| `parseSearchCoursesFirstClearParam` | smm2_courses.go:750 |
| `parseRateObjectParam` | smm2_courses.go:1016 |
| `parsePreparePostCourseParam` | smm2_objects.go:134 |
| `parseRegisterUserParam` | smm2_users.go:692 |
| `parseGetUsersPIDs` | smm2_profile.go:197 |
| `parseGetUsersOption` | smm2_profile.go:218 |

All follow:
```go
defer func() { recover() }()
in := nex.NewStreamIn(body, s)
_ = in.U8()           // struct version
sub := in.Substream()
// read fields from sub
```

**Canonical version** (one opener + per-param field readers):
```go
// openParamStream opens a SMM2-style param body (version + substream) and
// returns the substream reader + a recover. Callers panic on shape errors
// (caught by defer). No need for them to repeat the boilerplate.
func openParamStream(s *nex.Settings, body []byte) (sub *nex.StreamIn, bodyOK bool) {
    defer func() { recover() }()
    in := nex.NewStreamIn(body, s)
    _ = in.U8()
    return in.Substream(), true
}
```

Each `parseXxx` becomes:
```go
func parseXxx(s *nex.Settings, body []byte) (...) {
    sub, ok := openParamStream(s, body)
    if !ok { return zero, zero, false }
    // read fields from sub
}
```

Eliminates 8 `defer recover` blocks + 8 `U8 version` + 8 `Substream()`.

### 1.3. `search_courses_*` family — 9 handlers, same body shape

| Handler | Method | Common body |
|---|---|---|
| `smm2SearchCoursesLatest` | m=73 | parse → list courses → write list<CourseInfo> → write bool |
| `smm2SearchCoursesHot` | m=84 | same |
| `smm2SearchCoursesByMethod72` | m=72 | same |
| `smm2SearchCoursesLeaderboard` | m=58 | same + list<u32> ranks |
| `smm2SearchCoursesPostedBy` | m=74 | same |
| `smm2SearchCoursesPositiveRatedBy` | m=75 | same + per-user list |
| `smm2SearchCoursesPlayedBy` | m=76 | same + per-user list |
| `smm2SearchCoursesFirstClear` | m=80 | same + pagination |
| `smm2SearchCoursesBestTime` | m=81 | same + pagination |

Every one of these ends with:
```go
out := nex.NewStreamOut(s)
out.U32(uint32(len(list)))
for _, m := range list {
    if m == nil || !m.Ready { continue }
    out.Write(buildCourseInfo(s, m))
}
out.Bool(true) // or out.Bool(more) for paginated
return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
```

**Canonical version**:
```go
// writeCourseInfoListResponse emits a list<CourseInfo> + bool result envelope.
// Skips nil / not-Ready courses. result=true unless hasMore is set.
func writeCourseInfoListResponse(s *nex.Settings, list []*courseMeta) []byte {
    out := nex.NewStreamOut(s)
    out.U32(uint32(len(list)))
    for _, m := range list {
        if m == nil || !m.Ready { continue }
        out.Write(buildCourseInfo(s, m))
    }
    out.Bool(true) // or hasMore
    return out.Bytes()
}
```

And a paginated variant for m=80/81:
```go
// writeCourseInfoListResponsePaginated applies offset/size, returns body+more flag.
func writeCourseInfoListResponsePaginated(s *nex.Settings, list []*courseMeta, offset, size uint32) (body []byte, more bool) {
    // clamp, slice, buildCourseInfoListResponse, compute more
}
```

Each search handler shrinks from ~25 lines to ~10:
```go
func smm2SearchCoursesPositiveRatedBy(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
    s := conn.Settings
    pid, count := parseSearchCoursesByPIDParam(s, req.Body)
    if pid == 0 { pid = conn.PID }
    ids := profiles.coursesPositiveRated(pid)
    if count > 0 && uint32(len(ids)) > count { ids = ids[:count] }
    cs := make([]*courseMeta, 0, len(ids))
    for _, id := range ids { cs = append(cs, courses.get(id)) }
    body := writeCourseInfoListResponse(s, cs)
    log.Printf("[SMM2 Courses] search_courses_positive_rated_by(75) pid=%d target=%d count=%d -> %d courses\n", conn.PID, pid, count, len(cs))
    return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, body)
}
```

Eliminates ~150 lines of repeated envelope-writing across 9 handlers.

### 1.4. `search_users_by_course` family — already partially refactored

The 3 handlers (m=53, 54, 55) already share `parseSearchUsersByCourseParam`,
`applyCountHint`, and `writeUserInfoListResponse` (smm2_datastore.go:851-868).
**No change needed** — this is the model for the courses-side refactor.

### 1.5. `smm2GetUsers` vs `smm2GetUsersFromProfiles` — only one is live

`get_users(48)` is dispatched to `smm2GetUsersFromProfiles` (the dynamic
one). `smm2GetUsers` (smm2_profile.go:178) is dead code that uses the
captured template.

**Canonical version**: keep `smm2GetUsersFromProfiles`, **delete**
`smm2GetUsers` and the supporting `carveUserTemplates` /
`patchUserInfo` / `patchSyncProfile` if they're no longer reachable.
Verified: smm2_datastore.go:115 explicitly says "ALWAYS use the
registered-profile path". The template path is documented dead code
(smm2_datastore.go:117-122).

### 1.6. `syntheticUserInfoFromProfile` — 50 lines, god-function candidate

(smm2_users.go:825)

This single function:
- Resolves pseudo
- Reads 9 optional fields from the profile
- Writes 18 wire fields inline
- Is already 50 lines of mixed concerns

**Canonical version** (atomic pieces):
```go
// resolveUserInfoFields returns the "filled in" set of fields for one PID
// from a registered profile, with sensible defaults when r is nil.
func resolveUserInfoFields(pid uint64, r *registeredProfile) userInfoFields {
    // returns a struct holding name, country, region, play, maker, etc.
}

// writeUserInfo emits the full version-0 UserInfo wire format from a
// resolved userInfoFields + raw unk1/mii bytes.
func writeUserInfo(s *nex.Settings, pid uint64, f userInfoFields) []byte {
    // delegates to writeUserInfoHeader, writeUserInfoStats, writeUserInfoBadges, etc.
}
```

The two atomic functions together replace 50 lines with 2 calls per
handler. The "header / stats / badges" sub-pieces are each ~10 lines.

### 1.7. `parseSearchCoursesPostedByParam`'s ResultRange parsing — 2 places

The "ResultRange: [u8 ver][u32 length][u32 offset][u32 size]" shape is
parsed inline in `parseSearchCoursesPostedByParam` (m=74) and
`parseSearchCoursesFirstClearParam` (m=80, m=81). Two copies of the
4-line pattern.

**Canonical version**:
```go
// readResultRange reads [u8 ver][u32 length][u32 offset][u32 size] from sub
// and returns (offset, size). Always length=8 on SMM2 — we ignore the value.
func readResultRange(sub *nex.StreamIn) (offset, size uint32) {
    _ = sub.U8()
    _ = sub.U32() // length
    offset = sub.U32()
    size = sub.U32()
    return
}
```

### 1.8. `smm2EmptyBuilders` (smm2_datastore.go:29) — 15 inline closures

15 one-line `func(o *nex.StreamOut) { o.U32(0); ... }` closures for
methods 50, 51, 52, 56, 57, 71, 79, 82, 85, 86, 103, 147, 160, 162, 168.
Some have 1 field, some have 2, some have 3.

**Canonical version** (named, reusable, with comments):
```go
var smm2EmptyBuilders = map[uint32]func(*nex.StreamOut){
    50: writeEmptyUsers,           // list<UserInfo>
    51: writeEmptyUsers,           // ditto
    ...
}

// Then top-level:
func writeEmptyUsers(o *nex.StreamOut)  { o.U32(0) }
func writeEmptyUserRanks(o *nex.StreamOut)  { o.U32(0); o.U32(0) }
func writeEmptyCourses(o *nex.StreamOut)  { o.U32(0) }
func writeEmptyCourseRanks(o *nex.StreamOut)  { o.U32(0); o.U32(0); o.Bool(true) }
func writeEmptyWorldMaps(o *nex.StreamOut)  { o.U32(0); o.U32(0) }
```

The map shrinks from a wall of anonymous closures to 15 lines, and
each helper is named + has a docstring.

### 1.9. `sync_user_profile(49)` response — 2 implementations

`smm2_datastore.go:125-138` has two paths: a captured-template path
(`patchSyncProfile`) and a synthetic path (`syntheticSyncProfileResult`).
Only the template path is used if the capture is present, else the
synthetic fallback. The 9-field "SyncUserProfileResult" struct is
similar to but smaller than the 18-field UserInfo.

**Canonical version**: a single `writeSyncUserProfileResult(s, pid,
profile, fallbackName)` that picks the right path internally. The
caller doesn't need to know whether the template loaded.

### 1.10. `relationObjectReqGetInfo` builders — 1 helper, 1 inline copy

`writeRelationObjectReqGetInfo` (smm2_courses.go:160) is a helper.
`buildCourseInfo` (line ~245) inlines the same shape for its
`RelationObjectReqGetInfo` sub-struct. Two copies of the same wire
format.

**Canonical version**: have `buildCourseInfo` call
`writeRelationObjectReqGetInfo` instead of inlining.

## 2. Atomic function structure (target)

After the refactor, every wire-format field should have ONE atomic
writer. Top-level handlers compose them.

```text
// atomic writers (one field each, ≤ 5 lines each)
writeU8U32Map(out, m)                 // map<u8, u32> with len-prefix
writeU16U8List(out, list)             // list<{u16, u8}>
writeQBuffer(out, data)               // u32 length + bytes
writeStructV0(out, body)              // [u8 ver=0][u32 length][body] framing
writeBoolFalse3(out)                   // 3x Bool(false)
writeDateTimeNow(out)                  // DateTime(NowDateTime())
writeMakerCode(out, pid)               // String(makerCode(pid))
writeUserInfo(s, pid, fields)          // composes the 18-field UserInfo
writeCourseInfo(s, m)                  // composes the 20-field CourseInfo
writeCourseInfoList(s, list)           // list<CourseInfo> + bool
writeUserInfoList(s, pids)             // list<UserInfo> + bool
writeUserInfoListFromProfiles(s, pids) // same but reads from registry
writeEmptyList(out)                    // u32(0)
writeEmptyListBool(out)                // u32(0); bool(true)
writeEmptyListList(out)                // u32(0); u32(0)
writeEmptyListListBool(out)            // u32(0); u32(0); bool(true)
```

```text
// atomic parsers (one param shape each, ≤ 10 lines)
openParamStream(s, body)               // [u8 ver][u32 substream]
readResultRange(sub)                   // offset, size
parseSearchByPIDParam(s, body)         // pid, count
parseSearchByPaginationParam(s, body)  // pid, offset, size
parseSearchByUserIDParam(s, body)      // pid, count (count-or-cap)
parseRateObjectParam(s, body)          // dataID, slot, ratingValue
parsePreparePostCourseParam(s, body)   // name, desc, tags, gameStyle, ...
parseRegisterUserParam(s, body)        // username, unk1, miiData, region, ...
```

```text
// atomic mappers (one stat kind each)
playStatsToMap(p)                      // 4 keys
multiplayerStatsToMap(m)               // 5 keys
makerStatsToMap(m)                     // 1+ verified keys
coursePlayStatsToMap(c)                // 4 keys
courseRatingsToMap(c)                   // 3 keys
```

## 3. God-function candidates (will be broken up)

| Function | File:line | Current lines | Target lines | Why |
|---|---|---|---|---|
| `syntheticUserInfoFromProfile` | smm2_users.go:825 | 50 | 5 (delegates) | 9 optional fields + 18 wire fields = too many concerns |
| `buildCourseInfo` | smm2_courses.go:211 | ~90 | ~40 (delegates) | 20 fields inline; should compose write* helpers |
| `smm2RateObject` | smm2_courses.go:938 | ~60 | ~30 | Parse + 3 side-effects + aggregate + response = too much |
| `smm2PreparePostObjectCourse` | smm2_objects.go:84 | ~50 | ~20 | Parse + 4 list writers + headers = too much |
| `smm2PrepareRelationUpload` | smm2_objects.go:205 | ~50 | ~20 | Same shape as above |
| `smm2GetUsersFromProfiles` | smm2_users.go:928 | ~40 | ~15 | Loop + writeUserInfoList is the only concern |
| `smm2PostPlayResult` | smm2_datastore.go:678 | ~100 | ~30 | 51-byte body parse + 3 side-effects + response |

These keep their public function name and signature — only the body is
broken up.

## 4. Other improvements (out of refactor scope but worth noting)

1. **Replace `fmt.Printf` with structured logger** — every handler has
   `fmt.Printf` with a different format string. A `logRMC(method,
   pid, ...)` helper would unify. **Out of scope** — touches every
   file, not a duplication.
2. **Replace magic numbers (74, 75, 48, 0x73, 60078) with named consts** —
   already partially done (smm2_storage.go:39-49). The "method IDs"
   inside `smm2DataStoreHandler` could be a `const` block at the top
   of `smm2_datastore.go`. **Low priority** — the comments already
   name each one.
3. **Document the `option` field** in `parseSearchUsersByCourseParam` —
   currently consumed but never used. The kinnay docstring is
   silent. **Low priority** — wait for real capture.
4. **Consistent error handling in parseXxx** — some return
   `(values, false)`, others return zero values silently. A single
   "return bool ok" pattern would help. **Low priority** — current
   code works.
5. **`profileRegistry` operations are not atomic across struct fields** —
   `register` overwrites everything in one critical section, but
   `recordPlay` and `recordRate` mutate maps outside the main
   `register` lock. Reading after write is safe (same mutex), but
   cross-field invariants (e.g. "PlayedCourses length matches
   Play.Plays") aren't enforced. **Out of refactor scope.**
6. **`smm2GetDeathPositions` is the only handler with a meaningful
   parse step** — others either have no params or skip them. A
   `parseDeathPositionsFromRelation(dataID)` helper that reads the
   replay.bin would be the start of a real feature. **Out of scope**
   — needs the replay parser first.

## 5. Refactor execution plan

**Single PR / single commit per phase.** Each phase ends with
`go build` clean and `git diff` showing no behaviour change (we can
spot-check by hand on a couple of methods).

### Phase 1 — Helpers only, no handler changes (lowest risk)
Add new helpers (`writeCourseInfoListResponse`,
`openParamStream`, `readResultRange`, `buildU8U32MapIfAny`, etc.).
Don't change any handler. Compile-check. Commit.

### Phase 2 — Switch handlers to new helpers
Refactor `search_courses_by_X` family to use
`writeCourseInfoListResponse`. Refactor `parseXxx` to use
`openParamStream`. Refactor `buildXxxMap` family. Refactor
`smm2EmptyBuilders` to use named helpers. Compile-check after each.
Commit per group.

### Phase 3 — God-function decomposition
Break up `syntheticUserInfoFromProfile`, `buildCourseInfo`, the
relation-upload prepare handlers. These are bigger changes; do them
one function at a time, with a commit per function.

### Phase 4 — Cleanup
Delete dead code (`smm2GetUsers`, `patchUserInfo`, `patchSyncProfile`
if the synthetic path is now the only path). Final pass on docstrings.

## 6. What I will NOT do

- Change any wire-format byte — every response stays byte-exact.
- Change the JSON shape of `profiles.json` / `courses.json` /
  `comments.json` (would lose existing data).
- Touch the public dispatcher in `smm2DataStoreHandler()` — the
  case-statement structure is the most readable form of "which
  method does what", and changes there ripple into 30+ handlers.
- Add new functionality — the refactor is purely structural. New
  features (BestTimes, last_active, etc.) are tracked in
  [SCHEMA.md](./SCHEMA.md).
