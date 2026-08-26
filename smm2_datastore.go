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

// smm2EmptyBuilders : par méthode DataStore de contenu, écrit l'enveloppe VIDE valide.
// (courses/users/maps/comments = list<T> vide ; + bool result=true / list<result> vide selon la méthode.)
var smm2EmptyBuilders = map[uint32]func(*nex.StreamOut){
	// NOTE: get_users(48) reste en REPLAY — SMM2 exige un UserInfo valide (son PROPRE profil) au
	// boot, une liste vide casse l'init. Le nettoyer proprement = construire un UserInfo dynamique
	// pour le PID connecté (structure lourde, prochaine étape) au lieu de rejouer la session capturée.
	53:  func(o *nex.StreamOut) { o.U32(0) },                         // search_users_played_course: users[]
	54:  func(o *nex.StreamOut) { o.U32(0) },                         // search_users_cleared_course
	55:  func(o *nex.StreamOut) { o.U32(0) },                         // search_users_positive_rated_course
	70:  func(o *nex.StreamOut) { o.U32(0); o.U32(0) },               // get_courses: courses[], results[]
	71:  func(o *nex.StreamOut) { o.U32(0); o.U32(0); o.Bool(true) }, // point_ranking: courses[], ranks[], result
	74:  func(o *nex.StreamOut) { o.U32(0); o.Bool(true) },           // search_courses_posted_by
	75:  func(o *nex.StreamOut) { o.U32(0) },                         // search_courses_positive_rated_by
	76:  func(o *nex.StreamOut) { o.U32(0) },                         // search_courses_played_by
	80:  func(o *nex.StreamOut) { o.U32(0); o.Bool(true) },           // search_courses_first_clear
	81:  func(o *nex.StreamOut) { o.U32(0); o.Bool(true) },           // search_courses_best_time
	85:  func(o *nex.StreamOut) { o.U32(0); o.U32(0) },               // get_courses_event: courses[], results[]
	86:  func(o *nex.StreamOut) { o.U32(0) },                         // search_courses_event
	160: func(o *nex.StreamOut) { o.U32(0); o.U32(0) },               // get_world_map: maps[], results[]
	162: func(o *nex.StreamOut) { o.U32(0) },                         // search_world_map_pick_up: maps[]

	// --- Méthodes NON documentées (SMM2 3.x) qui peuplent le HUB Course World (Hot/Popular/New) :
	//     structure déduite en parsant les réponses capturées (list<CourseInfo>[+ranks][+bool]).
	//     Ce sont elles qui affichaient les faux niveaux Nintendo -> on les vide aussi.
	72: func(o *nex.StreamOut) { o.U32(0); o.Bool(true) }, // courses[], result
}

