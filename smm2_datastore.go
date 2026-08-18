package main

// SMM2 DataStore — passage du REPLAY (rejoue la session capturée = fuite des données du
// joueur capturé + faux niveaux Nintendo injouables) au DYNAMIQUE : les méthodes de CONTENU
// (listes de niveaux / d'utilisateurs / commentaires / world map) renvoient des listes VIDES
// (serveur vierge), et les méthodes STRUCTURELLES du boot gardent le replay (SMM2 en a besoin
// pour entrer dans Course World, et elles ne fuitent ni niveau ni ami).
//
// Forme des retours (datastore_smm2.proto) : list<T> => U32(0) ; bool => true. On construit
// donc l'enveloppe vide exacte de chaque méthode. Résultat : Course World s'affiche mais VIDE,
// le pseudo reste celui du compte local, aucune donnée capturée n'est servie aux autres.

import (
	"fmt"
	"os"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// smm2EmptyBuilders : par méthode DataStore de contenu, écrit l'enveloppe VIDE valide.
// (courses/users/maps/comments = list<T> vide ; + bool result=true / list<result> vide selon la méthode.)
var smm2EmptyBuilders = map[uint32]func(*nex.StreamOut){
	// NOTE: get_users(48) reste en REPLAY — SMM2 exige un UserInfo valide (son PROPRE profil) au
	// boot, une liste vide casse l'init. Le nettoyer proprement = construire un UserInfo dynamique
	// pour le PID connecté (structure lourde, prochaine étape) au lieu de rejouer la session capturée.
	// 53/54/55 (search_users_played/cleared/positive_rated_course): wired to
	// real handlers in the switch below (smm2SearchUsersPlayedCourse /
	// smm2SearchUsersClearedCourse / smm2SearchUsersPositiveRatedCourse).
	// They need the per-user PlayedCourses / FirstCleared / RatedCourses maps
	// to be populated, which happens on m=15 (rate), m=22 (touch), m=25
	// (prepare_get_object for non-self), m=96 (post_play_result) and on the
	// setCourseTimes path (storage.go) for first clears. Previously these
	// were stubs returning u32(0) — meaning the "People who played/cleared/
	// liked this course" lists on the course detail page ("more info") were
	// always empty.
	// 70 (get_courses): wired to smm2GetCourses in the switch below (case 70).
	// Kept out of smm2EmptyBuilders because the response needs conn.PID to filter
	// the catalog — a stateless builder can't do that.
	71: func(o *nex.StreamOut) { o.U32(0); o.U32(0); o.Bool(true) }, // point_ranking: courses[], ranks[], result
	// 73 (search_courses_latest / "New Courses"): wired to smm2SearchCoursesLatest
	// in the switch below, now that CourseInfo is confirmed working.
	// 74 (search_courses_posted_by): wired to smm2SearchCoursesPostedBy in the
	// switch below. The empty-list response was lying — even a player with uploads
	// got an empty "courses posted by" page, both in their own maker profile and on
	// other players' profile pages.
	//
	// 75/76/80/81 (positive_rated_by / played_by / first_clear / best_time)
	// are wired to real handlers in the switch above (smm2SearchCourses*)
	// — they need profiles.coursesPlayed/Rated/FirstCleared to be populated,
	// which happens on m=15 (rate) and m=22/m=96 (touch / post_play).
	79: func(o *nex.StreamOut) { o.U32(0) },               // search_courses_endless_mode
	82: func(o *nex.StreamOut) { o.U32(0); o.Bool(true) }, // search_courses_followee_posted_by: courses[], result — confirmed via measured_live.txt: fell to NotFound (method=0 in the S->C log) since it was missing from this map
	85: func(o *nex.StreamOut) { o.U32(0); o.U32(0) },     // get_courses_event: courses[], results[]
	86: func(o *nex.StreamOut) { o.U32(0) },               // search_courses_event
	// 94/95 (search_comments_in_order / search_comments): wired to real
	// handlers in the case-switch above (smarter than the all-zero fallback —
	// they need the comment store to be loaded at startup, which init() in
	// smm2_comments.go does).
	160: func(o *nex.StreamOut) { o.U32(0); o.U32(0) }, // get_world_map: maps[], results[]
	162: func(o *nex.StreamOut) { o.U32(0) },           // search_world_map_pick_up: maps[]

	// (103) get_death_positions: data_id:int -> list[DeathPositionInfo]. DOCUMENTED
	// (nintendoclients.readthedocs.io) but never implemented — fell through to
	// NotFound. New lead from the user: the "Uploaded Courses" view (your OWN
	// courses) shows a "View Deaths" button that "New Courses" (other players')
	// doesn't — the client may eagerly query this for owner-only courses while
	// building that list/detail view, and an unimplemented NotFound there could be
	// exactly what crashes rendering (matches the reported "spinner then instant
	// fail, nothing shown" symptom). We have no death-position data to report, so
	// an empty list is the correct honest answer regardless.
	103: func(o *nex.StreamOut) { o.U32(0) }, // get_death_positions: list<DeathPositionInfo>

	// --- Leaderboard-facing methods, per kinnay/NintendoClients wiki (Data-Store-Protocol SMM2) —
	//     none were implemented before, so Leaderboards fell through to NotFound. Same "empty
	//     tuple" pattern; a List<UserInfo> and a List<CourseInfo> both encode as U32(0) when empty,
	//     so labeling doesn't matter for the empty case.
	50: func(o *nex.StreamOut) { o.U32(0); o.U32(0); o.Bool(true) }, // search_users_user_point: users[], ranks[], result
	51: func(o *nex.StreamOut) { o.U32(0); o.U32(0); o.Bool(true) }, // search_users_endless_mode: users[], unk[], unk
	52: func(o *nex.StreamOut) { o.U32(0); o.U32(0); o.Bool(true) }, // search_users_battle_mode: users[], unk[], unk
	56: func(o *nex.StreamOut) { o.U32(0); o.Bool(true) },           // search_users_followee: users[], unk
	57: func(o *nex.StreamOut) { o.U32(0); o.U32(0); o.Bool(true) }, // search_users_clear_ranking: users[], unk[], unk

	// --- NOT documented at all by kinnay/NintendoClients (no request/response shape given).
	//     Traced by call sequence in measured_live.txt: 147 fires right before the client
	//     re-prompts Mii/name creation (the "M" leaderboard tab); 168 fires right before the
	//     "Favorites" error. Best-guess empty tuple, same shape as their documented siblings —
	//     unverified, revisit if a real capture or doc turns up.
	147: func(o *nex.StreamOut) { o.U32(0); o.Bool(true) }, // search_users_official (undocumented)
	168: func(o *nex.StreamOut) { o.U32(0); o.Bool(true) }, // search_users_followee_v2 (undocumented)

	// --- Méthodes NON documentées (SMM2 3.x) qui peuplent le HUB Course World (Hot/Popular/New) :
	//     structure déduite en parsant les réponses capturées (list<CourseInfo>[+ranks][+bool]).
	//     Ce sont elles qui affichaient les faux niveaux Nintendo -> on les vide aussi.
	58: func(o *nex.StreamOut) { o.U32(0); o.U32(0); o.Bool(true) }, // courses[], ranks[], result (comme 83)
	72: func(o *nex.StreamOut) { o.U32(0); o.Bool(true) },           // courses[], result
	83: func(o *nex.StreamOut) { o.U32(0); o.U32(0); o.Bool(true) }, // courses[], ranks[], result (Popular)
	84: func(o *nex.StreamOut) { o.U32(0) },                         // courses[] (Hot/New)
}

// smm2DataStoreHandler : contenu -> VIDE ; sinon -> replay capturé (méthodes structurelles du
// boot que SMM2 exige pour entrer dans Course World). 0x73.8 = NotFound comme Nintendo.
func smm2DataStoreHandler() nex.RMCHandler {
	return func(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
		s := conn.Settings

		// --- Dynamic profile: rewrite the measured identity to the connected account.
		// get_users(48): one profile per requested pid (never the 261 measured users).
		if req.Method == 48 {
			// ALWAYS use the registered-profile path, regardless of whether a captured
			// measured/resp_0x73_m48.bin template happens to be loaded. smm2GetUsers (the
			// template path) predates profiles.go entirely and has no idea it exists — it
			// patches pid/code/name into a static captured tail and calls pseudoOr() for the
			// name, ignoring anything RegisterUser(47) actually saved. Confirmed via
			// measured_live.txt: the moment the template loaded this session, the response
			// silently reverted to "Nextendo51966" instead of the real registered "Beer2".
			return smm2GetUsersFromProfiles(conn, req)
		}
		// sync_user_profile(49): the OWN profile — patch pid + pseudo into the template.
		if req.Method == 49 {
			if tmpl, ok := capturedResponses[replayKey(0x73, 49)]; ok {
				body := patchSyncProfile(s, tmpl, conn.PID, pseudoOr(conn.PID))
				fmt.Printf("[SMM2 DataStore] sync_user_profile(49) -> pseudo Nextendo pid=%d\n", conn.PID)
				return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, body)
			}
			// No captured template: build from whatever was REGISTERED for this pid, instead
			// of a bare U32(0) (which wasn't even a valid SyncUserProfileResult to begin with).
			r := profiles.get(conn.PID)
			body := syntheticSyncProfileResult(s, conn.PID, r)
			fmt.Printf("[SMM2 DataStore] sync_user_profile(49) -> pid=%d registered=%v\n", conn.PID, r != nil)
			return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, body)
		}
		// (47) RegisterUser — per kinnay/NintendoClients wiki (Data-Store-Protocol SMM2), this is
		// NOT "post_relation_data": it's where the client sends its just-built maker profile
		// (username, Mii, region/country). We now parse and PERSIST it (smm2_users.go) so a
		// future feature can use it, but we don't change get_users/sync_user_profile's
		// response shape yet — isolating this step's risk to "does RegisterUser's own
		// response change break anything", nothing else.
		if req.Method == 47 {
			return smm2RegisterUser(conn, req)
		}
		// (154) GetEventCourseStatus — per kinnay/NintendoClients wiki, NOT "get_ranking_by_pid":
		// takes no parameters and returns EventCourseStatusInfo{Uint64, Bool, DateTime}. We have
		// no active event course, so serve a neutral status instead of a bare U32(0) (which isn't
		// even the right shape — EventCourseStatusInfo isn't a list at all).
		if req.Method == 154 {
			body := nex.NewStreamOut(s)
			body.U64(0)      // unknown
			body.Bool(false) // unknown (likely "event active"-style flag)
			body.DateTime(0) // unknown
			resp := frameStruct(s, 0, body.Bytes())
			fmt.Printf("[SMM2 DataStore] GetEventCourseStatus(154) -> neutral EventCourseStatusInfo\n")
			return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, resp)
		}

		// (59) UpdateLastLoginTime — per kinnay/NintendoClients wiki: no parameters, no return
		// value. Wasn't implemented at all before (fell through to NotFound), and this call
		// shows up right around Courses-list entry in measured_live.txt — a likely trigger for
		// getting kicked back to account/Mii creation, since an error here could read to the
		// client as "this session has no valid login".
		if req.Method == 59 {
			fmt.Printf("[SMM2 DataStore] UpdateLastLoginTime(59) pid=%d -> ack (no return value)\n", conn.PID)
			return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, nil)
		}

		// (63) GetMiiClothes: per kinnay/NintendoClients' OFFICIAL wiki (Data-Store-
		// Protocol-SMM-2), this takes NO parameters but its response is documented as
		// `List<MiiClothes>` — NOT "no return value" as this method was treated before
		// (a truly empty body, 0 bytes). None of our 3 real reference captures show a
		// response for this call either (only ever the request, never a matching
		// INCOMING reply), so we don't have real bytes to copy — but the documented
		// shape is clear enough to build correctly: an empty list is U32(0), 4 bytes,
		// not a bare void ack. We have no Mii clothing data to report, so an empty
		// list is the honest answer.
		if req.Method == 63 {
			out := nex.NewStreamOut(s)
			out.U32(0) // list<MiiClothes>, empty
			fmt.Printf("[SMM2 DataStore] GetMiiClothes(63) pid=%d -> empty list (documented shape, not a void ack)\n", conn.PID)
			return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
		}
		// (65) GetUserNameNgType: per kinnay's official wiki, takes no parameters,
		// response is `Uint8` ("Type") — also NOT "no return value" as treated before.
		// 0 = not flagged/no NG type is the safe default (we don't run username
		// moderation).
		if req.Method == 65 {
			out := nex.NewStreamOut(s)
			out.U8(0) // Uint8 type: 0 = no NG flag
			fmt.Printf("[SMM2 DataStore] GetUserNameNgType(65) pid=%d -> 0 (documented shape, not a void ack)\n", conn.PID)
			return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
		}
		// (129) GetNgCourseNotification: confirmed via a real capture — the 5-byte body
		// 00 00 00 00 00 (u32 0 + u8 0) is what the client expects; sending a void ack
		// (nil body) was the bug — the client read the missing payload as an error and
		// aborted the surrounding flow.
		if req.Method == 129 {
			fmt.Printf("[SMM2 DataStore] method 129 pid=%d -> 5-byte ack (u32=0 + u8=0)\n", conn.PID)
			return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, []byte{0, 0, 0, 0, 0})
		}

		// (61) CanPostRatingAndComment: per kinnay/NintendoClients' official wiki
		// (Data-Store-Protocol-SMM-2) — NOT a relation-object fetch (an earlier guess,
		// made before the official doc turned up, had it as "PrepareGetRelationObject").
		// Confirmed by an exact byte-count match: request {Uint64, Uint32} and response
		// {Uint64,Bool,Uint32,Map,Bool,Uint32,Map} at all-zero/false/empty land on
		// exactly 12 and 26 bytes — matching the reference capture's byte counts field
		// for field, not just in total length.
		if req.Method == 61 {
			return smm2CanPostRatingAndComment(conn, req)
		}

		// --- Level storage: real object upload/download on the Nextendo VPS.
		switch req.Method {
		case 24:
			return smm2PreparePostObject(conn, req)
		case 25:
			return smm2PrepareGetObject(conn, req)
		case 26:
			return smm2CompletePostObject(conn, req)
		case 60:
			// CanPostCourse: no request params, response {Bool, Uint32} — documented.
			return smm2CanPostCourse(conn, req)
		case 66:
			// Course level-data upload prep: built from the documented DataStoreReqPostInfo
			// shape (smm2_objects.go), not a patched Copilot-generated blob.
			return smm2PreparePostObjectCourse(conn, req)
		case 68:
			// CompletePostObjectsCourse: ack with no return value (kinnay "void") — confirmed
			// correct: the real Course ID comes back via get_courses(70)'s CourseInfo.code,
			// not from here.
			return smm2CompletePostObjectsCourse(conn, req)
		case 69:
			// UpdateCourseTag: per spec, no return value.
			return smm2UpdateCourseTag(conn, req)
		case 70:
			// get_courses: list<CourseInfo> + list<result>. CONFIRMED WORKING via a real
			// "Upload complete. Course ID: XXX-XXX-XXX" screen after fixing 3 concrete bugs
			// (request not parsed, CourseInfo double-buffered, empty results list).
			return smm2GetCourses(conn, req)
		case 73:
			// search_courses_latest: "New Courses" tab, global across all uploaders.
			return smm2SearchCoursesLatest(conn, req)
		case 15:
			// rate_object: like/heart/boo on a course. Was previously falling through
			// to NotFound, so any attempt to rate a course failed silently and the
			// per-course LikeCount + the owner's MakerStats.LikesReceived never
			// moved. Now wired: bumps courses.recordRating + profiles.recordRating
			// and returns the updated aggregate.
			return smm2RateObject(conn, req)
		case 74:
			// search_courses_posted_by: "courses by player X" — backs the maker
			// profile's "My courses" tab AND other players' profile pages. Per
			// SearchCoursesPostedByParam (NintendoClients:1607), takes a pid list
			// and a ResultRange pagination window; we treat the first pid as the
			// owner (SMM2 sends one at a time in practice).
			return smm2SearchCoursesPostedBy(conn, req)
		case 75:
			// search_courses_positive_rated_by: "courses I liked/hearted (not
			// boo'd)" — the maker profile's "Liked Courses" tab. Reads from
			// profiles.RatedCourses (fed by rate_object(15)).
			return smm2SearchCoursesPositiveRatedBy(conn, req)
		case 76:
			// search_courses_played_by: "courses I played" — the maker
			// profile's "Played Courses" tab. Reads from profiles.PlayedCourses
			// (fed by touch_object(22) and post_play_result(96)).
			return smm2SearchCoursesPlayedBy(conn, req)
		case 80:
			// search_courses_first_clear: "courses I was the first to clear".
			// Reads from profiles.FirstCleared (fed by setCourseTimes on
			// first replay upload + by m=96 with cleared=1).
			return smm2SearchCoursesFirstClear(conn, req)
		case 81:
			// search_courses_best_time: "courses with my best time on the
			// leaderboard". Currently same data source as 80 (no per-player
			// best-time parser yet). Will tighten the filter once replay.bin
			// is decoded.
			return smm2SearchCoursesBestTime(conn, req)
		case 84:
			// search_courses_hot: "Hot/Popular Courses" tab in Course World.
			// NOT documented in NintendoClients (same undocumented territory as 58/72/83
			// which populate the same Hub). Previously fell through to smm2EmptyBuilders
			// and returned just `u32 0` (empty list) — meaning the tab always showed
			// nothing, no error, just no courses. Now wired with the same buildCourseInfo
			// used by 73/74, sorted by hotness (likes+hearts+plays) instead of by date.
			return smm2SearchCoursesHot(conn, req)
		case 72:
			// search_courses_method72: third Course World tab (between "New" and "Hot").
			// NOT documented. Same response shape as 73/74: list<CourseInfo> + bool.
			// Previously returned `u32 0; u8 true` (empty). Now wired with real data,
			// sorted newest first (same as 73) — switch to a different sort if the
			// tab turns out to need popularity/region/tag filtering.
			return smm2SearchCoursesByMethod72(conn, req)
		case 58:
			// search_courses_leaderboard: "Leaderboards / Course Markers" tab in
			// Course World. NOT documented. Response shape is the wider "ranking"
			// format: list<CourseInfo> + list<u32> ranks + bool result. Previously
			// fell through to smm2EmptyBuilders and returned all-zero (no error, just
			// no courses). Now wired with the same buildCourseInfo, sorted by hotness
			// (likes+hearts+plays) with 1-indexed rank values per course.
			return smm2SearchCoursesLeaderboard(conn, req)
		case 134:
			// get_req_get_info_headers_info: the client calls this before actually
			// fetching a relation object (thumbnail) over HTTP — confirmed via a real
			// capture, it was previously falling through to NotFound (unimplemented),
			// and the thumbnails never rendered even though the URL/size/data_type
			// were all correct.
			return smm2GetReqGetInfoHeadersInfo(conn, req)
		case 132:
			// Relation-data upload prep (thumbnails + clear-check): a fresh
			// RelationObjectReqPostInfo per call, built from the documented shape.
			return smm2PrepareRelationUpload(conn, req)
		case 133:
			// CompletePostRelationObject: undocumented in detail, acked like its siblings.
			return smm2CompletePostRelationObject(conn, req)
		case 152:
			// (152) — Undocumented in kinnay/NintendoClients. Called by the client
			// BEFORE m=25 in the play flow (twice in the reference capture, both
			// with the same 9-byte body). The reference response is a void ack (0-byte
			// body, success=true). Without this the client fell off the connection
			// ~8s into the play sequence — NotFound error seems to abort the flow.
			// Same shape as the "no params, no return" methods (59/68/69/133) that
			// the rest of the dispatcher treats as void acks.
			return smm2Method152(conn, req)
		case 125:
			// (125) — Undocumented in kinnay/NintendoClients. Called by the client
			// right after m=152 in the play flow, with an empty body. The reference
			// response is 8 bytes: 00 03 00 00 00 00 00 00 — could be u64=0x300=768
			// or some other field layout, but the byte sequence is verified against
			// the reference capture. Send the exact same 8 bytes so the client's downstream
			// parsing lands in the same state.
			return smm2Method125(conn, req)
		case 103:
			// (103) GET_DEATH_POSITIONS — kinnay-indexed, "for retrieving the death
			// positions recorded for a course". In the play flow the reference
			// response is u32(0) (no deaths recorded for this course yet). Sending
			// NotFound here would also abort the flow.
			return smm2GetDeathPositions(conn, req)
		case 22:
			// (22) TOUCH_OBJECT — kinnay-indexed, "marks the object as viewed/touched".
			// The client calls it right when a play session starts (or when the
			// course detail is opened). We bump PlayCount by 1 and ack with no
			// return value. Future client sessions can then read the bumped count
			// back via CourseInfo.play_stats.
			return smm2TouchObject(conn, req)
		case 96:
			// (96) POST_PLAY_RESULT — NOT documented by kinnay/NintendoClients.
			// Called by the client right after a play session ends (death or clear),
			// BEFORE the m=70/m=48 re-fetches that close the loop. The reference
			// (OCW) sends back a void ack (0 bytes, success=true). The body is a
			// 51-byte substream that we read best-effort to extract:
			//   - data_id of the course played
			//   - a "cleared" flag (u8 at the end, value 1 = cleared, 0 = died)
			//   - a death count (u32 in the middle)
			// The remaining fields are undocumented SMM2-specific payloads; we
			// don't try to interpret them, just bump the catalog's PlayCount
			// (always) + ClearCount or DeathCount based on the cleared flag, and
			// mirror the deltas to the player profile + the course's owner
			// profile. Without this, the post-play stats the client shows in the
			// detail screen stay frozen at 0.
			return smm2PostPlayResult(conn, req)
		case 104:
			// (104) PLAY_EVENT — NOT documented by kinnay/NintendoClients.
			// Called by the client twice in the post-play sequence (once before
			// m=96 with type=0x0101, once after the m=70/m=48 re-fetch with
			// type=0x0201). The body is a fixed 16-byte shape:
			//   [u8=0][u32=11][u64=course_id][u8=0][u16=type]
			// The reference (OCW) sends back a void ack. We just log the event
			// and ack — no stats mutation, this is a "play started / play ended"
			// log entry, not a stats update.
			return smm2PlayEvent(conn, req)
		case 94:
			// (94) SEARCH_COMMENTS_IN_ORDER — paginated CommentInfo list.
			// Wired to smm2SearchCommentsInOrder (smm2_comments.go), which
			// handles the ResultRange pagination + the trailing "result" bool.
			return smm2SearchCommentsInOrder(conn, req)
		case 95:
			// (95) SEARCH_COMMENTS — all-in-one CommentInfo list (no
			// pagination, no trailing bool). Wired to smm2SearchComments.
			return smm2SearchComments(conn, req)
		case 53:
			// (53) SEARCH_USERS_PLAYED_COURSE — "people who have played this
			// course". The "more info" panel on a course detail page issues
			// 53/54/55 in sequence to populate the three "who" lists. Wired
			// to smm2SearchUsersPlayedCourse (reads profiles.PlayedCourses
			// and emits one UserInfo per matching PID).
			return smm2SearchUsersPlayedCourse(conn, req)
		case 54:
			// (54) SEARCH_USERS_CLEARED_COURSE — "people who first-cleared
			// this course". Same flow as 53, reads profiles.FirstCleared.
			return smm2SearchUsersClearedCourse(conn, req)
		case 55:
			// (55) SEARCH_USERS_POSITIVE_RATED_COURSE — "people who liked
			// or hearted this course". Same flow as 53, reads
			// profiles.RatedCourses filtered to slot 0/1.
			return smm2SearchUsersPositiveRatedCourse(conn, req)
		}

		if build, ok := smm2EmptyBuilders[req.Method]; ok {
			out := nex.NewStreamOut(s)
			build(out)
			fmt.Printf("[SMM2 DataStore] 0x73.%d -> VIDE (serveur vierge)\n", req.Method)
			return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
		}

		if req.Method == 8 {
			return nex.NewRMCError(s, 0x73, req.CallID, 0x80690004) // DataStore::NotFound
		}

		body, ok := capturedResponses[replayKey(0x73, req.Method)]
		if !ok {
			// Return DataStore::NotFound for unimplemented methods
			fmt.Printf("[SMM2 DataStore] UNCAPTURED 0x73.%d call=%d -> NotFound error\n", req.Method, req.CallID)
			return nex.NewRMCError(s, 0x73, req.CallID, 0x80690004) // DataStore::NotFound
		}
		fmt.Printf("[SMM2 DataStore] 0x73.%d -> replay structurel (%do)\n", req.Method, len(body))
		return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, body)
	}
}

