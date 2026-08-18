# SMM2 Server — Data Schema & Persistence Layout

> On-disk and in-memory shapes for the three persisted stores:
> `registeredProfile` (per-PID), `courseMeta` (per-course), and `comment`
> (per-comment). For per-method handler status see [STATE.md](./STATE.md);
> for the "what works / doesn't work" feature inventory see [README.md](./README.md).

## Where things live

| Store | In-memory | On disk | Auto-loaded |
|---|---|---|---|
| Profiles | `var profiles *profileRegistry` (smm2_users.go) | `<STORAGE_DIR>/profiles.json` | `init()` at startup |
| Courses | `var courses *courseStore` (smm2_storage.go) | `<STORAGE_DIR>/courses.json` | `load()` at startup |
| Comments | `var comments *commentStore` (smm2_comments.go) | `<STORAGE_DIR>/comments.json` | `init()` at startup |
| Objects (blobs) | filesystem | `<STORAGE_DIR>/<data_id>/level.bin`, `*.bmp` | n/a |

Default `<STORAGE_DIR>` = `smm2_objects` (smm2_storage.go:46).

## 1. Profile — `registeredProfile` (smm2_users.go:103)

### Current JSON shape

```json
{
  "pid": 1800000001,
  "username": "Beer2",
  "mii_data_hex": "418dbd47b0b9e0e653ef92fb8a42006500650072003200...",
  "unk1_hex": "0000000000000000",
  "region_id": 1,
  "country_code": "AR",
  "pseudo_device_id": "abcdef0123456789abcdef0123456789",
  "registered_at": 1787091234,

  "play_stats":     { "plays": 12, "clears": 3, "attempts": 4, "deaths": 9 },
  "maker_stats":    { "uploaded": 5, "plays_received": 12, "clears_received": 3,
                      "attempts_received": 4, "deaths_received": 9,
                      "likes_received": 2, "hearts_received": 1, "boos_received": 0,
                      "maker_points": 0 },
  "multiplayer_stats": { "score": 0, "versus_plays": 0, "versus_wins": 0,
                         "coop_plays": 0, "coop_wins": 0 },

  "endless_high_scores": { },
  "badges": [ ],

  "unk7": null, "unk8": null, "unk9": null,
  "unk10": false, "unk11": 0, "unk12": false,
  "unk14": "", "unk15": null, "unk16": false,

  "uploaded_count": 5,
  "uploaded_ids": [1000, 1001, 1002, 1003, 1004],

  "played_courses":  { "1003": true, "1004": true },
  "rated_courses":   { "1003": 1 },
  "cleared_courses": { "1003": true },
  "first_cleared":   { "1003": true }
}
```

### Fields present vs. kinnay's `UserInfo` (datastore_smm2.py)

Mapping our JSON fields → what the wire-format `UserInfo` carries:

| Our field | UserInfo field | Notes |
|---|---|---|
| `pid` | `pid` (u64) | ✓ |
| — | `code` (string, 9-char maker code) | ❌ **derived at write-time** via `makerCode(pid)` (smm2_profile.go:76), NOT stored. |
| `username` | `name` (string) | ✓ |
| `unk1_hex` | `unk1` UnknownStruct1 (opaque body) | ✓ as opaque hex; sub-fields NOT unpacked |
| `mii_data_hex` | `unk2` (qBuffer) | ✓ |
| `country_code` | `country_code` (string) | ✓ |
| `region_id` | `unk3` (u8) | ✓ |
| — | `last_active` (DateTime = u64) | ❌ **emitted as `NowDateTime()` at write-time**, not stored per profile. |
| — | `unk4`, `unk5`, `unk6` (3× bool) | ❌ **always emitted as `false`**, not stored. |
| `play_stats` | `play_stats` (Map<u8,u32>) | ✓ flattened to JSON object |
| `maker_stats` | `maker_stats` (Map<u8,u32>) | ✓ flattened to JSON object |
| `endless_high_scores` | `endless_challenge_high_scores` (Map<u8,u32>) | ✓ |
| `multiplayer_stats` | `multiplayer_stats` (Map<u8,u32>) | ✓ flattened |
| `unk7` | `unk7` (Map<u8,u32>) | ✓ |
| `badges` | `badges` (List<BadgeInfo>) | ✓ |
| `unk8` | `unk8` (Map<u8,u32>) | ✓ |
| `unk9` | `unk9` (Map<u8,u32>) | ✓ |
| `unk10/11/12` | `unk10/11/12` (UserInfo rev≥1) | ✓ fields present but always default |
| `unk14/15/16` | `unk14/15/16` (UserInfo rev≥3) | ✓ fields present but always default |

