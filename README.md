# SMM2 Private Server (`super-mario-maker-2`)

> **Status:** pre-release (`v0.1/audit-docs`). This is a private NEX server for
> Super Mario Maker 2, built on the [Nextendo NEX stack](https://github.com/NextendoNetwork/nextendo-nex).
> It only serves Nextendo accounts (no anonymous, no Nintendo Network IDs).
>
> For a per-method handler status of the DataStore (0x73) protocol see **[STATE.md](./STATE.md)**.

## What this is

A Go implementation of the NEX services Super Mario Maker 2 needs at runtime:

- **Auth** (protocol `0x6E`, port `443`) — TicketGranting with Nextendo gates
  (account required, e-mail verified, single active device).
- **Secure** (protocol `0x6A`, port `60007`) — SecureConnection + matchmaking +
  NAT-traversal + DataStore + Utility.
- **Storage** (HTTP, port `60078`) — own object store for course blobs and
  relation data (thumbnails / clear-check), avoiding Nintendo's S3/CloudFront.

The DataStore (0x73) protocol is the meat of the server: course upload,
search, list, comment, get-users, sync-profile, rate, touch, play-result, etc.
A full method inventory is in [STATE.md](./STATE.md).

## Build & run

```bash
go build -o smm2-server .
./smm2-server
```

## What works

End-to-end flows validated against a real SMM2 capture. Each feature is mapped
to the NEX method(s) that implement it.

| Status | Meaning |
|---|---|
| ✅ | Working as captured. |
| 🟡 | Working but with a known caveat (see note). |
| ❌ | Handler is wired but does **not** work in the client (verified by real testing). |
| ⚪ | Not implemented / stub / no data to return. |

### Course World

| Feature | NEX method(s) | Status |
|---|---|---|
| Browse "Hot Courses" | m=84 `search_courses_hot` | ✅ |
| Browse "New Courses" | m=73 `search_courses_latest` | ✅ |
| Course detail (full metadata) | m=70 `get_courses` (CourseInfo) | ✅ |
| Upload course (level + relations) | m=66, m=68, m=132, m=133 + HTTP | ✅ |
| Play course (download + run) | m=25 (HTTP GET) + m=24/26 (telemetry) | ✅ |
| Submit score (clear/death/attempts) | m=96 `post_play_result`, m=22 `touch_object` | ✅ |
| **Player list** — "people who played this course" | m=53 `search_users_played_course` | ✅ |
| **Cleared-by list** — "people who first-cleared this course" | m=54 `search_users_cleared_course` | ✅ |
| **Liked-by list** — "people who liked/hearted this course" | m=55 `search_users_positive_rated_course` | ✅ |
| Rate like / heart / boo | m=15 `rate_object` | ❌ — wired, no anda en cliente |
| Comments list (per course) | m=94 `search_comments_in_order`, m=95 `search_comments` | ❌ — wired, no anda en cliente |
| Download course blob | m=25 `prepare_get_object` + HTTP | ✅ |
| World record display | m=70 (CourseTimeStats substruct) | 🟡 — placeholder values, no replay parser yet |
| Clear rate / play stats | m=70 (CourseInfo.play_stats), m=96 (feeds them) | ✅ |
| Thumbnails (1-screen, 3-screen) | m=132, m=133, m=134 | ✅ |
| Course ID display post-upload | m=70 (CourseInfo.code) | ✅ |
| Get NG course notification | m=129 | ✅ |

### Maker Profile

| Feature | NEX method(s) | Status |
|---|---|---|
| Overview (own profile, Mii, country, stats) | m=49 `sync_user_profile`, m=48 `get_users` | ✅ |
| "My Courses" / Upload Courses tab | m=74 `search_courses_posted_by` | ✅ |
| "Courses I Played" tab | m=76 `search_courses_played_by` | ✅ |
| "Courses I Liked / Hearted" tab | m=75 `search_courses_positive_rated_by` | ❌ — no funciona |
| "First to Clear" tab | m=80 `search_courses_first_clear` | ✅ |
| "My Best Time" tab | m=81 `search_courses_best_time` | 🟡 — same data source as m=80 (no per-player best-time parser) |

### Other wired methods

- `m=103` `get_death_positions` — list is always empty (no replay parser);
  the "View Deaths" button renders with no entries instead of erroring.
- `m=154` `get_event_course_status` — returns a neutral status (no event
  courses running).
- World maps (m=160, m=162) — empty lists. We have no world map data.

## Not implemented

User-facing features that are not implemented. The client either sees an
empty/blank state, an error, or the tab doesn't render at all. Stubbed
methods return the empty-list envelope (U32(0) + bool(true)) so the
client renders "no data" without erroring; methods not in the dispatcher
fall through to `DataStore::NotFound` (0x80690004) which can abort the
surrounding flow.

### Network play (multiplayer)

The whole NEX matchmaking stack (NEX SecureServer `0x6A` session methods)
is not implemented — no session management, no matchmaking, no
versus/co-op lobby. The per-profile `multiplayer_stats` schema exists
(see `registeredProfile` in `smm2_users.go`) but no event handler
populates it, so the "versus wins / coop wins" stats on a profile are
always zero.

### Leaderboards

Ranking searches return empty (stub in `smm2EmptyBuilders`). The
"Course Markers" / leaderboard tabs render with no entries:

- m=50 `search_users_user_point` — user point ranking
- m=51 `search_users_endless_mode` — endless-mode user ranking
- m=52 `search_users_battle_mode` — battle-mode user ranking
- m=56 `search_users_followee` — followed-players ranking
- m=57 `search_users_clear_ranking` — clear-count ranking
- m=71 `point_ranking` — courses + ranks + result (the "Course Markers" data)
- m=147 `search_users_official` (undocumented) — stub
- m=168 `search_users_followee_v2` (undocumented) — stub

Note: `m=58` `search_courses_leaderboard` IS wired (returns the
top-by-hotness courses), but without the surrounding ranking data the
"Course Markers" tab looks empty.

### ID search

Looking up a course by its shareable 4-segment code
(`ABCD-1234-EFGH-5678`). No `code → data_id` lookup method is
implemented. The in-game search bar returns no results; this also breaks
"join by ID" flows (where the client pastes a code and expects to be
navigated to the course).

### Endless challenge

- m=79 `search_courses_endless_mode` — stub
- m=85 `get_courses_event` — stub
- m=86 `search_courses_event` — stub
- The per-profile `EndlessHighScores` map (per-difficulty) exists in the
  schema but no event handler populates it; the "Endless Challenge"
  score on a profile is always zero.

### Super worlds

- m=160 `get_world_map` — stub, returns empty list
- m=162 `search_world_map_pick_up` — stub, returns empty list
- m=155, 156, 157 (undocumented, super-worlds-related per call sequence
  in measured traces) — NotFound; not handled in the dispatcher. If the
  client calls them the response is `DataStore::NotFound` which may abort
  the surrounding flow.

### Ninji speedruns

- m=154 `get_event_course_status` returns a neutral status ("no event
  course active"). The client sees "no ninji event running right now"
  rather than an error.
- No event-course upload / download flow on the server (no ninji data on
  disk). The in-game "Ninji Speedrun" tab renders but no courses are
  available.
- No methods for posting a ninji replay or fetching the leaderboard
  specific to the active event.

The binary expects `cert.pem` + `key.pem` in the working directory (or via
`CERT_FILE` / `KEY_FILE` env vars). It also needs a reachable `nextendo-account`
service (default `NEXTENDO_ACCOUNT_URL=http://nextendo-account:8080`).

### Environment variables

| Var | Default | Meaning |
|---|---|---|
| `NEXTENDO_HOST` | `127.0.0.1` | Public hostname (used in storage URLs). |
| `NEXTENDO_ACCOUNT_URL` | `http://nextendo-account:8080` | Account service for gates + pseudo resolution. |
| `NEXTENDO_INTERNAL_KEY` | (none) | Shared secret for `/internal/online-check`. |
| `NEXTENDO_REQUIRE_ACCOUNT` | (unset) | Set to `1` to reject logins without a Nextendo token. |
| `AUTH_PORT` | `443` | Auth server port. |
| `SECURE_PORT` | `60007` | Secure server port. |
| `STORAGE_PORT` | `60078` | Object storage HTTP port. |
| `STORAGE_DIR` | `smm2_objects` | Local dir for blob/relation files. |
| `STORAGE_URL` | derived from `NEXTENDO_HOST:STORAGE_PORT` | Public base for blob URLs. |
| `STORAGE_CA_FILE` | (none) | If set, returned as `root_ca_cert` for the console to trust our TLS. |
| `SMM2_CAPTURE` | (unset) | If set to a file path, dumps all RMC frames to that file (debug). |
| `SMM2_REPLAY_DIR` | `measured` | Override the directory of pre-captured response bodies. |

### Persistent data

```
<STORAGE_DIR>/
├── courses.json        # courseStore — uploaded courses + stats
├── profiles.json       # profileRegistry — maker profiles (from RegisterUser 47)
├── comments.json       # commentStore — per-course comment lists
└── <data_id>/
    ├── level.bin       # the course blob itself
    ├── one_screen_thumbnail.bmp
    ├── three_screen_thumbnail.bmp
    └── ...
```

## Architecture

```
Switch / emulator
   │  (TLS, SNI = g22306d00-XXX.srv.nintendo.net, etc.)
   ▼
sni-router (:443)         ←── routes by SNI to the right NEX server
   │
   ├──► SMM2 auth    (:443)  → TicketGranting (LoginEx + gates)
   │
   ├──► SMM2 secure  (:60007) → SecureConnection + DataStore + Utility
   │
   └──► SMM2 storage (:60078) → HTTP for blobs/relations
                                   ▲
                                   │  (URL handed back by m=25 / m=132)
                                   │
                                 client uploads/downloads level.bin here
```

`sni-router` is a separate Go binary in the parent checkout
(`../sni-router/main.go`). It peeks the SNI from the TLS ClientHello and
forwards the raw stream to the right backend. SMM2's identifiers in the SNI
are the five `gXXXXXXX` build/region codes:

| SNI prefix | Game |
|---|---|
| `g210a9200` | SMM2 (US v1) |
| `g22306d00` | SMM2 (this server emulates this one — see URLs in `.../10.ngs_lp1_22306d00_datastore/...`) |
| `g83110300` | SMM2 (EU) |
| `g83110400` | SMM2 (EU newer) |
| `g8930141a` | SMM2 (latest) |

These are NOT eShop title IDs (which are 16 hex chars like `0100F1C00006DC000`)
— they are NEX-internal game-version identifiers used in the SNI hostname.

### Cross-service

- **`../sni-router`** — TLS SNI passthrough. Routes by game ID. Without it
  the Switch can't reach this server on `:443`.
- **`../nextendo-account`** — owns gates, pseudo resolution, NSA ↔ PID
  resolution, e-mail-verified state, single-active-device state. This server
  calls it over HTTP (`/api/names`, `/api/nsa`, `/internal/online-check`).
- **`../nextendo-nex`** — the NEX protocol library. PRUDP-Lite, RMC, stream
  encoding. This server imports it as `nex "github.com/NextendoNetwork/nextendo-nex"`.

## Code map

| File | Purpose |
|---|---|
| `main.go` | Auth + Secure server entry point. LoginEx / resolveUser / Nextendo gates. |
| `gates.go` | Nextendo gates (online check, NSA resolution). |
| `smm2_datastore.go` | 0x73 DataStore dispatcher. Per-method routing + `smm2EmptyBuilders`. |
| `smm2_users.go` | `profileRegistry` (m=47, 48, 49, 53, 54, 55). |
| `smm2_profile.go` | UserInfo templating, pseudo resolution, `pseudoOr`, `makerCode`. |
| `smm2_courses.go` | `buildCourseInfo` + `smm2GetCourses` + all `search_courses_*` handlers. |
| `smm2_storage.go` | `courseStore` + HTTP object server + relation download. |
| `smm2_objects.go` | 0x73 object methods (m=24/25/26/60/61/66/68/69/132/133/134). |
| `smm2_comments.go` | `commentStore` + m=94/95 search handlers. |
| `smm2_capture.go` | Optional RMC capture wrapper (only when `SMM2_CAPTURE` is set). |
| `init_replay.go` | Loads `measured/resp_0x73_mXX.bin` (legacy — currently a no-op). |
| `type8.go` | PRUDP-Lite type-8 message dispatch. |

## What this server does NOT do

- It does NOT validate uploaded course blobs against the SMM2 binary format
  (no parser, only metadata + raw storage). Nintendo's official client would
  not accept an invalid blob; we don't catch that on the server side.
- It does NOT sync with Nintendo's NEX. This is a standalone private server.
- It does NOT talk to BOSS (SpotPass) or any Nintendo telemetry.
- It does NOT implement event courses (m=154 always returns a neutral status).

## What is known to be missing

See [STATE.md](./STATE.md) for the full inventory. The P0/P1/P2/P3 prioritisation
is at the bottom of that file. Highlights:

- **Death positions** (m=103) — list is always empty, no replay parser.
- **World maps** (m=160, m=162) — no implementation, empty lists.
- **Endless-mode high scores** (per profile) — schema exists, no events feed it.
- **Best-time leaderboard** (m=81) — falls back to the same data source as
  m=80; no per-player best-time parser yet.
- **Comment posting** — no documented RMC for `add_comment`; the seed comments
  in `smm2_comments.go` are the only ones.

## Verifying a method

For a per-method capture/diff flow:

```bash
SMM2_CAPTURE=measured_live.txt ./smm2-server
# … do your thing in the emulator/Switch …
python3 ../tools/parse_capture.py measured_live.txt
```

The capture is the source of truth for which bytes the client expects.
**Real SMM2 hex capture is required to accept a fix for a wire-format bug** —
guesses stacked on guesses (captured blobs of unknown provenance) have been
the source of several silent-corruption bugs in the past.

## Contributing notes

- Don't commit `measured_live.txt` or anything under `smm2_objects/` —
  course data is private to each player. Use `SMM2_CAPTURE` for local
  captures, keep them out of git.
- Long-lived branches per task (`debug/<symptom>`, `v0.1/<prep>`, etc.).
  Avoid squashing or force-pushing shared branches.
- Comments in code are ES / EN / FR depending on who wrote them. Don't
  translate — keep the original voice for git blame to keep working.
- For a new method: confirm the wire format against a real capture OR
  against `NintendoClients/datastore_smm2.py` (kinnay's reverse-engineered
  reference), then write the handler in the appropriate `smm2_*.go` file.
  Add an entry to [STATE.md](./STATE.md) with the status.
