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
	53: func(o *nex.StreamOut) { o.U32(0) },                      // search_users_played_course: users[]
	54: func(o *nex.StreamOut) { o.U32(0) },                      // search_users_cleared_course
	55: func(o *nex.StreamOut) { o.U32(0) },                      // search_users_positive_rated_course
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
	75: func(o *nex.StreamOut) { o.U32(0) },                      // search_courses_positive_rated_by
	76: func(o *nex.StreamOut) { o.U32(0) },                      // search_courses_played_by
	79: func(o *nex.StreamOut) { o.U32(0) },                      // search_courses_endless_mode
	80: func(o *nex.StreamOut) { o.U32(0); o.Bool(true) },        // search_courses_first_clear
	81: func(o *nex.StreamOut) { o.U32(0); o.Bool(true) },        // search_courses_best_time
	82: func(o *nex.StreamOut) { o.U32(0); o.Bool(true) },        // search_courses_followee_posted_by: courses[], result — confirmed via measured_live.txt: fell to NotFound (method=0 in the S->C log) since it was missing from this map
	85: func(o *nex.StreamOut) { o.U32(0); o.U32(0) },            // get_courses_event: courses[], results[]
	86: func(o *nex.StreamOut) { o.U32(0) },                      // search_courses_event
	94: func(o *nex.StreamOut) { o.U32(0); o.Bool(true) },        // search_comments_in_order: comments[], result
	95: func(o *nex.StreamOut) { o.U32(0) },                      // search_comments
	160: func(o *nex.StreamOut) { o.U32(0); o.U32(0) },           // get_world_map: maps[], results[]
	162: func(o *nex.StreamOut) { o.U32(0) },                     // search_world_map_pick_up: maps[]

	// (103) get_death_positions: data_id:int -> list[DeathPositionInfo]. DOCUMENTED
	// (nintendoclients.readthedocs.io) but never implemented — fell through to
	// NotFound. New lead from the user: the "Uploaded Courses" view (your OWN
	// courses) shows a "View Deaths" button that "New Courses" (other players')
	// doesn't — the client may eagerly query this for owner-only courses while
	// building that list/detail view, and an unimplemented NotFound there could be
	// exactly what crashes rendering (matches the reported "spinner then instant
	// fail, nothing shown" symptom). We have no death-position data to report, so
	// an empty list is the correct honest answer regardless.
	103: func(o *nex.StreamOut) { o.U32(0) },                     // get_death_positions: list<DeathPositionInfo>

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

		// (63, 65, 129) — NOT documented by kinnay/NintendoClients (link-less in the method
		// table, or not indexed at all). All three arrive with len=0 (no parameters) right in
		// the middle of otherwise-working sessions in measured_live.txt, and every OTHER
		// parameterless method in this protocol we've confirmed (59, 68, 69, 133) turned out to
		// have "no return value" — acking them the same way is the best-founded guess available
		// right now, not a shot in the dark. If the client still resets the Mii after this,
		// these three are ruled out and the search moves elsewhere.
		if req.Method == 63 || req.Method == 65 || req.Method == 129 {
			fmt.Printf("[SMM2 DataStore] method %d pid=%d -> ack (sin params, patrón \"sin retorno\", no verificado)\n", req.Method, conn.PID)
			return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, nil)
		}

		// (61) PrepareGetRelationObject: the download-side counterpart of m=132. Client
		// sends [u64 dataID, u32 relType] and expects a RelationObjectReqGetInfo back
		// (kinnay/datastore_smm2.py:2544: {url, data_type, size, unk, filename}) pointing
		// at /relation/<id>/<relType>. We were returning a void ack — the request shape
		// was missing entirely, so the client couldn't learn where the relation object
		// lives and disconnected ~8s later. Real shapes (kinnay spec, not reference) confirmed
		// via the request bytes captured during a play attempt (callID=52, body=17).
		if req.Method == 61 {
			return smm2PrepareGetRelationObject(conn, req)
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

// smm2GetDeathPositions (103) — kinnay-indexed as "GET_DEATH_POSITIONS" (for
// retrieving the death positions recorded for a course). In the reference play
// flow the request is [u8 ver=0][u32 substream][u64 courseID] and the response
// is u32(0) — i.e. "no deaths recorded for this course yet". A real response
// would be a list of {x, y} world-coordinate death positions; for a private
// server with no recorded deaths, u32(0) (empty list) is correct.
func smm2GetDeathPositions(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	in := nex.NewStreamIn(req.Body, s)
	_ = in.U8()
	sub := in.Substream()
	courseID := sub.U64()
	out := nex.NewStreamOut(s)
	out.U32(0) // empty list of death positions
	fmt.Printf("[SMM2 DataStore] get_death_positions(103) pid=%d course_id=0x%x -> empty list\n",
		conn.PID, courseID)
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
	courses.applyPlayed(dataID, 1, 0, 0, 0) // +1 play
	fmt.Printf("[SMM2 DataStore] touch_object(22) pid=%d data_id=%d -> play_count++ (ack)\n",
		conn.PID, dataID)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, nil)
}