// smm2Method152 (152) — undocumented in kinnay/NintendoClients. In the reference play
// flow capture the client calls it twice (same 9-byte body both times) BEFORE
// m=25, and the server returns a void ack both times. The 9-byte body decodes
// to [u8 ver=0][u32 substream=4][u32 param=0x14B1] — the u32 is probably a
// session/play ID of some kind, but without a second capture we can't tell
// exactly what the client does with our response. Void ack is the safe answer
// because anything else (NotFound error, structured body of wrong shape)
// reliably aborts the play flow.
func smm2Method152(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	in := nex.NewStreamIn(req.Body, s)
	_ = in.U8()
	sub := in.Substream()
	param := sub.U32()
	fmt.Printf("[SMM2 DataStore] method 152 pid=%d param=0x%x -> ack (no return value, matches the reference)\n",
		conn.PID, param)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, nil)
}

// smm2Method125 (125) — undocumented in kinnay/NintendoClients. In the reference
// play flow capture the client calls it once with an empty body, RIGHT AFTER
// m=152, and the server returns exactly 8 bytes: 00 03 00 00 00 00 00 00.
// The most likely layout is u64 LE = 0x0000000000000300 = 768, but the
// field-shape isn't verified — we send the raw bytes verbatim so the client's
// downstream parser sees the same wire format the reference does.
func smm2Method125(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	out := nex.NewStreamOut(s)
	out.Write([]byte{0x00, 0x03, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
	fmt.Printf("[SMM2 DataStore] method 125 pid=%d -> 8 bytes (matches the reference: 00 03 00 00 00 00 00 00)\n",
		conn.PID)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}

// deathPositionInfoOut is one entry of get_death_positions(103)'s response.
// Real structure per kinnay/NintendoClients (nintendoclients.readthedocs.io,
// datastore_smm2.DeathPositionInfo): {data_id: int, x: int, y: int, is_subworld: bool}.
type deathPositionInfoOut struct {
	dataID     uint64
	x          int32
	y          int32
	isSubworld bool
}

func (s *deathPositionInfoOut) Levels() []nex.Level {
	return []nex.Level{{
		Version: 0,
		Save: func(out *nex.StreamOut) {
			out.U64(s.dataID)
			out.S32(s.x)
			out.S32(s.y)
			out.Bool(s.isSubworld)
		},
	}}
}

// parseDeathPositionsFromRelation reads the post-play relation blobs (relType 6/12,
// see relationBaseName's doc) and extracts death positions from them.
//
// CONFIRMED REAL (18/8, comparing data6.bin/data12.bin across the 5 courses in this
// catalog): the file starts with a magic header "SPRH" (not documented anywhere we've
// found), followed by a run of FIXED 12-byte records, each starting with the same
// 4-byte marker 80 3B 00 40, followed by ONE byte that varies 0-255 across records —
// on the one course in our catalog with several attempts (1002, ALSO the longest
// course in the catalog per the user), 18 such records appeared with values spread
// across nearly the full 0-255 range (13 to 230), matching what a normalized
// "how far through this specific course's length" position would look like on a
// course using close to the maximum allowed width. Courses with no/few attempts have
// NO such records (file is just the fixed ~130-byte header/footer with none of this
// repeating block).
//
// NOT YET CONFIRMED: whether that single varying byte is genuinely "x" (position
// along course length) with "y" living somewhere else in the same 12-byte record, or
// whether y is simply not encoded here at all. We only have ONE varying byte
// isolated so far — treating it as x (scaled into a small coordinate range) and
// leaving y at a defensible middle-of-screen default until a capture with real
// varying y data lets us isolate it the same way we isolated x. is_subworld is
// always false — we have no confirmed signal for sub-area deaths yet.
func parseDeathPositionsFromRelation(dataID uint64) []deathPositionInfoOut {
	var blob []byte
	for _, relType := range []uint32{6, 12} {
		p := relationPath(dataID, relType)
		if p == "" {
			continue
		}
		if b, err := os.ReadFile(p); err == nil && len(b) > 0 {
			blob = b
			break
		}
	}
	if len(blob) == 0 {
		return nil
	}
	marker := []byte{0x80, 0x3b, 0x00, 0x40}
	var out []deathPositionInfoOut
	for i := 0; i+len(marker)+1 <= len(blob); i++ {
		if blob[i] == marker[0] && blob[i+1] == marker[1] && blob[i+2] == marker[2] && blob[i+3] == marker[3] {
			raw := blob[i+4] // the one confirmed-varying byte, 0-255
			// Scale the normalized 0-255 value into a plausible SMM2 world-x range.
			// SMM2 courses can run up to roughly 480 "blocks" wide in-game units —
			// this scaling is a best-effort guess, NOT confirmed against a real
			// client-rendered death marker yet.
			x := int32(raw) * 480 / 255
			out = append(out, deathPositionInfoOut{
				dataID: dataID,
				x:      x,
				y:      120, // defensible mid-height default; no isolated y signal yet
			})
		}
	}
	return out
}

// smm2GetDeathPositions (103) — kinnay-indexed as "GET_DEATH_POSITIONS" (for
// retrieving the death positions recorded for a course). Response is
// list<DeathPositionInfo> — real fields confirmed via kinnay's official doc:
// {data_id, x, y, is_subworld}, NOT a bare U32(0) placeholder as before.
//
// IMPLEMENTED (18/8): reads real positions via parseDeathPositionsFromRelation
// (see its doc comment for the confirmed 12-byte-record format and what's
// still uncertain about it). Courses with no recorded deaths correctly get an
// empty list, same as before this change.
func smm2GetDeathPositions(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	// Request is a raw u64 data_id — NO [u8 ver][u32 substream] framing.
	// Confirmed from wire: len=8, bytes = e803000000000000 = LE u64 1000.
	in := nex.NewStreamIn(req.Body, s)
	courseID := in.U64()
	positions := parseDeathPositionsFromRelation(courseID)
	out := nex.NewStreamOut(s)
	out.U32(uint32(len(positions)))
	for _, p := range positions {
		out.Add(&p)
	}
	fmt.Printf("[SMM2 DataStore] get_death_positions(103) pid=%d course_id=%d -> %d position(s)\n",
		conn.PID, courseID, len(positions))
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}

// smm2TouchObject (22) — kinnay-indexed as "TOUCH_OBJECT" (fire-and-forget
// "mark the object as viewed/touched"). The client calls it when a play
// session starts; we bump PlayCount by 1 and ack with no return value. The
// next time the client reads CourseInfo (m=70/73/74/84), play_stats[0] will
// reflect the bumped value.
//
// Request shape per kinnay/datastore.py TouchObject: takes a single u64
// data_id, framed the standard NEX way. We accept the data_id in the
// substream (whatever version byte kinnay emits today); if parsing fails the
// substream is empty and we still ack.
func smm2TouchObject(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	var dataID uint64
	if len(req.Body) > 0 {
		defer func() { recover() }()
		in := nex.NewStreamIn(req.Body, s)
		_ = in.U8() // version
		sub := in.Substream()
		dataID = sub.U64()
	}
	courses.applyPlayed(dataID, 1, 0, 0, 0) // course: +1 play
	// Player stat: the person playing
	profiles.applyPlayStats(conn.PID, 1, 0, 0, 0)
	// Per-user relation: track that this PID has played this course, so
	// search_courses_played_by(76) can answer correctly. Idempotent.
	profiles.recordPlay(conn.PID, dataID)
	// Maker stat: the owner of the course receives a play
	if m := courses.get(dataID); m != nil {
		profiles.applyMakerReceived(m.OwnerPID, 1, 0, 0, 0)
	}
	fmt.Printf("[SMM2 DataStore] touch_object(22) pid=%d data_id=%d -> play_count++ + player/maker stats + played_set\n",
		conn.PID, dataID)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, nil)
}

// smm2PostPlayResult (96) — UNDOCUMENTED in kinnay/NintendoClients. Called
// by the client right after a play session ends, before the m=70/m=48
// re-fetches that close the loop. The reference (OCW) response is a void
// ack (0 bytes, success=true).
//
// Request body: EXACTLY 51 bytes in both reference captures (verified 18/8 by
// reading the source capture files directly — the body[0]=version(1) +
// body[1:5]=substream_len(46) + 46-byte substream, no trailing byte at all;
// an earlier version of this comment claimed one capture was 52 bytes with a
// trailing 0x00, that was wrong). Layout, verified field-by-field against both
// real captures (both play sessions of OCW course 1000003046):
//
//	substream (46 bytes):
//	  u64  course_id                (1000003046, same both times)
//	  u32  attempt_count?           CONFIRMED VARIES (18/8, controlled test: same
//	                                 course played twice, once cleared on the first
//	                                 try (value=1), once with 2 deliberate deaths
//	                                 added before clearing (value=3) — 2 deaths + 1
//	                                 successful clear = 3 total tries, matches
//	                                 exactly). Much stronger candidate for "attempt
//	                                 count" than the field below ever was.
//	  u32  playtime_ms              CONFIRMED (18/8, cross-checked against an
//	                                 on-screen clear-time screenshot for a DIFFERENT
//	                                 capture: field value 1450 matched the displayed
//	                                 "00:01.450" exactly): MILLISECONDS, not 60fps
//	                                 frames as an earlier version of this comment
//	                                 claimed. Retroactively corrects the two
//	                                 OCW captures too: 0x6BF6=27638ms=~27.6s and
//	                                 0x71FF=29183ms=~29.2s (not 7m41s / 8m06s).
//	  u16  unknown                  (1, both times)
//	  u32  unknown, NOT death_count (13 in FIVE separate captures now, including a
//	                                 CONTROLLED test — 18/8: the exact same course
//	                                 played with 2 deliberately added deaths STILL
//	                                 showed 13 here, identical to a same-course run
//	                                 with no added deaths. Confirmed, not just
//	                                 suspected: this field cannot be a death count.
//	                                 Kept the "?" naming until we find what it
//	                                 actually is.
//	  u64  owner_pid? (=course_id)  (1000003046, both times)
//	  u32  timestamp?               (0x00200A4C vs 0x0025012A — changes per play)
//	  u32  unknown                  (256, both times)
//	  u32  unknown                  (5, both times)
//	  u32  cleared_flag             (1, both times — last u32 of the body)
//
// The two captures give us two data points: playtime + timestamp + the first
// "unknown" u32 change per play, but course_id/owner_pid/the constant-13
// field/the last three u32s stay constant — consistent with those being
// per-course rather than per-play values (or, for the constant-13 field,
// possibly not gameplay data at all — see its note above).
//
// A THIRD, LONGER variant (59 bytes, seen 18/8 from this project's own local
// test courses 1002 and 1003, not OCW) adds two more fields after the same
// 10-field prefix above: the course's numeric ID repeated twice as a STRING
// (e.g. "1002"), with a small 5-byte gap between them (u32=5, u8=1). Real,
// small local test courses without a proper Nintendo-style alphanumeric code
// appear to fall back to the numeric ID as a string here — untested whether a
// real 9-character code (like "PFGYVNN6K") would appear in that same slot for
// an OCW-style course, since neither of the two 51-byte OCW captures we have
// include this suffix at all.
//
// We only trust the fields the client itself will reuse downstream: the
// course_id (first u64 of the substream) and the cleared_flag (the LAST u32
// of the body — fixed position relative to body end, robust to the 1-byte
// length difference between OCW captures). The cleared_flag decides whether
// we bump ClearCount or DeathCount in the catalog — both go through the same
// courses.applyPlayed() / profiles.applyPlayStats() / profiles.applyMakerReceived()
// pipeline used by m=22 and m=133, so all three post-play paths stay consistent.
//
// Owner self-clear detection: we skip the maker-side bump when conn.PID ==
// course.OwnerPID, matching the same self-play guard m=25 already enforces
// (so a maker testing their own course doesn't inflate their own stats).
func smm2PostPlayResult(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	var dataID uint64
	var attempts uint32 // per-session attempt count — see field doc above. Treated as a DELTA
	// (added to the course/player's running AttemptCount via applyPlayed/applyPlayStats), same
	// as plays/clears/deaths — NOT an absolute lifetime total. The confirmed value (1 on a
	// first-try clear, 3 after 2 deaths) is "how many tries THIS session took", so adding it
	// once per post_play_result call is the correct accumulation, not an overwrite.
	var playtimeMs uint32 // CONFIRMED milliseconds (see field doc above) — 18/8, now wired to setCourseTimes
	var cleared uint32    // 0 = died, 1 = cleared (per OCW, the last u32 of the body is the cleared flag)
	if len(req.Body) >= 5 {
		defer func() { recover() }() // tolerate any parse error
		in := nex.NewStreamIn(req.Body, s)
		_ = in.U8() // outer u8 prefix (some clients send a version byte outside the substream)
		sub := in.Substream()
		if sub != nil && sub.Remaining() >= 16 {
			dataID = sub.U64()
			attempts = sub.U32()   // the field confirmed via the controlled 2-death test (see doc above)
			playtimeMs = sub.U32() // confirmed via an on-screen "00:01.450" screenshot match
		} else if sub != nil && sub.Remaining() >= 12 {
			dataID = sub.U64()
			attempts = sub.U32()
		} else if sub != nil && sub.Remaining() >= 8 {
			dataID = sub.U64()
		}
	}
	// Cleared flag is the LAST u32 of the body. Two OCW reference captures
	// (51 and 52 bytes) both have it = 1 (cleared), so the 1-byte length
	// difference doesn't move the field — it lives at body[len-4:len-0].
	if len(req.Body) >= 4 {
		cleared = uint32(req.Body[len(req.Body)-4]) |
			uint32(req.Body[len(req.Body)-3])<<8 |
			uint32(req.Body[len(req.Body)-2])<<16 |
			uint32(req.Body[len(req.Body)-1])<<24
	}
	// Decide which counter to bump. Cleared → +1 play +1 clear. Died → +1
	// play +1 death. Either way, +1 play always (mirrors m=22 / m=133).
	plays, clears, deaths := uint32(1), uint32(0), uint32(0)
	if cleared != 0 {
		clears = 1
	} else {
		deaths = 1
	}
	if dataID != 0 {
		courses.applyPlayed(dataID, plays, clears, attempts, deaths)
		profiles.applyPlayStats(conn.PID, plays, clears, attempts, deaths)
		// Per-user relation: track that this PID has played this course
		// AND (if cleared) was the first clearer. Idempotent on both.
		profiles.recordPlay(conn.PID, dataID)
		if cleared != 0 {
			profiles.recordClear(conn.PID, dataID)
			profiles.recordFirstClear(conn.PID, dataID)
			// World-record time: now that playtimeMs is confirmed real (not a
			// placeholder), record it — setCourseTimes compares against the
			// current holder and only updates if this run is faster (see its
			// own doc for the "first ever clear" vs "actually faster" logic).
			if playtimeMs > 0 {
				courses.setCourseTimes(dataID, conn.PID, playtimeMs)
			}
		}
		if m := courses.get(dataID); m != nil && m.OwnerPID != conn.PID {
			profiles.applyMakerReceived(m.OwnerPID, plays, clears, attempts, deaths)
		}
	}
	verb := "DIED"
	if cleared != 0 {
		verb = "CLEARED"
	}
	fmt.Printf("[SMM2 DataStore] post_play_result(96) pid=%d data_id=%d cleared=0x%x attempts=%d -> +%d play +%d clear +%d death +%d attempt (%s) + player%s\n",
		conn.PID, dataID, cleared, attempts, plays, clears, deaths, attempts, verb,
		func() string {
			if dataID != 0 {
				if m := courses.get(dataID); m != nil && m.OwnerPID == conn.PID {
					return " (self-play, maker stats skipped)"
				}
			}
			return " + maker"
		}())
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, nil)
}