### What's missing / could be improved

1. **`code` (maker code) not stored** — currently derived each time from PID via
   `makerCode(pid)`. Risk: if `makerCode` is ever changed (e.g. alphabet
   upgrade), old profiles get a different code on the wire. Storing it on
   first registration would freeze it. **Low priority** — the derived
   function is deterministic, but a stored field would be more
   "Nintendo-faithful".
2. **`last_active` (DateTime) not stored** — emitted as `NowDateTime()` at
   write-time, so every read of the same profile returns the current time,
   not the actual last-active. If the client compares to detect stale
   profiles this is a bug. **Medium priority** — needs a writer (probably
   on m=104 play_event, or piggyback on sync_user_profile).
3. **`unk4/5/6` (3 bools) always `false`** — should be writable per profile
   (e.g. a `preferences` block: opt-out-of-events, etc.). Currently no
   client UI surfaces them, but they're a real UserInfo field. **Low
   priority** — no client impact yet.
4. **`RegisteredAt` not surfaced on the wire** — stored but not emitted.
   UserInfo has no field for it, so it's a server-side audit field only.
5. **No aggregated counters derived on read** — e.g. `len(played_courses)`,
   `len(cleared_courses)`, `len(first_cleared)` are computable from the
   maps but we always re-iterate at query time. Could be cached as
   `played_count`, `cleared_count`, etc. for O(1) lookups. **Low priority**
   — current data scale doesn't need it.
6. **No account-level cross-server fields** — SMM2 has account-wide
   fields (region, account-creation date, parental-control, etc.) that
   are server-side, not profile-side. Currently the userInfo "country"
   comes from RegisterUser(47) but the true account country lives on
   `nextendo-account` and isn't stored on the profile. **Out of scope for
   this struct** — it's an account concern, not a profile concern.

### Sub-struct audit

