package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

// Les recherches de Course World : Nouveautes, Populaires, A la une.
//
// POURQUOI ELLES ETAIENT VIDES. Ce n'etait pas une panne. Kazu les avait deliberement
// neutralisees dans smm2EmptyBuilders : leurs reponses venaient de captures faites sur
// les vrais serveurs Nintendo, et les rejouer faisait apparaitre dans Nextendo des
// niveaux Nintendo qui n'existent pas chez nous — cliquables, et introuvables ensuite.
// Une liste vide valait mieux qu'un catalogue fantome.
//
// Cette raison a disparu : on a maintenant un catalogue reel. Ces methodes servent donc
// NOS niveaux, et la neutralisation ne s'applique plus qu'a ce qu'on ne sait pas encore
// produire.
//
// LA PAGINATION EST REELLE. Le jeu envoie un ResultRange (depart + nombre) et fait
// defiler. L'ignorer marche tant qu'il y a moins d'une page de niveaux — c'est-a-dire
// exactement pendant les essais, et plus jamais ensuite. On l'applique tout de suite.

// niveauxPublics rend les niveaux publiables, du plus recent au plus ancien.
//
// On ecarte les fichiers rattaches (miniatures, rediffusions) : ils partagent le
// catalogue avec les niveaux et n'ont rien a faire dans une liste de niveaux.
func niveauxPublics() []*courseMeta {
	var liste []*courseMeta
	courses.mu.Lock()
	for _, m := range courses.byID {
		if m.Ready && !estFichierRattache(m.Name) {
			liste = append(liste, m)
		}
	}
	courses.mu.Unlock()
	// Tri du plus recent au plus ancien, DEPARTAGE PAR data_id. Sans cette seconde cle,
	// deux niveaux publies dans la meme seconde changeaient d'ordre d'un appel a l'autre —
	// l'ordre de parcours d'une carte Go est volontairement aleatoire.
	//
	// Sur Course World cela ne se voyait pas. En COOPERATIF c'est fatal : les deux joueurs
	// demandent le niveau chacun de leur cote et doivent recevoir LE MEME. Troisieme fois
	// que l'ordre d'une carte Go mord ce depot en une nuit.
	sort.Slice(liste, func(i, j int) bool {
		if liste[i].CreatedAt != liste[j].CreatedAt {
			return liste[i].CreatedAt > liste[j].CreatedAt
		}
		return liste[i].DataID > liste[j].DataID
	})
	return liste
}

// trancher applique le ResultRange demande par le client.
func trancher(liste []*courseMeta, depart, nombre uint32) []*courseMeta {
	if depart >= uint32(len(liste)) {
		return nil
	}
	liste = liste[depart:]
	if nombre > 0 && nombre < uint32(len(liste)) {
		liste = liste[:nombre]
	}
	return liste
}

// parametreRecherche lit le debut commun a ces requetes : le masque des champs
// facultatifs, puis le ResultRange. Le reste des criteres (style, theme, difficulte)
// est volontairement ignore : les filtrer exigerait d'interpreter l'en-tete binaire du
// niveau, qu'on stocke sans la lire. Mieux vaut rendre tous les niveaux que d'en
// masquer au hasard.
func parametreRecherche(s *nex.Settings, corps []byte) (options, depart, nombre uint32) {
	in := nex.NewStreamIn(corps, s)
	p := in
	if s.StructHeader {
		_ = in.U8()
		p = in.Substream()
	}
	options = p.U32()
	if s.StructHeader {
		_ = p.U8()
		rr := p.Substream()
		depart = rr.U32()
		nombre = rr.U32()
	} else {
		depart = p.U32()
		nombre = p.U32()
	}
	return
}

// repondreNiveaux ecrit une liste de CourseInfo, avec ou sans le tableau de rangs que
// certaines de ces methodes attendent en plus.
func repondreNiveaux(conn *nex.Connection, req *nex.RMCMessage, nom string, avecRangs bool) *nex.RMCMessage {
	s := conn.Settings
	options, depart, nombre := parametreRecherche(s, req.Body)

	liste := trancher(niveauxPublics(), depart, nombre)

	// Meme garde que pour les methodes 70 et 74 : sans SMM2_COURSEINFO=1, on ne sert
	// aucune fiche. Ecrire une CourseInfo mal formee ne casse pas que la liste — le
	// client rejette la reponse entiere, et l'ecran affiche une erreur de connexion la
	// ou une liste vide n'aurait rien casse. L'interrupteur existe parce que c'est
	// exactement ce qui est arrive la premiere fois.
	if os.Getenv("SMM2_COURSEINFO") != "1" {
		liste = nil
	}

	out := nex.NewStreamOut(s)
	out.U32(uint32(len(liste)))
	for _, m := range liste {
		ecrireCourseInfo(out, m, options)
	}
	if avecRangs {
		// Un rang par niveau, dans l'ordre rendu. On ne classe pas encore : le rang est
		// la position dans la liste, ce qui est honnete pour « Nouveautes » et
		// approximatif pour « Populaires ».
		out.U32(uint32(len(liste)))
		for i := range liste {
			out.U32(uint32(i + 1))
		}
	}
	out.Bool(true)

	fmt.Printf("[SMM2 Courses] %s(%d) pid=%d options=0x%x depart=%d nombre=%d -> %d niveau(x)\n",
		nom, req.Method, conn.PID, options, depart, nombre, len(liste))
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}

