# SMM2 DataStore Server — Estado actual de los métodos

> Snapshot del branch `v0.1/audit-docs`. Esta tabla refleja qué respondió la auditoría
> de los métodos del protocolo DataStore SMM2 (NEX 0x73). Para detalles de cada handler
> ver los docstrings de las funciones en `smm2_*.go`.

## Leyenda

- ✅ **Wired + tested**: handler real, validado contra captura SMM2 real.
- 🟡 **Wired, sin captura**: handler real, lógica razonada, falta una captura SMM2
  que confirme el shape exacto.
- ⚪ **Stub vacío**: cae a `smm2EmptyBuilders` con la envolvente vacía correcta
  (U32(0) + bool(true) según el shape del método). El cliente no rompe, la lista
  sale vacía.
- 🚫 **NotFound**: no implementado. Si el cliente lo llama → error DataStore::NotFound
  (0x80690004) → puede abortar el flow que lo rodea.

## DataStore (0x73) — métodos confirmados

| M | Nombre kinnay | Estado | Handler | Notas |
|---|---|---|---|---|
| 8 | (not named) | 🚫 NotFound | (catch-all) | explícitamente devuelto como error |
| 15 | rate_object | ✅ Wired | `smm2RateObject` | like/heart/boo. Bumps `courses.recordRating` + `profiles.recordRating` |
| 22 | touch_object | ✅ Wired | `smm2TouchObject` | bump PlayCount + persiste relación played |
| 24 | prepare_post_object | ✅ Wired | `smm2PreparePostObject` | metadata stashing (legacy, no-courses) |
| 25 | prepare_get_object | ✅ Wired | `smm2PrepareGetObject` | URL + headers para el blob |
| 26 | complete_post_object | ✅ Wired | `smm2CompletePostObject` | ack; ya no usado para courses |
| 47 | register_user | ✅ Wired | `smm2RegisterUser` | parse + persist en `profiles.json` |
| 48 | get_users | ✅ Wired | `smm2GetUsersFromProfiles` | dynamic per-PID; cae a `conn.PID` si 1 PID y no match |
| 49 | sync_user_profile | ✅ Wired | `patchSyncProfile` (template) o `syntheticSyncProfileResult` | pid+name patcheados al template |
| 53 | search_users_played_course | ✅ Wired | `smm2SearchUsersPlayedCourse` | lee `profiles.PlayedCourses` |
| 54 | search_users_cleared_course | ✅ Wired | `smm2SearchUsersClearedCourse` | lee `profiles.FirstCleared` |
| 55 | search_users_positive_rated_course | ✅ Wired | `smm2SearchUsersPositiveRatedCourse` | lee `profiles.RatedCourses` filtrado a slot 0/1 |
| 58 | search_courses_leaderboard | 🟡 Wired | `smm2SearchCoursesLeaderboard` | shape deducida (list<CourseInfo>+list<u32>ranks+bool) |
| 59 | update_last_login_time | ✅ Wired | inline (void ack) | paramless, sin return value |
| 60 | can_post_course | ✅ Wired | `smm2CanPostCourse` | {bool, u32} documentado |
| 61 | can_post_rating_and_comment | ✅ Wired | `smm2CanPostRatingAndComment` | shape confirmado byte-by-byte contra doc |
| 63 | get_mii_clothes | 🟡 Wired | inline (lista vacía) | documentado, sin captura específica |
| 64 | get_course_record | 🚫 NotFound | (catch-all) | el cliente cae al `smm2GetUsers` si se llama |
| 65 | get_user_name_ng_type | 🟡 Wired | inline (u8 0) | documentado, sin NG flag = 0 |
| 66 | prepare_post_object_course | ✅ Wired | `smm2PreparePostObjectCourse` | allocate data_id, stash metadata |
| 68 | complete_post_objects_course | ✅ Wired | `smm2CompletePostObjectsCourse` | ack void; el Course ID real vuelve vía m=70 |
| 69 | update_course_tag | ✅ Wired | `smm2UpdateCourseTag` | sin return value |
| 70 | get_courses | ✅ Wired | `smm2GetCourses` | CourseInfo + result list; el flow de "Upload complete. Course ID: ..." vive acá |
| 72 | search_courses_method72 | 🟡 Wired | `smm2SearchCoursesByMethod72` | tab 3 de Course World; misma shape que 73/74 |
| 73 | search_courses_latest | ✅ Wired | `smm2SearchCoursesLatest` | tab "New Courses" |
| 74 | search_courses_posted_by | ✅ Wired | `smm2SearchCoursesPostedBy` | "courses by player X" — fix del offset/size de ResultRange |
| 75 | search_courses_positive_rated_by | ✅ Wired | `smm2SearchCoursesPositiveRatedBy` | lee `profiles.RatedCourses` |
| 76 | search_courses_played_by | ✅ Wired | `smm2SearchCoursesPlayedBy` | lee `profiles.PlayedCourses` |
| 80 | search_courses_first_clear | ✅ Wired | `smm2SearchCoursesFirstClear` | lee `profiles.FirstCleared` |
| 81 | search_courses_best_time | 🟡 Wired | `smm2SearchCoursesBestTime` | misma data source que 80 (sin parser per-player best-time) |
| 82 | search_courses_followee_posted_by | ⚪ Stub | `smm2EmptyBuilders` | list<CourseInfo> + bool — antes caía a NotFound |
| 84 | search_courses_hot | ✅ Wired | `smm2SearchCoursesHot` | tab "Hot/Popular" — sort por hotness (likes+hearts+plays) |
| 94 | search_comments_in_order | ✅ Wired | `smm2SearchCommentsInOrder` | paginado |
| 95 | search_comments | ✅ Wired | `smm2SearchComments` | all-in-one sin paginación |
| 96 | post_play_result | ✅ Wired | `smm2PostPlayResult` | bump PlayCount + clear/death + mirror a profile del owner |
| 103 | get_death_positions | ⚪ Stub | `smm2EmptyBuilders` | list<DeathPositionInfo> vacío; antes NotFound |
| 104 | play_event | ✅ Wired | `smm2PlayEvent` | log fire-and-forget (play started / play ended) |
| 125 | (undocumented) | 🟡 Wired | `smm2Method125` | responde 8 bytes 00 03 00 00 00 00 00 00 (verified vs capture) |
| 129 | get_ng_course_notification | ✅ Wired | inline (5 bytes 00 00 00 00 00) | antes devolvía nil body (0 bytes) → cliente erroreaba |
| 132 | prepare_relation_upload | ✅ Wired | `smm2PrepareRelationUpload` | thumbnails + clear-check upload prep |
| 133 | complete_post_relation_object | ✅ Wired | `smm2CompletePostRelationObject` | ack |
| 134 | get_req_get_info_headers_info | ✅ Wired | `smm2GetReqGetInfoHeadersInfo` | antes NotFound → thumbnails no renderizaban |
| 147 | (undocumented) | ⚪ Stub | `smm2EmptyBuilders` | shape deducida, sin doc |
| 152 | (undocumented) | ✅ Wired | `smm2Method152` | void ack, llamado 2x en flow de play |
| 154 | get_event_course_status | 🟡 Wired | inline (EventCourseStatusInfo neutral) | sin evento activo |
| 160 | get_world_map | ⚪ Stub | `smm2EmptyBuilders` | list<WorldMap> + list<result> vacíos |
| 162 | search_world_map_pick_up | ⚪ Stub | `smm2EmptyBuilders` | list<WorldMap> vacío |
| 168 | (undocumented) | ⚪ Stub | `smm2EmptyBuilders` | shape deducida, sin doc |

