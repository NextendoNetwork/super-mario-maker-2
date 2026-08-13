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
	// 70 (get_courses): REVERTED to empty. A real successful upload session (captured before
	// smm2GetCourses existed) showed the client sail through 68->70(empty)->69 without any
	// retry loop or "Upload failed" — the full CourseInfo response we tried instead produced
	// the same fixed 8x-retry-then-fail pattern regardless of what we put in it. The
	// course's shareable code still needs a real home; it isn't this response.
	70: func(o *nex.StreamOut) { o.U32(0); o.U32(0) },            // get_courses: courses[], results[]
	71: func(o *nex.StreamOut) { o.U32(0); o.U32(0); o.Bool(true) }, // point_ranking: courses[], ranks[], result
	73: func(o *nex.StreamOut) { o.U32(0); o.Bool(true) },        // search_courses_latest: courses[], result
	74: func(o *nex.StreamOut) { o.U32(0); o.Bool(true) },        // search_courses_posted_by
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
			// CompletePostObjectsCourse: per spec, no return value. Reverted to a plain
			// ack — a real successful upload session (before we added smm2GetCourses'
			// rich response for method 70) showed this exact sequence completing fine:
			// 68 -> ack, 70 -> EMPTY (fell through to smm2EmptyBuilders), 69 -> ack, done.
			// Returning a full CourseInfo from 68 didn't help either (same retry loop).
			return smm2CompletePostObjectsCourseAck(conn, req)
		case 69:
			// UpdateCourseTag: per spec, no return value.
			return smm2UpdateCourseTag(conn, req)
		case 132:
			// Relation-data upload prep (thumbnails + clear-check): a fresh
			// RelationObjectReqPostInfo per call, built from the documented shape.
			return smm2PrepareRelationUpload(conn, req)
		case 133:
			// CompletePostRelationObject: undocumented in detail, acked like its siblings.
			return smm2CompletePostRelationObject(conn, req)
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