// (73) SearchCoursesLatest — l'onglet « Nouveautes ».
func smm2SearchCoursesLatest(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	return repondreNiveaux(conn, req, "search_courses_latest", false)
}

// (83) SearchCoursesTermsRanking et (58) — les onglets classes.
func smm2SearchCoursesRanking(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	return repondreNiveaux(conn, req, "search_courses_ranking", true)
}

// (84) SearchCoursesPickUp — « A la une ». Liste seule, sans rangs ni booleen final.
func smm2SearchCoursesPickUp(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	options, depart, nombre := parametreRecherche(s, req.Body)
	liste := trancher(niveauxPublics(), depart, nombre)
	if os.Getenv("SMM2_COURSEINFO") != "1" {
		liste = nil
	}
	out := nex.NewStreamOut(s)
	out.U32(uint32(len(liste)))
	for _, m := range liste {
		ecrireCourseInfo(out, m, options)
	}
	fmt.Printf("[SMM2 Courses] search_courses_pickup(84) pid=%d options=0x%x -> %d niveau(x)\n",
		conn.PID, options, len(liste))
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}

// smm2SearchCoursesEndlessMode (79) : la reserve de niveaux du mode Mario sans fin.
//
// C'est elle qui bloquait « Demarrer ». L'ecran s'ouvrait — la 115 repondait — mais le
// jeu n'envoyait jamais InitEndlessMode(109) : sans un seul niveau a proposer, il
// abandonne avant de commencer et affiche « service non disponible ».
//
// Parametre documente : resultOption (Uint32), count (Uint32), difficulty (Uint8).
// Reponse : List<CourseInfo>, sans booleen final — contrairement aux autres recherches.
//
// LA DIFFICULTE EST IGNOREE, ET C'EST UN CHOIX. Elle vit dans l'en-tete binaire du
// niveau, qu'on stocke sans l'interpreter. Filtrer dessus exigerait de deviner un champ
// qu'on ne sait pas lire ; on rend donc tous les niveaux quelle que soit la difficulte
// demandee. Consequence assumee : les quatre difficultes proposent le meme choix. Mieux
// vaut un choix honnete et complet qu'un tri au hasard qui vide trois menus sur quatre.
func smm2SearchCoursesEndlessMode(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings

	in := nex.NewStreamIn(req.Body, s)
	p := in
	if s.StructHeader {
		_ = in.U8()
		p = in.Substream()
	}
	options := p.U32()
	nombre := p.U32()
	difficulte := p.U8()

	// Si on arrive ici, c'est que la 109 a ete acceptee : le jeu ne demande la reserve
	// de niveaux qu'apres avoir initialise la partie.
	SondeAcceptee(79, 109)

	liste := niveauxPublics()
	if nombre > 0 && nombre < uint32(len(liste)) {
		liste = liste[:nombre]
	}
	if os.Getenv("SMM2_COURSEINFO") != "1" {
		liste = nil
	}

	out := nex.NewStreamOut(s)
	out.U32(uint32(len(liste)))
	for _, m := range liste {
		ecrireCourseInfo(out, m, options)
	}

	fmt.Printf("[SMM2 Courses] search_courses_endless(79) pid=%d options=0x%x demande=%d difficulte=%d -> %d niveau(x)\n",
		conn.PID, options, nombre, difficulte, len(liste))
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}

