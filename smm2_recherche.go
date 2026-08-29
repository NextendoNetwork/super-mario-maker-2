package main

import (
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

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

	// LE NIVEAU EST TIRE AU SORT, PAS PRIS EN TETE DE LISTE.
	//
	// La premiere version rendait simplement les premiers de niveauxPublics, trie du plus
	// recent au plus ancien : le cooperatif servait donc TOUJOURS le dernier niveau publie.
	// Trois parties de suite dans « desierto pinchudo ». Ce n'etait pas faux au sens du
	// protocole, mais c'etait faux au sens du jeu.
	//
	// Le tirage est MEMORISE PAR PARTIE. Les joueurs d'une meme partie interrogent le
	// serveur chacun de leur cote, a une ou deux secondes d'intervalle ; tirer au hasard a
	// chaque appel les enverrait dans des niveaux differents. Le premier arrive choisit
	// pour tout le monde, et le choix vaut le temps d'une partie.
	liste := niveauxChoisisPourPartie(conn.PID, nombre)

	out := nex.NewStreamOut(s)
	out.U32(uint32(len(liste)))
	for _, m := range liste {
		ecrireCourseInfo(out, m, options)
	}

	fmt.Printf("[SMM2 Courses] search_courses_multi(78) pid=%d options=0x%x demande=%d difficulte=%d -> %d niveau(x)\n",
		conn.PID, options, nombre, difficulte, len(liste))
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}

// smm2GetBattleModeRating : la methode 117, la porte du mode VERSUS.
//
// CE QU'ELLE EST, trouve dans le binaire du jeu le 2026-08-29 apres quatre hypotheses
// ratees. Le jeu porte un etat nomme « State::cGetBattleModeRating » : c'est la requete
// qu'il lance avant d'appairer, et elle n'a besoin d'aucun parametre puisque l'identite
// voyage deja dans la connexion.
//
// CE QU'ELLE DOIT RENDRE. Le versus de SMM2 classe les joueurs en GLICKO-2, un systeme a
// trois composantes. On le sait parce que le client les RENVOIE au serveur en fin de
// combat, dans une structure que le binaire documente :
//
//	EndBattleModeParam: (battleResults=(...), killCount=, killedCount=,
//	                     glicko2Rate=, glicko2Deviation=, glicko2Volatility=, gid=)
//
// et parce que la liste des champs de telemetrie du jeu contient « rating, glicko2_rate,
// glicko2_rd, glicko2_volatility ». Un client qui rend ces valeurs les a forcement recues.
//
// LES TROIS SONT DES ENTIERS 32 BITS, pas des flottants : dans le vidage ci-dessus chaque
// champ est charge par `ldr w1, [sp, #...]` sur six mots consecutifs. La volatilite, qui
// vaut 0,06 en Glicko-2, voyage donc MISE A L'ECHELLE.
//
// CE QUI RESTE UNE DEDUCTION : le nombre exact de champs de la REPONSE et l'echelle de la
// volatilite. Le binaire ne documente que les requetes, jamais les reponses. D'ou une liste
// reglable sans redeployer — valeurs par defaut : les valeurs de depart de Glicko-2, 1500
// de note, 350 de deviation, et 0,06 mis a l'echelle par un million.
//
//	echo 1500,350,600 > /opt/smm2/smm2_117.valeurs    # autre echelle
//	echo 1500,350,60000,0 > ...                        # un champ de plus
//	echo 1 > /opt/smm2/smm2_117.forme                  # revenir aux enveloppes vides
func smm2GetBattleModeRating(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings

	// UNE FORME NUMEROTEE dans le fichier reprend la main : c'est le filet qui a protege
	// le cooperatif pendant les essais.
	if f := formeEssai(117, -1); f >= 0 {
		return repondreSonde(conn, req, "versus", f)
	}

	valeurs := []uint32{1500, 350, 60000}
	if b, err := os.ReadFile("/data/smm2_117.valeurs"); err == nil {
		var lus []uint32
		for _, part := range strings.Split(strings.TrimSpace(string(b)), ",") {
			n, err := strconv.ParseUint(strings.TrimSpace(part), 10, 32)
			if err != nil {
				lus = nil
				break
			}
			lus = append(lus, uint32(n))
		}
		if len(lus) > 0 {
			valeurs = lus
		}
	}

	corps := nex.NewStreamOut(s)
	for _, v := range valeurs {
		corps.U32(v)
	}
	out := frameStruct(s, 0, corps.Bytes())

	fmt.Printf("[SMM2 Courses] get_battle_mode_rating(117) pid=%d -> glicko2 %v (DEDUCTION : reponse non documentee)\n",
		conn.PID, valeurs)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out)
}

// --- le tirage du niveau cooperatif ------------------------------------------

// MatchmakingSMM2 : la table des parties, posee au demarrage. Sans elle, le tirage
// retombe sur un decoupage par tranche de temps, moins sur.
var MatchmakingSMM2 *nex.Matchmaking

type choixPartie struct {
	niveaux []*courseMeta
	quand   time.Time
}

var (
	choixMu      sync.Mutex
	choixParties = map[uint32]choixPartie{}
)

// dureeChoix : au-dela, la partie est consideree finie et un nouveau tirage a lieu. Assez
// long pour couvrir une partie entiere, assez court pour ne pas resservir le meme niveau
// a un groupe qui rejoue.
const dureeChoix = 10 * time.Minute

// niveauxChoisisPourPartie rend les niveaux a jouer, identiques pour tous les joueurs
// d'une meme partie.
func niveauxChoisisPourPartie(pid uint64, combien uint32) []*courseMeta {
	tous := niveauxPublics()
	if len(tous) == 0 {
		return nil
	}
	if combien == 0 || combien > uint32(len(tous)) {
		combien = 1
	}

	var gid uint32
	if MatchmakingSMM2 != nil {
		gid = MatchmakingSMM2.GidDuParticipant(pid)
	}
	// Aucune partie trouvee : on retombe sur une tranche de temps commune. Deux consoles
	// separees par la frontiere d'une tranche tireraient differemment — d'ou la preference
	// pour l'identifiant de partie quand il existe.
	if gid == 0 {
		gid = 1<<31 | uint32(time.Now().Unix()/300)
	}

	choixMu.Lock()
	defer choixMu.Unlock()
	if c, ok := choixParties[gid]; ok && time.Since(c.quand) < dureeChoix && len(c.niveaux) >= int(combien) {
		return c.niveaux[:combien]
	}

	// Melange de Fisher-Yates sur une COPIE : niveauxPublics rend des pointeurs vers le
	// catalogue, et reordonner la tranche rendue ne touche pas le catalogue lui-meme.
	melange := make([]*courseMeta, len(tous))
	copy(melange, tous)
	for i := len(melange) - 1; i > 0; i-- {
		j := rand.Intn(i + 1)
		melange[i], melange[j] = melange[j], melange[i]
	}

	// Menage : sans cela la table grandit avec chaque partie jamais rejouee.
	for g, c := range choixParties {
		if time.Since(c.quand) > dureeChoix {
			delete(choixParties, g)
		}
	}
	choixParties[gid] = choixPartie{niveaux: melange, quand: time.Now()}
	return melange[:combien]
}