| Sub-struct | Fields | All written? |
|---|---|---|
| `playStats` | Plays, Clears, Attempts, Deaths | ❌ none (no handler wires them; m=96 only feeds the course's play_stats) |
| `makerStats` | Uploaded, Plays/Clears/Attempts/DeathsReceived, Likes/Hearts/Boos Received, MakerPoints | 🟡 partial — Uploaded (via reconcileFromCatalog), Likes/Hearts/Boos (via m=15). Others not wired. |
| `multiplayerStats` | Score, VersusPlays/Wins, CoopPlays/Wins | ❌ none (no network play at all) |
| `badgeInfo` | Unk1, Unk2 | ❌ none (no badge-awarding handler) |
| `endlessHighScores` | per-difficulty best | ❌ none (no event feeder) |

## 2. Course — `courseMeta` (smm2_storage.go:53)

### Current JSON shape

```json
{
  "data_id": 1003,
  "owner_pid": 1800000001,
  "name": "test 1",
  "description": "1 test",
  "data_type": 1,
  "meta_hex": "0000000000000000",
  "tags": [1, 0, 0, 0],
  "game_style": 1,
  "course_theme": 1,
  "difficulty": 0,
  "size": 12345,
  "ready": true,
  "code": "32XHG053K",
  "created_at": 1787091234,

  "play_count":    12,
  "clear_count":    3,
  "attempt_count":  4,
  "death_count":    9,
  "like_count":     2,
  "heart_count":    1,
  "boos_count":     0,
  "rating_initial": { "0": 0, "1": 1, "2": 0 },
  "comment_counts": { "0": 5, "1": 2 },

  "first_completion_pid":  1800000001,
  "world_record_holder_pid": 1800000001,
  "world_record_frames":   1
}
```

### Fields present vs. kinnay's `CourseInfo` (datastore_smm2.py)

| Our field | CourseInfo field | Notes |
|---|---|---|
| `data_id` | `data_id` (u64) | ✓ |
| `owner_pid` | `owner_pid` (u64) | ✓ |
| `name` | `name` (string) | ✓ |
| `description` | `description` (string) | ✓ |
| `data_type` | `data_type` (u16) | ✓ |
| `meta_hex` | `unk1` (buffer — meta_binary header) | ✓ as opaque hex |
| `game_style` | `unk2` (u8) | ✓ |
| `course_theme` | `unk3` (u8) | ✓ |
| `difficulty` | `unk4` (u8) | ✓ |
| — | `unk5` (bool) | ❌ **not stored**; emitted as `true` always at wire time |
| `code` | `unk6` (string — SMM2 shareable code) | ✓ |
| — | `unk7` (DateTime — created_at?) | ❌ **not stored**; need to verify the wire field is creation time. Likely bug. |
| `first_completion_pid` + `world_record_holder_pid` + `world_record_frames` | `unk8` (CourseTimeStats sub-struct) | ✓ flattened |
| `size` | `unk9` (u64) | ✓ |
| `play_count` etc. | `unk10/11/12/13` (u32) | ✓ |
| `like_count`, `heart_count`, `boos_count` | `unk14/15/16` (u32) | ✓ |
| — | `unk17` (RelationObjectReqGetInfo) | ❌ **not stored**; computed live from filesystem + URL builder |
| `rating_initial` | `unk18` (Map<u8,u32> — ratings.initial_value) | ✓ |
| `comment_counts` | `unk19` (Map<u8,u32> — comment_stats) | ✓ |
| — | `unk20` (Map<u8,u32> — play_stats) | ❌ **not stored as a Map**; we have play_count/clear_count/etc. as scalars. May or may not match the wire shape. |

### What's missing / could be improved

1. **`unk5` (bool) not stored** — emitted as `true` always. Probably
   "featured" or "official" flag; if a real flag is ever needed it would
   have to be stored. **Low priority** — likely a Nintendo-side flag we
   never set.
2. **`unk7` (DateTime) not stored** — this is *probably* the upload time /
   created time. We have `created_at` (unix seconds) and we emit
   `DateTime(0)` at wire time. **Medium-high priority** — likely a real
   bug if the client shows upload date.
3. **`unk17` (RelationObjectReqGetInfo) not stored** — we build it live
   from `thumbURLsForCourse(m)`. Reasonable, but if the relation
   metadata ever changes per course (e.g. different size, different
   `data_type`), we'd need a per-course override. **Low priority** — the
   live builder works.
4. **`unk20` (play_stats Map<u8,u32>) stored as scalars** — wire
   encodes a `Map<u8,u32>` for `play_stats`, but we have
   `play_count`/`clear_count`/`attempt_count`/`death_count` as four
   u32s. As long as the keys map 0=play, 1=clear, 2=attempt, 3=death
   (and the kinnay doc says so), the on-wire encoder flattens them via
   `buildCoursePlayStatsMap`. **Looks correct** but the JSON shape
   doesn't match the wire shape — could be a maintenance trap if keys
   change. **Low priority** — refactor would be cosmetic.
5. **No per-player best time** — `world_record_frames` is one global
   value. SMM2's CourseTimeStats in the wire has `world_record_holder`
   and we store the PID, but no `pid → frames` map for per-player
   bests. Adding a `BestTimes map[uint64]uint32` would unlock real
   "my best time on this course" data. **Medium priority** — needed
   to make m=81 `search_courses_best_time` non-degenerate.
6. **No unique-player counts** — `play_count` is total plays, not
   unique players. For a "X players have played this course" display
   we'd need `unique_player_count uint32`. **Low priority** — no
   surfaced UI yet.
7. **No clear-rate cache** — `clear_count / play_count` is computed at
   every read. Could be cached. **Trivial.**
8. **No last-played / last-cleared timestamp** — would need
   `last_played_at int64`, `last_cleared_at int64` (m=96 could write).
   **Low priority** — no surfaced UI yet.
9. **No upload-source tracking** — no `source` field
   (Switch/Ryujinx/iOS-Android). Could be useful for "courses I
   uploaded from a CFW". **Trivial / low priority.**

### Sub-struct audit

| Sub-struct | Fields | All written? |
|---|---|---|
| `CourseTimeStats` | first_completion_pid, world_record_holder_pid, world_record_frames | 🟡 set on first clear via setCourseTimes; never updated thereafter |
| Per-course `comment_stats` | per slot counts | ❌ never written (no comment-post handler) |
| Per-course `play_stats` Map | u8 key → u32 count | ✓ via buildCoursePlayStatsMap (derived from scalars) |
| Per-course `ratings` Map | u8 slot → u32 count | 🟡 partial (LikeCount/HeartCount/BoosCount, not the full slot-based map) |

## 3. Comment — `comment` (smm2_comments.go:134)

### Current JSON shape

```json
{
  "comment_id": 1,
  "course_id": 1003,
  "author_pid": 1800000001,
  "author_name": "Beer2",
  "body": "Nice level!",
  "face_id": 0,
  "picture_url": "",
  "created_at": 1787091234
}
```

### Fields present vs. kinnay's `CommentInfo` (datastore_smm2.py:2032)

| Our field | CommentInfo field | Notes |
|---|---|---|
| `comment_id` | `comment_id` (u64) | ✓ |
| `body` | `body` (string) | ✓ |
| `typeOrFace` (synthesised) | `type` (u8) | 🟡 emitted as our `face_id`; kinnay separates `type` (post/emoji) from `face_id` (Mii face). **Bug-risk.** |
| `author_pid` | `author_pid` (u64) | ✓ |
| `author_name` | `author_nickname` (string) | ✓ |
| `created_at` | `created_at` (DateTime) | ✓ |
| `picture_url` (joined into `comment_picture` sub-struct) | `comment_picture` (sub-struct) | ✓ |
| — | `unk16` (u16) | ❌ emitted as `0` always |
| — | `unk17` (u8) | ❌ emitted as `0` always |
| — | `good` (u32 — likes?) | ❌ **not stored**, emitted as `0` — likely a real "this comment was helpful" count |
| — | `bad` (u32 — dislikes?) | ❌ **not stored**, emitted as `0` |
| — | `unk20/21/22/23` | ❌ emitted as `0` — undocumented fields |

### What's missing / could be improved

1. **`type` vs `face_id` conflation** — we store `face_id` (0..22) and
   emit it in the slot kinnay calls `type`. May not be a real bug
   (Nintendo's doc isn't fully public) but worth a closer look against
   a real capture. **Medium priority.**
2. **Comment likes/dislikes (`good`/`bad`)** — kinnay indexes these;
   we don't store them. No UI to react on comments yet so the
   client likely doesn't send them. **Low priority.**
3. **No edit/delete support** — there's no RMC for `edit_comment` or
   `delete_comment` (per kinnay's index 1-49, 53-95, 103, 131, 134,
   153-157, 160-162), and no `edited_at` field on the struct.
   **Out of scope** — no client surfaces this.
4. **Comments are seeded at startup** (in `seedDefaults`) but there's
   no path to add a new one. The "add comment" flow would need either
   a new method or to hijack an existing one.

## 4. Storage layout

```
<STORAGE_DIR>/
├── profiles.json       # ProfileRegistry dump (loaded on init)
├── courses.json        # CourseStore dump (loaded on load())
├── comments.json       # CommentStore dump (loaded on init)
└── <data_id>/          # per-course files
    ├── level.bin       # the course blob itself
    ├── one_screen_thumbnail.bmp
    ├── three_screen_thumbnail.bmp
    └── (any other relation types the client uploaded)
```

The blob files are not in JSON — they're raw bytes keyed by `data_id`.
The metadata in `courses.json` is what links a `data_id` to its on-disk
path and to the wire format (CourseInfo).

## 5. Cross-references to handlers

For each missing field above, the handler that should be writing it is:

| Field | Handler that should write it | Current state |
|---|---|---|
| `last_active` | m=104 (play_event) or m=49 (sync_user_profile) | ❌ not wired |
| `play_stats.Play/Clears/Attempts/Deaths` | m=96 (post_play_result) | 🟡 only on the course's `play_stats`, not on the profile |
| `multiplayer_stats.*` | (no network play yet) | ❌ no handler |
| `EndlessHighScores` | m=86 (search_courses_event)?? or undocumented event | ❌ no handler |
| `Badges` | undocumented award handler | ❌ no handler |
| `Course.unk7` (DateTime) | m=66/68 (prepare/complete post object) | ❌ not written |
| `Course.unk20` (play_stats map) | (we have scalars; map is derived) | 🟡 derived but JSON shape doesn't match |
| `Course.BestTimes` (per-player) | m=15?? or replay parser | ❌ no parser |
| `Comment.good/bad` | (no client UI) | ❌ no handler |

## 6. What is intentionally NOT in the JSON

These are computed at wire-emit time, not stored, to keep the JSON
minimal and the storage layer stateless:

- `Course.unk17` (RelationObjectReqGetInfo) — built by
  `thumbURLsForCourse` from the filesystem layout + the configured
  storage URL.
- `UserInfo.code` (maker code) — derived by `makerCode(pid)`.
- `UserInfo.last_active` — emitted as `NowDateTime()` (this is the
  bug noted in the Profile section).
- `UserInfo.unk4/5/6` — emitted as `false`.
- `UserInfo.version` / `UserInfo.length` framing — added by
  `frameStruct(s, 0, ...)` at emit time.