// smm2PlayEvent (104) — UNDOCUMENTED in kinnay/NintendoClients. Called by
// the client twice in the post-play sequence:
//
//  1. Before m=96, with type=0x0101 — "play ended" log entry
//  2. After the m=70/m=48 re-fetch, with type=0x0201 — "play fully done" log entry
//
// Request body (16 bytes, fixed shape):
//
//	[u8=0][u32=11][u64=course_id][u8=0][u16=type]
//
// We don't trust the u32=11 magic to be a length prefix (the SMM2 client
// might emit different sub-types in a future patch that change the
// trailing payload size). Instead, we read the 16 bytes positionally:
// skip 5 bytes, u64 course_id, skip 1 byte, u16 type. The reference (OCW)
// response is a void ack — we don't mutate stats, this is a logging
// event, not a stats update. (Stats live in m=96 / m=22 / m=133.)
func smm2PlayEvent(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	var dataID uint64
	var evType uint16
	if len(req.Body) >= 16 {
		// [u8=0][u32=11][u64=course_id][u8=0][u16=type]
		dataID = uint64(req.Body[5]) | uint64(req.Body[6])<<8 | uint64(req.Body[7])<<16 |
			uint64(req.Body[8])<<24 | uint64(req.Body[9])<<32 | uint64(req.Body[10])<<40 |
			uint64(req.Body[11])<<48 | uint64(req.Body[12])<<56
		evType = uint16(req.Body[14]) | uint16(req.Body[15])<<8
	}
	typeName := "?"
	switch evType {
	case 0x0101:
		typeName = "play_ended"
	case 0x0201:
		typeName = "play_done"
	}
	fmt.Printf("[SMM2 DataStore] play_event(104) pid=%d data_id=%d type=0x%04x (%s) -> ack\n",
		conn.PID, dataID, evType, typeName)
	return nex.NewRMCSuccess(conn.Settings, 0x73, req.Method, req.CallID, nil)
}