## Auth (0x6E)

| M | Estado | Notas |
|---|---|---|
| 1 | ✅ | Login estándar |
| 2 | ✅ | LoginEx — con gates Nextendo + token nx2 firmado |
| 3 | ✅ | Logout |
| 7 | ✅ | RequestTicket — ticket granting |
| 8 | ✅ | GetPID — público |

## SecureServer (0x6A)

| M | Estado | Notas |
|---|---|---|
| 1 | ✅ | Login NEX estándar |
| 2 | ✅ | RegisterServer |
| 4 | ✅ | RequestConnectionData |
| 6 | ✅ | ReportNatProperties |
| 9 | ✅ | GetNatMapping |

## Qué falta / gaps conocidos

### P0 — rompería el flow de upload o play
- **m=8** explícitamente NotFound (intencional; no debería llamarse)
- Cualquier método no listado arriba que el cliente llame y caiga a NotFound

### P1 — UX degradado pero no rompe
- **m=81** best_time: misma data source que 80; sin parser per-player de replay.bin
- **m=103** death_positions: lista vacía (no tenemos data de death-positions en replay)
- **m=160, 162** world maps: listas vacías (no implementado)
- **m=147, 168** undocumented: shapes deducidas, sin doc — riesgo de shape incorrecto

### P2 — fino, posibles issues de shape
- **m=125** undocumented: 8 bytes asumidos, no contrastados con doc
- **m=58, 72** leaderboard/method72: shape deducida, funciona en práctica pero no hay doc

### P3 — no afecta funcionalidad
- Endless mode high scores (perfil): schema existe, no se popula desde handlers
- Badges: schema existe, no se otorgan
- Mii clothes (m=63): no tenemos catálogo

## Persistencia

| Store | Archivo | Función | Init |
|---|---|---|---|
| `courseStore` | `<storageDir>/courses.json` | `load()` | startup |
| `profileRegistry` | `<storageDir>/profiles.json` | `load()` | `init()` smm2_users.go:197 |
| `commentStore` | `<storageDir>/comments.json` | `load()` | `init()` smm2_comments.go:449 |

donde `storageDir = envOr("STORAGE_DIR", "smm2_objects")` (smm2_storage.go:46).

## Cross-service

- `sni-router` (puerto 443) → rutéa a este server los SNIs `g210a9200`,
  `g22306d00`, `g83110300`, `g83110400`, `g8930141a` (SMM2 builds).
- `nextendo-account` (HTTP) → `/api/names` para resolver pseudos,
  `/api/nsa` para mapear NSA → PID, `/internal/online-check` para gates.
- `nextendo-nex` (Go lib) → protocolo PRUDP-Lite + RMC encoding.
