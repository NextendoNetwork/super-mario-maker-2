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
	70: func(o *nex.StreamOut) { o.U32(0); o.U32(0) },            // get_courses: courses[], results[]
	71: func(o *nex.StreamOut) { o.U32(0); o.U32(0); o.Bool(true) }, // point_ranking: courses[], ranks[], result
	73: func(o *nex.StreamOut) { o.U32(0); o.Bool(true) },        // search_courses_latest: courses[], result
	74: func(o *nex.StreamOut) { o.U32(0); o.Bool(true) },        // search_courses_posted_by
	75: func(o *nex.StreamOut) { o.U32(0) },                      // search_courses_positive_rated_by
	76: func(o *nex.StreamOut) { o.U32(0) },                      // search_courses_played_by
	79: func(o *nex.StreamOut) { o.U32(0) },                      // search_courses_endless_mode
	80: func(o *nex.StreamOut) { o.U32(0); o.Bool(true) },        // search_courses_first_clear
	81: func(o *nex.StreamOut) { o.U32(0); o.Bool(true) },        // search_courses_best_time
	85: func(o *nex.StreamOut) { o.U32(0); o.U32(0) },            // get_courses_event: courses[], results[]
	86: func(o *nex.StreamOut) { o.U32(0) },                      // search_courses_event
	94: func(o *nex.StreamOut) { o.U32(0); o.Bool(true) },        // search_comments_in_order: comments[], result
	95: func(o *nex.StreamOut) { o.U32(0) },                      // search_comments
	160: func(o *nex.StreamOut) { o.U32(0); o.U32(0) },           // get_world_map: maps[], results[]
	162: func(o *nex.StreamOut) { o.U32(0) },                     // search_world_map_pick_up: maps[]

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
			if len(userInfoTemplate) > 0 {
				return smm2GetUsers(conn, req)
			}
			// Fallback: return empty user list + empty result list (stub for public build)
			out := nex.NewStreamOut(s)
			out.U32(0)   // list<UserInfo> count = 0
			out.U32(0)   // list<result> count = 0
			fmt.Printf("[SMM2 DataStore] get_users(48) -> empty fallback (no template)\n")
			return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
		}
		// sync_user_profile(49): the OWN profile — patch pid + pseudo into the template.
		if req.Method == 49 {
			if tmpl, ok := capturedResponses[replayKey(0x73, 49)]; ok {
				body := patchSyncProfile(s, tmpl, conn.PID, pseudoOr(conn.PID))
				fmt.Printf("[SMM2 DataStore] sync_user_profile(49) -> pseudo Nextendo pid=%d\n", conn.PID)
				return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, body)
			}
			// Fallback: return empty profile (stub for public build without templates)
			out := nex.NewStreamOut(s)
			out.U32(0) // Empty struct/result
			fmt.Printf("[SMM2 DataStore] sync_user_profile(49) -> empty fallback (no template)\n")
			return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
		}
		// post_relation_data(47): save relation data - expect bool result (true = success)
		if req.Method == 47 {
			// DEBUG: Parse the payload to understand structure
			fmt.Printf("[SMM2 DataStore] post_relation_data(47) received %d bytes\n", len(req.Body))
			if len(req.Body) > 0 {
				fmt.Printf("[SMM2 DataStore] Payload (hex): %x\n", req.Body)
				in := nex.NewStreamIn(req.Body, s)
				// Try to parse as Mii data
				relationType := in.U32() // relation type?
				fmt.Printf("[SMM2 DataStore] Parsed U32(0): %d\n", relationType)
				if in.Remaining() > 0 {
					nextU32 := in.U32()
					fmt.Printf("[SMM2 DataStore] Parsed U32(1): %d\n", nextU32)
				}
			}
			out := nex.NewStreamOut(s)
			out.Bool(true) // Success
			fmt.Printf("[SMM2 DataStore] post_relation_data(47) -> responding with true\n")
			return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
		}
		// get_ranking_by_pid(154): try single U32 only
		if req.Method == 154 {
			out := nex.NewStreamOut(s)
			out.U32(0) // Just empty count
			fmt.Printf("[SMM2 DataStore] get_ranking_by_pid(154) -> U32(0) only\n")
			return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
		}

		// --- Level storage: real object upload/download on the Nextendo VPS.
		switch req.Method {
		case 24:
			return smm2PreparePostObject(conn, req)
		case 25:
			return smm2PrepareGetObject(conn, req)
		case 26:
			return smm2CompletePostObject(conn, req)
		case 66:
			// Course level-data upload prep: replay the measured S3 descriptor with the
			// bucket host rewritten to our object store.
			if tmpl, ok := capturedResponses[replayKey(0x73, 66)]; ok {
				body := rewriteUploadHost(tmpl)
				fmt.Printf("[SMM2 Storage] upload-prep 0x73.66 (données niveau) -> URL réécrite (%do)\n", len(body))
				return nex.NewRMCSuccess(s, 0x73, 66, req.CallID, body)
			}
		case 132:
			// Relation-data upload prep (thumbnails + clear-check): per-type descriptor.
			return smm2PrepareRelationUpload(conn, req)
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