// --- m=53/54/55 (search users by course) ----------------------------------
//
// These three fire in sequence when the user opens the "more info" panel of
// a course (confirmed via measured_live.txt for course_id 1000: the client
// sends 53, 54, 55 with IDENTICAL 21-byte request bodies, then 134 for the
// thumbnail URL). Each one returns a list<UserInfo> — the people who played
// / cleared / positive-rated the course.
//
// Request body (21 bytes, real wire, decoded from the user's capture):
//
//	[u8 ver=0]                       (1 byte)
//	[u32 option=0x1000=4096]         (4 bytes) — bitfield, see below
//	[u64 data_id=1000]               (8 bytes)
//	[u32 unk1=0x6224=25124]          (4 bytes) — NOT documented
//	[u32 unk2=0x64=100]              (4 bytes) — "give me up to N" page-size hint?
//
// kinnay's SearchUsersPlayedCourseParam class declares the order as
// [u64 data_id, u32 option, u32 count] — different from the real wire. We go
// with the real wire, since that's what the client actually sends.
//
// option bits (0x1000 = 4096 = bit 12 set): not decoded, just passed through
// to the log line for debugging. A future SMM2 patch may add filter bits
// (region-only, online-only, etc.) and we'd parse them then.
//
// Response (same for all three, kinnay's handle_* methods all use
// `output.list(response, output.add)`):
//
//	[u32 count]
//	[UserInfo] x count               — each as [u8 ver=0][u32 len][body]
//
// syntheticUserInfoFromProfile already produces that exact framing, so the
// helper below just wraps it in the list envelope.

