package main

import (
	"fmt"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

// Les methodes restantes du protocole 0x73 : profil, notifications, derniere connexion,
// classements. Aucune n'empeche de jouer — elles tombaient jusqu'ici sur le repli
// « liste vide », et le jeu s'en accommodait. Ce qui suit remplace ce hasard par des
// reponses de la bonne FORME.
//
// Cette distinction compte. Pour la moitie de ces methodes, la liste vide etait deja la
// bonne reponse, et le code ci-dessous ne fait que l'ecrire explicitement. Pour l'autre
// moitie — 59, 65, 108, 152, 154 — le repli renvoyait quatre octets la ou le jeu attend
// autre chose, ou rien du tout. Ce sont celles-la qui valaient le detour.
//
// SOURCES. 57, 63, 65, 69, 82, 108 et 154 ont une section dans la documentation
// PretendoNetwork et leur forme en vient. 125, 129 et 152 n'en ont AUCUNE : seul leur
// nom est connu. Pour ces trois-la, la reponse est un choix prudent, signale comme tel
// a chaque fois, et non une specification.

// vide ecrit une liste vide (le compte, et rien apres).
func listeVide(o *nex.StreamOut) { o.U32(0) }

// mapVide ecrit une Map<Uint8, Uint32> vide.
func mapVide(o *nex.StreamOut) { o.U32(0) }

// succesVide : la reponse des methodes qui ne renvoient rien du tout.
//
// La documentation les donne avec une section « Response » vierge. Le repli generique,
// lui, ajoutait un Uint32 a zero — quatre octets de trop. Le jeu les ignorait, mais
// c'est le genre d'approximation qui finit par coincider avec un vrai champ.
func succesVide(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	return nex.NewRMCSuccess(conn.Settings, 0x73, req.Method, req.CallID, nil)
}

// smm2UpdateLastLoginTime (59) et smm2UpdateLastLoginInfo (152) : le jeu signale qu'il
// vient de se connecter.
//
// La 59 est documentee : ni requete ni reponse. La 152 ne l'est PAS ; sa requete, lue
// sur une vraie console, est une structure contenant un seul Uint32
// (`00 04000000 00000000`). On la traite comme sa jumelle documentee, ce qui est une
// deduction et pas une certitude — mais une deduction etroite : meme famille, meme nom,
// meme absence de donnees a rendre.
func smm2UpdateLastLoginTime(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	fmt.Printf("[SMM2 Divers] update_last_login(%d) pid=%d\n", req.Method, conn.PID)
	return succesVide(conn, req)
}

// smm2GetUserNameNgType (65) : le pseudo du joueur est-il signale comme inapproprie ?
//
// Reponse : un seul Uint8. Le repli en renvoyait quatre — mauvaise taille pour un champ
// que le jeu lit vraiment. Zero signifie « rien a signaler », ce qui est exact : Nextendo
// ne filtre pas les pseudonymes.
func smm2GetUserNameNgType(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	out := nex.NewStreamOut(s)
	out.U8(0)
	fmt.Printf("[SMM2 Divers] get_user_name_ng_type(65) pid=%d -> 0\n", conn.PID)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}

// smm2GetMiiClothes (63) : les tenues de Mii disponibles.
//
// Liste vide. Nextendo ne distribue pas de catalogue de vetements, et en inventer un
// ferait apparaitre dans le jeu des tenues qui n'existent nulle part.
func smm2GetMiiClothes(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	out := nex.NewStreamOut(s)
	listeVide(out)
	fmt.Printf("[SMM2 Divers] get_mii_clothes(63) pid=%d -> 0\n", conn.PID)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}

// smm2GetNotifications (125 GetNewNotification, 129 GetNgCourseNotification).
//
// Ni l'une ni l'autre n'est documentee. Leur nom dit « notification » et le repli en
// liste vide fonctionnait deja — le jeu tourne avec depuis le debut. On garde donc la
// liste vide, mais en la posant volontairement plutot qu'en la laissant tomber d'un
// repli generique, et en le journalisant. C'est aussi la verite du moment : Nextendo
// n'a pas de systeme de notifications, ni de moderation de niveaux.
func smm2GetNotifications(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	out := nex.NewStreamOut(s)
	listeVide(out)
	fmt.Printf("[SMM2 Divers] notifications(%d) pid=%d -> 0 (non documente, liste vide assumee)\n", req.Method, conn.PID)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}

// smm2GetEventCourseStatus (154) : l'etat du niveau evenementiel en cours.
//
// EventCourseStatusInfo = Uint64, Bool, DateTime. Le repli renvoyait un Uint32 : forme
// entierement differente. Il n'y a pas d'evenement sur Nextendo, d'ou le faux et les
// zeros — mais dans la bonne enveloppe.
func smm2GetEventCourseStatus(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	corps := nex.NewStreamOut(s)
	corps.U64(0)
	corps.Bool(false)
	corps.U64(0) // DateTime
	fmt.Printf("[SMM2 Divers] get_event_course_status(154) pid=%d -> aucun evenement\n", conn.PID)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, frameStruct(s, 0, corps.Bytes()))
}