// smm2DataStoreHandler : contenu -> VIDE ; sinon -> replay capturé (méthodes structurelles du
// boot que SMM2 exige pour entrer dans Course World). 0x73.8 = NotFound comme Nintendo.
func smm2DataStoreHandler() nex.RMCHandler {
	return func(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
		s := conn.Settings

		// --- Profil dynamique -------------------------------------------------
		// Le chemin d'origine exige userInfoTemplate, une reponse capturee absente de
		// la build publique : sans elle, get_users(48) tombait sur la liste vide, et le
		// commentaire de ce fichier dit deja pourquoi c'est fatal — SMM2 refuse de
		// demarrer sans un UserInfo valide pour SON propre compte.
		//
		// nextendo-nex construit desormais cet UserInfo entierement, sans capture :
		// RegisterUser(47) enregistre le Mii, le nom et le pays que le joueur saisit, et
		// GetUsers(48) les rend. C'est la « prochaine etape » annoncee plus haut dans ce
		// meme fichier, faite le 2026-08-24 en observant une vraie console.
		//
		// La capture reste prioritaire quand elle existe : elle vient d'un vrai serveur
		// et contient des champs qu'on ne sait pas encore remplir.
		if req.Method == 48 && len(userInfoTemplate) > 0 {
			return smm2GetUsers(conn, req)
		}
		// 60 = CanPostCourse : c'est le verrou de la publication. Sa structure (Bool + Uint32)
		// vient de la documentation PretendoNetwork, pas d'une supposition.
		if req.Method == 47 || req.Method == 48 || req.Method == 49 || req.Method == 60 {
			return nex.DataStoreSMM2Handler()(conn, req)
		}
		// sync_user_profile(49): the OWN profile — patch pid + pseudo into the template.
		if req.Method == 49 {
			if tmpl, ok := capturedResponses[replayKey(0x73, 49)]; ok {
				body := patchSyncProfile(s, tmpl, conn.PID, pseudoOr(conn.PID))
				fmt.Printf("[SMM2 DataStore] sync_user_profile(49) -> pseudo Nextendo pid=%d\n", conn.PID)
				return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, body)
			}
		}

		// --- Level storage: real object upload/download on the Nextendo VPS.
		switch req.Method {
		case 24:
			return smm2PreparePostObject(conn, req)
		case 25:
			return smm2PrepareGetObject(conn, req)
		case 26:
			return smm2CompletePostObject(conn, req)
		case 67:
			// CompletePostObjectCourse : meme forme que la 26 generique.
			return smm2CompletePostObject(conn, req)
		case 109:
			// InitEndlessMode : reponse sans corps (ocw-server la declare sans valeur
			// de retour). Plus de sonde ici.
			return smm2InitEndlessMode(conn, req)
		case 131:
			// GetUserOrCourse : la recherche par code de niveau.
			return smm2GetUserOrCourse(conn, req)
		case 79:
			// SearchCoursesEndlessMode : la reserve de Mario sans fin.
			return smm2SearchCoursesEndlessMode(conn, req)
		case 73:
			// SearchCoursesLatest : l'onglet « Nouveautes ».
			return smm2SearchCoursesLatest(conn, req)
		case 58, 83:
			// Onglets classes (Populaires).
			return smm2SearchCoursesRanking(conn, req)
		case 84:
			// SearchCoursesPickUp : « A la une ».
			return smm2SearchCoursesPickUp(conn, req)
		case 52:
			return smm2SearchUsersBattleMode(conn, req)
		case 115:
			return smm2GetEndlessModePlayInfo(conn, req)
		case 153:
			return smm2GetEventCourseStamp(conn, req)
		case 57:
			return smm2SearchUsersClearRanking(conn, req)
		case 59, 152:
			// UpdateLastLoginTime / UpdateLastLoginInfo : rien a rendre.
			return smm2UpdateLastLoginTime(conn, req)
		case 63:
			return smm2GetMiiClothes(conn, req)
		case 65:
			return smm2GetUserNameNgType(conn, req)
		case 69:
			return smm2UpdateCourseTag(conn, req)
		case 82:
			return smm2SearchCoursesFolloweePostedBy(conn, req)
		case 108:
			return smm2GetEndlessModeStatus(conn, req)
		case 125, 129:
			return smm2GetNotifications(conn, req)
		case 154:
			return smm2GetEventCourseStatus(conn, req)
		case 96:
			// PostPlayResult : le resultat d'une partie (tentatives, temps, reussite).
			return smm2PostPlayResult(conn, req)
		case 103:
			// GetDeathPositions : les marques de mort affichees dans le niveau.
			// Reponse : List<DeathPositionInfo>. Une liste vide est ici la VERITE —
			// personne n'est encore mort dans ce niveau sur Nextendo — et non un
			// bouche-trou. On l'ecrit proprement, avec l'en-tete de structure, parce
			// que le repli generique ne le posait pas.
			return smm2GetDeathPositions(conn, req)
		case 88:
			// PreparePostObjectCommentPicture : le descripteur de televersement d'un
			// commentaire dessine. Meme geste que la 132.
			return smm2PreparePostObjectCommentPicture(conn, req)
		case 89, 90:
			// CompletePostObjectCommentPicture : confirmation, et rattachement du dessin
			// au commentaire qui l'attend.
			return smm2CompletePostObjectCommentPicture(conn, req)
		case 94, 95:
			// SearchCommentsInOrder / SearchComments : les commentaires d'un niveau.
			return smm2SearchComments(conn, req)
		case 91:
			// PostCommentText : on l'enregistre, meme si on ne sait pas encore le rendre.
			return smm2PostCommentText(conn, req)
		case 61:
			// CanPostRatingAndComment : posee juste avant de lancer la partie.
			return smm2CanPostRatingAndComment(conn, req)
		case 134:
			// GetReqGetInfoHeadersInfo : en-tetes de telechargement. Demande juste avant
			// de jouer un niveau.
			return smm2GetReqGetInfoHeadersInfo(conn, req)
		case 70:
			// GetCourses : le jeu s'en sert pour afficher le code juste apres publication.
			return smm2GetCourses(conn, req)
		case 74:
			// SearchCoursesPostedBy : les niveaux du createur.
			return smm2SearchCoursesPostedBy(conn, req)
		case 68:
			// CompletePostObjectsCourse : c'est CELLE-CI que SMM2 appelle pour clore la
			// publication, et c'est elle qui manquait — d'ou des niveaux complets sur le
			// disque mais jamais marques prets.
			return smm2CompletePostObjectsCourse(conn, req)
		case 66:
			// Course level-data upload prep: replay the measured S3 descriptor with the
			// bucket host rewritten to our object store.
			if tmpl, ok := capturedResponses[replayKey(0x73, 66)]; ok {
				body := rewriteUploadHost(tmpl)
				fmt.Printf("[SMM2 Storage] upload-prep 0x73.66 (données niveau) -> URL réécrite (%do)\n", len(body))
				return nex.NewRMCSuccess(s, 0x73, 66, req.CallID, body)
			}
			// Sans capture — le cas de la build publique — on CONSTRUIT la reponse au
			// lieu d'abandonner. La structure du parametre vient de la documentation
			// PretendoNetwork, celle de la reponse est la meme que pour la 24.
			return smm2PreparePostObjectCourse(conn, req)
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
			out := nex.NewStreamOut(s)
			out.U32(0)
			// On imprime le corps BRUT de la requete. Les methodes qui restent — 96
			// (PostPlayResult), 91/92 (PostCommentText/Stamp) — n'ont AUCUNE section
			// dans la documentation PretendoNetwork : deviner leur forme serait
			// inventer. Ces octets-la viennent d'une vraie console et sont la seule
			// source honnete dont on dispose.
			if len(req.Body) > 0 {
				n := len(req.Body)
				if n > 160 {
					n = 160
				}
				fmt.Printf("[SMM2 DataStore] 0x73.%d corps brut len=%d: %x\n", req.Method, len(req.Body), req.Body[:n])
			}
			fmt.Printf("[SMM2 DataStore] UNCAPTURED 0x73.%d call=%d -> empty-list fallback\n", req.Method, req.CallID)
			return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
		}
		fmt.Printf("[SMM2 DataStore] 0x73.%d -> replay structurel (%do)\n", req.Method, len(body))
		return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, body)
	}
}