func parseSearchUsersByCourseParam(s *nex.Settings, body []byte) (dataID uint64, option, countHint uint32) {
	defer func() { recover() }() // tolerate any parse error → return all zeros
	if len(body) < 5 {
		return
	}
	in := nex.NewStreamIn(body, s)
	_ = in.U8() // version
	sub := in.Substream()
	if sub == nil || sub.Remaining() < 16 {
		return
	}
	dataID = sub.U64()
	option = sub.U32()    // unknown field: 0x2462 in the OCW capture
	countHint = sub.U32() // result limit: 100 in the OCW capture
	return
}

// writeUserInfoListResponse emits the list<UserInfo> envelope shared by m=53/54/55.
// Unknown PIDs (no registered profile) get a complete-but-empty UserInfo placeholder
// (same pattern smm2GetUsersFromProfiles uses) so the response COUNT matches the
// number of matching PIDs and the client doesn't see a desynced list.
func writeUserInfoListResponse(s *nex.Settings, pids []uint64) []byte {
	out := nex.NewStreamOut(s)
	out.U32(uint32(len(pids)))
	for _, pid := range pids {
		r := profiles.get(pid)
		out.Write(syntheticUserInfoFromProfile(s, pid, r))
	}
	return out.Bytes()
}

// applyCountHint trims pids to at most `hint` entries. hint=0 means "no hint,
// return all". Stable input order is preserved.
func applyCountHint(pids []uint64, hint uint32) []uint64 {
	if hint == 0 || uint32(len(pids)) <= hint {
		return pids
	}
	return pids[:hint]
}