// smm2GetEndlessModeStatus (108) : la configuration et l'etat des quatre difficultes.
//
// Structure prise du serveur d'Open Course World (ocw-server,
// nex/datastore/datastore_smm2.go), trouve par mario638 :
//
//	EndlessModeStatus :
//	  Map<Uint8, EndlessModeConfig>     une entree par difficulte, cles 0 a 3
//	  Map<Uint8, EndlessModeRunState>   idem
//
//	EndlessModeConfig   : Mode u8 · Coins u8 · PointsScore u32 · DateTime · DateTime ·
//	                      Lives u8 · StartLives u8
//	EndlessModeRunState : Lives u8 · Clears u32
//
// DEUX TABLES VIDES NE SUFFISENT PAS, comme pour la 115 : le jeu a besoin des quatre
// cles pour proposer les quatre difficultes. Les VALEURS, elles, sont celles d'un joueur
// qui n'a jamais joue — c'est exact chez nous — sauf StartLives, qui est une regle du
// jeu et vaut 5, 10, 15, 30 selon la difficulte (releve dans endless_type_normal.go).
func smm2GetEndlessModeStatus(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	etat := endless.etatDe(conn.PID)

	imbrique := func(o *nex.StreamOut, f func(*nex.StreamOut)) {
		inner := nex.NewStreamOut(s)
		f(inner)
		if s.StructHeader {
			o.U8(0)
			o.Buffer(inner.Bytes())
		} else {
			o.Write(inner.Bytes())
		}
	}

	corps := nex.NewStreamOut(s)

	// Config, une entree par difficulte.
	corps.U32(4)
	for d := uint8(0); d < 4; d++ {
		corps.U8(d)
		e := etat[d]
		// Si aucune partie n'est en cours, les vies valent les vies INITIALES et non
		// zero. C'est la ligne exacte d'ocw-server, et c'est ce qui nous bloquait : on
		// annoncait quatre difficultes sans une seule vie.
		vies := e.Vies
		if e.Mode != 2 {
			vies = viesInitialesEndless[d]
		}
		imbrique(corps, func(o *nex.StreamOut) {
			o.U8(e.Mode)
			o.U8(e.Pieces)
			o.U32(e.PointsScore)
			// PAS ZERO. ocw-server ecrit DateTimeFromTimestamp(0), c'est-a-dire la date
			// du 1er janvier 1970 EMPAQUETEE — un grand nombre, pas un zero. Un
			// DateTime NEX est un champ de bits ; zero n'y designe aucune date valide.
			// C'est la meme confusion qui affichait « 06/10/0026 » sur les niveaux.
			o.U64(dateTimeEpoque)
			o.U64(dateTimeEpoque)
			o.U8(vies)
			o.U8(viesInitialesEndless[d])
		})
	}

	// State, une entree par difficulte.
	corps.U32(4)
	for d := uint8(0); d < 4; d++ {
		corps.U8(d)
		e := etat[d]
		vies := e.Vies
		if e.Mode != 2 {
			vies = viesInitialesEndless[d]
		}
		imbrique(corps, func(o *nex.StreamOut) {
			o.U8(vies)
			o.U32(e.Reussites)
		})
	}

	fmt.Printf("[SMM2 Divers] get_endless_mode_status(108) pid=%d -> modes=[%d %d %d %d] vies=[%d %d %d %d]\n",
		conn.PID, etat[0].Mode, etat[1].Mode, etat[2].Mode, etat[3].Mode,
		viesOu(etat[0], 0), viesOu(etat[1], 1), viesOu(etat[2], 2), viesOu(etat[3], 3))
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, frameStruct(s, 0, corps.Bytes()))
}