// smm2SearchCoursesMultijoueur : la methode 78, le niveau d'une partie cooperative.
//
// C'EST ELLE QUI FAISAIT DIRE « donnees du niveau corrompues ». Rien n'etait corrompu :
// elle tombait sur le repli generique, la console repartait sans aucun niveau, demandait
// le data_id ZERO — « prepare_get(25) data_id=0 INTROUVABLE » dans le journal — et le jeu
// annoncait la seule chose qu'il pouvait annoncer. Les 188 fichiers de niveaux ont ete
// verifies un a un contre le catalogue au meme moment : pas un octet de travers.
//
// AUCUNE SOURCE PUBLIQUE NE LA NOMME. ocw-server saute de la 76 a la 79. Ce qui l'a
// identifiee, c'est la FORME DE SON PARAMETRE, mesuree le 2026-08-29 :
//
//	00 09000000 3f000000 01000000 00
//	            ^options  ^combien  ^difficulte
//
// Trois champs, dans le meme ordre et avec les memes largeurs que la 79 du mode sans fin.
// Une methode qui demande la meme chose rend tres probablement la meme chose : une liste
// de fiches. C'est une deduction, pas une mesure, et elle est ecrite comme telle.
func smm2SearchCoursesMultijoueur(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	in := nex.NewStreamIn(req.Body, s)
	p := in
	if s.StructHeader {
		_ = in.U8()
		p = in.Substream()
	}
	options := p.U32()
	nombre := p.U32()
	difficulte := p.U8()
	if err := p.Err(); err != nil {
		fmt.Printf("[SMM2 Courses] search_courses_multi(78) : parametre illisible (%v) brut=%x\n", err, req.Body)
		return nex.NewRMCError(s, 0x73, req.CallID, 0x00690002)
	}

	// L'ordre vient de niveauxPublics, desormais departage par data_id : les DEUX joueurs
	// d'une meme partie interrogent le serveur chacun de leur cote et doivent recevoir le
	// meme niveau. Un ordre instable les enverrait dans deux niveaux differents.
	liste := niveauxPublics()
	if nombre > 0 && nombre < uint32(len(liste)) {
		liste = liste[:nombre]
	}

	out := nex.NewStreamOut(s)
	out.U32(uint32(len(liste)))
	for _, m := range liste {
		ecrireCourseInfo(out, m, options)
	}

	fmt.Printf("[SMM2 Courses] search_courses_multi(78) pid=%d options=0x%x demande=%d difficulte=%d -> %d niveau(x)\n",
		conn.PID, options, nombre, difficulte, len(liste))
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}

// smm2CoursesVersus : la methode 117, la porte du mode VERSUS.
//
// CE QU'ON SAIT, mesure le 2026-08-29 : elle est appelee sans aucun parametre — le corps
// est vide — deux fois a cinq secondes d'intervalle, et si la reponse ne lui convient pas
// la console abandonne le versus. Aucune source publique ne la nomme : ocw-server saute de
// la 116 a la 123 et son auteur ne l'a jamais implementee.
//
// CE QU'ON SUPPOSE, et c'est ecrit comme une supposition : qu'elle rende une liste de
// fiches, comme la 78 pour le cooperatif et la 79 pour le mode sans fin. La sonde a deja
// etabli qu'une enveloppe « struct{ liste } » ne fait pas abandonner la console — mais une
// liste VIDE ne lui donne aucun niveau, exactement l'impasse ou etait le cooperatif avant
// que la 78 existe.
//
// SANS PARAMETRE, il n'y a pas de masque d'options : on prend 0x3f, celui que la console
// envoie elle-meme a la 78 pour le cooperatif. C'est le choix le moins invente disponible.
//
// Reglable sans redeployer, parce que ce n'est qu'une hypothese :
//
//	echo 1 > /opt/smm2/smm2_117.forme   -> revenir a la liste VIDE
//	echo 0 > ...                        -> corps vide
func smm2CoursesVersus(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings

	// UNE FORME NUMEROTEE dans le fichier reprend la main : /opt/smm2/smm2_117.forme.
	if f := formeEssai(117, -1); f >= 0 {
		return repondreSonde(conn, req, "versus", f)
	}

	liste := niveauxPublics()
	if len(liste) > 1 {
		liste = liste[:1] // le cooperatif en demande UN ; on ne fait pas plus large a l'aveugle
	}

	corps := nex.NewStreamOut(s)
	corps.U32(uint32(len(liste)))
	for _, m := range liste {
		ecrireCourseInfo(corps, m, 0x3f)
	}

	// LISTE NUE PAR DEFAUT, comme la 78 et la 79 — les deux methodes de ce protocole qui
	// rendent des fiches et qui FONCTIONNENT. Le premier essai l'avait encapsulee dans une
	// structure et la console s'est arretee net : elle recevait la reponse et n'emettait
	// plus rien. C'etait une divergence gratuite d'avec les deux seuls exemples verifies.
	//
	//	echo enc > /opt/smm2/smm2_117.forme   -> reessayer encapsulee
	//	echo 1   > ...                         -> liste VIDE encapsulee (l'ancien defaut)
	out := corps.Bytes()
	forme := "liste nue"
	if b, err := os.ReadFile("/data/smm2_117.forme"); err == nil && strings.TrimSpace(string(b)) == "enc" {
		out = frameStruct(s, 0, corps.Bytes())
		forme = "liste encapsulee"
	}

	fmt.Printf("[SMM2 Courses] versus(117) pid=%d -> %d niveau(x), %s, options=0x3f (HYPOTHESE, non documente)\n",
		conn.PID, len(liste), forme)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out)
}