func smm2SearchUsersPlayedCourse(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	dataID, option, hint := parseSearchUsersByCourseParam(s, req.Body)
	pids := applyCountHint(profiles.playersWhoPlayedCourse(dataID), hint)
	body := writeUserInfoListResponse(s, pids)
	fmt.Printf("[SMM2 DataStore] search_users_played_course(53) pid=%d data_id=%d opt=%#x hint=%d -> %d user(s)\n",
		conn.PID, dataID, option, hint, len(pids))
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, body)
}

func smm2SearchUsersClearedCourse(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	dataID, option, hint := parseSearchUsersByCourseParam(s, req.Body)
	pids := applyCountHint(profiles.clearersOfCourse(dataID), hint)
	body := writeUserInfoListResponse(s, pids)
	fmt.Printf("[SMM2 DataStore] search_users_cleared_course(54) pid=%d data_id=%d opt=%#x hint=%d -> %d user(s)\n",
		conn.PID, dataID, option, hint, len(pids))
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, body)
}

func smm2SearchUsersPositiveRatedCourse(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	dataID, option, hint := parseSearchUsersByCourseParam(s, req.Body)
	pids := applyCountHint(profiles.positiveRatersOfCourse(dataID), hint)
	body := writeUserInfoListResponse(s, pids)
	fmt.Printf("[SMM2 DataStore] search_users_positive_rated_course(55) pid=%d data_id=%d opt=%#x hint=%d -> %d user(s)\n",
		conn.PID, dataID, option, hint, len(pids))
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, body)
}