// smm2InitEndlessMode (109) : le joueur demarre une partie.
//
// Requete : struct{ Uint8 difficulte } — ce que nous avions mesure.
// Reponse : AUCUN CORPS. Dans ocw-server la methode est declaree
// `InitEndlessMode func(param InitEndlessModeParam) error` : elle ne rend aucune valeur.
//
// On avait balaye neuf enveloppes ici sans succes, dont celle-ci. Le refus ne venait pas
// de cette reponse : il venait de la 115 et de la 108, deux appels plus tot, qui
// rendaient des tables vides la ou le jeu attend quatre difficultes. Chercher le defaut
// la ou l'erreur s'affiche plutot que la ou elle nait — c'est la lecon de la nuit.
func smm2InitEndlessMode(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	in := nex.NewStreamIn(req.Body, s)
	p := in
	if s.StructHeader {
		_ = in.U8()
		p = in.Substream()
	}
	difficulte := p.U8()

	endless.demarrer(conn.PID, difficulte)
	fmt.Printf("[SMM2 Divers] init_endless_mode(109) pid=%d difficulte=%d -> partie active, %d vies\n",
		conn.PID, difficulte, viesInitialesEndless[difficulte%4])
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, nil)
}

// smm2SearchUsersClearRanking (57) : le classement des joueurs.
//
// Reponse : List<UserInfo>, List<Uint32>, Bool. Vide, vide, faux — il n'y a pas encore
// de classement a montrer, et le fabriquer afficherait des joueurs inexistants.
func smm2SearchUsersClearRanking(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	out := nex.NewStreamOut(s)
	listeVide(out) // utilisateurs
	listeVide(out) // Uint32 associes
	out.Bool(false)
	fmt.Printf("[SMM2 Divers] search_users_clear_ranking(57) pid=%d -> 0\n", conn.PID)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}

// smm2SearchCoursesFolloweePostedBy (82) : les niveaux des createurs suivis.
//
// Reponse : List<CourseInfo>, Bool. Nextendo n'a pas encore de systeme d'abonnements,
// donc personne ne suit personne : liste vide.
func smm2SearchCoursesFolloweePostedBy(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	out := nex.NewStreamOut(s)
	listeVide(out)
	out.Bool(false)
	fmt.Printf("[SMM2 Divers] search_courses_followee(82) pid=%d -> 0\n", conn.PID)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}

// smm2UpdateCourseTag (69) : le createur change les etiquettes de son niveau.
//
// Requete : Uint64 dataId, Uint8 tagId1, Uint8 tagId2. Pas de reponse.
//
// Celle-ci fait un vrai travail : elle ECRIT les nouvelles etiquettes dans le catalogue.
// Repondre « d'accord » sans rien changer aurait affiche la modification a l'ecran et
// l'aurait perdue au rechargement suivant — un mensonge qui ne se decouvre que plus tard.
//
// On verifie que le demandeur est bien le proprietaire. Le jeu ne propose pas de modifier
// le niveau d'autrui, mais un serveur ne doit pas dependre de la bonne conduite du client.
func smm2UpdateCourseTag(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	in := nex.NewStreamIn(req.Body, s)
	p := in
	if s.StructHeader {
		_ = in.U8()
		p = in.Substream()
	}
	dataID := p.U64()
	tag1 := p.U8()
	tag2 := p.U8()

	m := courses.get(dataID)
	if m == nil {
		fmt.Printf("[SMM2 Divers] update_course_tag(69) data_id=%d introuvable\n", dataID)
		return nex.NewRMCError(s, 0x73, req.CallID, 0x80690004) // DataStore::NotFound
	}
	if m.OwnerPID != conn.PID {
		fmt.Printf("[SMM2 Divers] update_course_tag(69) pid=%d n'est pas proprietaire de %d (=%d) — refuse\n",
			conn.PID, dataID, m.OwnerPID)
		return nex.NewRMCError(s, 0x73, req.CallID, 0x80690001) // DataStore::PermissionDenied
	}

	courses.setTags(dataID, tag1, tag2)
	fmt.Printf("[SMM2 Divers] update_course_tag(69) pid=%d data_id=%d -> %d,%d\n", conn.PID, dataID, tag1, tag2)
	return succesVide(conn, req)
}

