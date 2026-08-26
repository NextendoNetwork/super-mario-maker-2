package main

import (
	"fmt"
	"os"
	"sort"

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
	sort.Slice(liste, func(i, j int) bool { return liste[i].CreatedAt > liste[j].CreatedAt })
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