// smm2SearchUsersBattleMode (52) : le classement du mode versus.
//
// Meme reponse que la 57 — List<UserInfo>, List<Uint32>, Bool — d'apres la
// documentation PretendoNetwork. C'est elle qui manquait a l'ecran Classements : 57 et
// 58 repondaient deja, 52 tombait sur le repli, et le client rejetait l'ecran entier.
func smm2SearchUsersBattleMode(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	out := nex.NewStreamOut(s)
	listeVide(out) // utilisateurs
	listeVide(out) // Uint32 associes
	out.Bool(false)
	fmt.Printf("[SMM2 Divers] search_users_battle_mode(52) pid=%d -> 0\n", conn.PID)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}

// smm2GetEventCourseStamp (153) : le nombre de tampons d'evenement.
//
// Un seul Uint32. Le repli generique renvoyait deja exactement cela — cette methode
// n'etait donc PAS cassee. On l'ecrit explicitement pour ne pas dependre d'une
// coincidence, mais il ne faut pas s'attendre a un changement a l'ecran.
func smm2GetEventCourseStamp(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	out := nex.NewStreamOut(s)
	out.U32(0)
	fmt.Printf("[SMM2 Divers] get_event_course_stamp(153) pid=%d -> 0\n", conn.PID)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}

// smm2GetEndlessModePlayInfo (115) : l'etat des parties de Mario sans fin.
//
// STRUCTURE EXACTE, plus une supposition. Elle vient du serveur d'Open Course World —
// forge.unstable.systems/ocw/ocw-server, nex/datastore/datastore_smm2.go — trouve par
// mario638 le 2026-08-24. Le depot est sur un Forgejo prive, ce qui explique qu'une
// recherche sur GitHub n'ait rendu que le mod client : j'en avais conclu, a tort, que le
// serveur de TGR etait ferme.
//
//	EndlessModePlayInfo :
//	  Uint32 4                          <- taille de la table
//	  Uint8 0 + List<Item>              facile
//	  Uint8 1 + List<Item>              normal
//	  Uint8 2 + List<Item>              expert
//	  Uint8 3 + List<Item>              super expert
//
// CE N'EST PAS UNE LISTE, C'EST UNE TABLE A QUATRE ENTREES. Nous rendions une liste vide
// encapsulee ; la console l'ACCEPTAIT — l'ecran s'ouvrait — mais elle se retrouvait sans
// une seule difficulte a proposer, et InitEndlessMode(109) n'aboutissait jamais. Le mur
// n'etait donc pas la 109 : c'etait la 115, un appel plus tot, mal formee d'une facon
// que rien ne signalait.
//
// Les quatre listes sont vides et c'est exact : personne n'a de partie en cours chez
// nous. Ce sont les CLES qui manquaient, pas les valeurs.
func smm2GetEndlessModePlayInfo(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings

	corps := nex.NewStreamOut(s)
	corps.U32(4) // quatre difficultes
	for cle := uint8(0); cle < 4; cle++ {
		corps.U8(cle)
		corps.U32(0) // aucune partie en cours a cette difficulte
	}

	e := endless.etatDe(conn.PID)
	fmt.Printf("[SMM2 Divers] get_endless_mode_play_info(115) pid=%d -> 4 difficultes, modes=[%d %d %d %d]\n",
		conn.PID, e[0].Mode, e[1].Mode, e[2].Mode, e[3].Mode)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, frameStruct(s, 0, corps.Bytes()))
}

// dateTimeEpoque : le 1er janvier 1970 au format DateTime de NEX. ocw-server emploie
// cette valeur la ou nous mettions zero.
var dateTimeEpoque = uint64(nex.MakeDateTime(1970, 1, 1, 0, 0, 0))

// viesOu rend les vies a afficher : celles de la partie si elle est active, sinon les
// vies initiales de la difficulte.
func viesOu(e partieEndless, difficulte int) uint8 {
	if e.Mode == 2 {
		return e.Vies
	}
	return viesInitialesEndless[difficulte]
}
