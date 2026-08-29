package main

// CourseInfo — la fiche d'un niveau, telle que le jeu l'attend.
//
// POURQUOI ELLE COMPTE PLUS QUE PREVU. On croyait n'en avoir besoin que pour peupler
// Course World. En realite le jeu la reclame DES LA PUBLICATION : juste apres avoir
// confirme l'envoi, il appelle GetCourses(70) pour afficher le code du niveau. Sans
// reponse, l'ecran affiche « Niveau publie (ID : ) » avec un identifiant vide — le
// niveau est bien sur le disque, mais le joueur repart sans son code.
//
// LES CHAMPS DITS OPTIONNELS SONT TOUJOURS ECRITS. La note precedente affirmait le
// contraire — qu'un bit du masque « Result options » commandait chaque champ, et
// qu'ecrire un champ non demande decalait toute la suite. C'etait une deduction tiree de
// la colonne « Option » de la documentation, jamais une mesure.
//
// Elle ne s'est jamais trahie parce que TOUS les appels qui marchaient envoyaient le
// masque 0x1ff, ou les neuf bits sont poses : « ecrire ce qui est demande » et « tout
// ecrire » y donnent exactement les memes octets. Le mode sans fin est le premier a
// demander autre chose — 0x3f — et nous taisions alors trois champs que le client
// attendait quand meme : la table Unk4 et les deux vignettes. Il rejetait la reponse
// entiere, ce qui s'affiche « erreur de connexion ».
//
// L'implementation de reference declare ces champs sans condition et son encodeur les
// ecrit toujours. Ce changement ne peut donc pas abimer ce qui fonctionne : a 0x1ff la
// sortie est identique au byte pres.
//
// Structures relevees dans la documentation PretendoNetwork.

import (
	"fmt"
	"os"
	"sort"
	"time"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

// Bits du masque « Result options ». Les valeurs viennent de la colonne Option de la
// documentation, pas d'une deduction.
const (
	optPlayStats    uint32 = 0x1
	optRatings      uint32 = 0x2
	optTimeStats    uint32 = 0x4
	optCommentStats uint32 = 0x8
	optUnk10        uint32 = 0x10
	optUnk20        uint32 = 0x20
	optUnk40        uint32 = 0x40
	optThumbOneScr  uint32 = 0x80
	optThumbEntire  uint32 = 0x100
)

// codeNiveau fabrique le code a neuf caracteres affiche au joueur.
//
// Il doit etre STABLE : c'est ce que les gens s'echangent pour trouver un niveau. On le
// derive du data_id, donc il ne bouge jamais pour un niveau donne. L'alphabet exclut
// les caracteres qu'on confond a l'oral et a l'ecrit (I/1, O/0, U/V), comme le fait
// Nintendo — un code qu'on ne peut pas dicter au telephone ne sert a rien.
func codeNiveau(dataID uint64) string {
	const alphabet = "0123456789BCDFGHJKLMNPQRSTVWXY"
	code := make([]byte, 9)
	v := dataID
	for i := range code {
		code[i] = alphabet[v%uint64(len(alphabet))]
		v /= uint64(len(alphabet))
	}
	return string(code)
}

// ecrireCourseInfo serialise un niveau. `options` est le masque envoye par le client.
func ecrireCourseInfo(out *nex.StreamOut, c *courseMeta, options uint32) {
	s := out.Settings
	f := nex.NewStreamOut(s)

	// --- les seize champs toujours presents ---
	f.U64(c.DataID)
	// Code MELANGE : voir smm2_code.go. L'ancienne forme laissait six zeros a la fin et
	// permettait d'enumerer le catalogue en essayant les codes a la suite.
	f.String(codeMelange(c.DataID))
	f.PID(c.OwnerPID)
	f.String(c.Name)
	f.String(c.Description)
	// Style et theme sont maintenant LUS dans le niveau (smm2_bcd.go). Ils valaient zero
	// auparavant, ce qui annoncait tous les niveaux en SMB1 : un joueur a vu un niveau
	// SM3DW presente avec le Mario d'origine. Zero n'etait pas un defaut inoffensif,
	// c'etait une affirmation fausse.
	//
	// La difficulte, elle, reste a zero : elle ne vient pas du fichier du niveau mais des
	// statistiques de reussite calculees par le serveur, qu'on n'agrege pas encore.
	f.U8(c.Style)
	f.U8(c.Theme)
	// CreatedAt est un instant Unix ; un DateTime NEX est un CHAMP DE BITS
	// (seconde | minute<<6 | heure<<12 | jour<<17 | mois<<22 | annee<<26). Ecrire les
	// secondes Unix telles quelles ne casse rien — les deux sont des Uint64 — mais la
	// console desempaquette les bits : les niveaux s'affichaient « 06/10/0026 », et le
	// calcul le confirme (1787788800>>26 = 26 pour l'annee, >>22&0xF = 10 pour le mois).
	//
	// La bibliotheque avait DEJA MakeDateTime, et tous les autres appels du projet
	// l'utilisent correctement. Ce site etait le seul faux : ma faute, pas une lacune.
	t := time.Unix(c.CreatedAt, 0).UTC()
	f.DateTime(uint64(nex.MakeDateTime(t.Year(), int(t.Month()), t.Day(), t.Hour(), t.Minute(), t.Second())))
	f.U8(difficulteNiveau(c)) // difficulte : calculee, voir smm2_difficulte.go
	f.U8(uint8(tagOuZero(c.Tags, 0)))
	f.U8(uint8(tagOuZero(c.Tags, 1)))
	f.U8(0)
	f.U32(c.Condition)   // clear condition
	f.U16(c.CondAmpleur) // clear condition magnitude
	f.U16(0)
	f.QBuffer(nil)

	// --- champs conditionnels, dans l'ordre EXACT du tableau documente ---
	vide := func() {
		nex.WriteMap(f, map[uint8]uint32{}, func(o *nex.StreamOut, k uint8) { o.U8(k) }, func(o *nex.StreamOut, v uint32) { o.U32(v) })
	}
	r := resultats.lire(c.DataID)
	{
		// Cles documentees : 0 parties, 1 tentatives, 3 reussites. On n'ecrit que
		// celles dont on connait le sens ; les cles 2 et 4 restent absentes plutot que
		// remplies de zeros qui affirmeraient « zero partie en versus » alors qu'on ne
		// compte simplement pas ce mode.
		nex.WriteMap(f, map[uint8]uint32{0: r.Parties, 1: r.Tentatives, 3: r.Reussites},
			func(o *nex.StreamOut, k uint8) { o.U8(k) },
			func(o *nex.StreamOut, v uint32) { o.U32(v) })
	}
	vide() // Ratings
	vide() // Unk4
	{
		// CourseTimeStats : PID, PID, Uint32, Uint32 — pas trois Uint32 comme je l'avais
		// ecrit de tete. Sur Switch un PID fait huit octets, donc mon erreur decalait la
		// fiche de douze octets et le client rejetait TOUTE la reponse. Les zeros sont
		// exacts : personne n'a encore fini ce niveau, il n'y a ni premier finisseur ni
		// record du monde.
		ts := nex.NewStreamOut(s)
		ts.PID(r.PremierPID)  // premier a terminer
		ts.PID(r.RecordPID)   // detenteur du record
		ts.U32(r.RecordMs)    // record du monde (ms)
		ts.U32(c.TempsAuteur) // temps de l'auteur (ms), lu dans le fichier du niveau
		if s.StructHeader {
			f.U8(0)
			f.Buffer(ts.Bytes())
		} else {
			f.Write(ts.Bytes())
		}
	}
	{
		// Cle 0 : le nombre de commentaires. Une table VIDE fait afficher « 9999 » au
		// jeu — sa facon de dire qu'il n'a pas la donnee. Un vrai zero vaut mieux qu'un
		// nombre inquietant qui n'est meme pas un nombre.
		nex.WriteMap(f, map[uint8]uint32{0: commentaires.nombreDe(c.DataID)},
			func(o *nex.StreamOut, k uint8) { o.U8(k) },
			func(o *nex.StreamOut, v uint32) { o.U32(v) })
	}
	// LES QUATRE Uint8 INCONNUS — et l'un d'eux restreint les commentaires.
	//
	// Le jeu affiche « tu ne peux pas commenter, le createur l'a restreint ». Ce n'est
	// donc pas un refus general : le client lit un DRAPEAU PAR NIVEAU et le trouve mis.
	// Il ne vient pas du fichier du niveau (verifie : rien de tel dans level.ksy), et la
	// seule chose que nous envoyions a cet endroit sont ces quatre octets, tous a zero.
	//
	// La documentation les donne « Unknown », kinnay les nomme unk9 a unk12 sans plus.
	// On ne peut donc pas savoir LEQUEL sans essayer — d'ou un masque de bits, un par
	// champ, reglable sans redeploiement :
	//
	//   echo 15 > /opt/smm2/smm2_unk.masque   -> les quatre a 1  (defaut)
	//   echo 1  > ...                         -> seul le premier
	//   echo 2  > ...                         -> seul le deuxieme
	//   echo 0  > ...                         -> tous a zero (comportement precedent)
	//
	// Une dichotomie a quatre valeurs suffit a isoler le bon, et chaque essai coute un
	// `echo` au lieu d'une compilation.
	// ZERO PAR DEFAUT. On les avait mis a 1 en cherchant ce qui restreignait les
	// commentaires ; ce n'etait pas eux — le coupable etait ailleurs (voir les booleens
	// d'UserInfo et la methode 61). Rien n'a jamais montre qu'une autre valeur serve, et
	// inventer un drapeau dont on ignore le sens finit toujours par se payer.
	masque := uint32(formeEssai(9999, 0)) // 9999 : ce n'est pas une methode, juste un nom de fichier
	octet := func(bit uint32) uint8 {
		if masque&bit != 0 {
			return 1
		}
		return 0
	}
	f.U8(octet(1))
	f.U8(octet(2))
	f.U8(octet(4))
	f.U8(octet(8))
	// Types releves dans la documentation, pas devines : 2 = vignette d'un ecran,
	// 3 = vignette du niveau entier. J'avais mis 5 et 1, donc on renvoyait l'adresse
	// des mauvais fichiers.
	ecrireMiniature(f, c, 2)
	ecrireMiniature(f, c, 3)

	if s.StructHeader {
		out.U8(0)
		out.Buffer(f.Bytes())
		return
	}
	out.Write(f.Bytes())
}

// ecrireMiniature ecrit un RelationObjectReqGetInfo : l'adresse ou la console ira
// chercher l'image, pas l'image elle-meme. Les fichiers sont deja sur notre magasin,
// deposes au moment de la publication.
func ecrireMiniature(out *nex.StreamOut, c *courseMeta, relType uint8) {
	s := out.Settings
	id, taille := miniatureDe(c.DataID, relType)

	r := nex.NewStreamOut(s)
	r.String(fmt.Sprintf("%s/object/%d", storageURL, id))
	r.U8(relType)
	r.U32(taille)
	r.Buffer(courses.rootCA)
	r.String(fmt.Sprintf("%d.bin", id))

	if s.StructHeader {
		out.U8(0)
		out.Buffer(r.Bytes())
		return
	}
	out.Write(r.Bytes())
}

// tagOuZero rend l'etiquette n si elle existe. Les etiquettes arrivent en texte a la
// publication mais voyagent en code numerique dans CourseInfo ; la table de
// correspondance n'est pas encore etablie, donc on rend zero (« aucune ») plutot
// qu'un code invente qui afficherait une etiquette fausse.
func tagOuZero(tags []string, n int) uint32 {
	return 0
}

// miniatureDe retrouve le fichier rattache d'un type donne pour ce niveau. Ils sont
// enregistres sous le nom « rel-<parent>-<type> » au moment de la publication.
func miniatureDe(parent uint64, relType uint8) (uint64, uint32) {
	cible := fmt.Sprintf("rel-%d-%d", parent, relType)
	courses.mu.Lock()
	defer courses.mu.Unlock()
	for id, m := range courses.byID {
		if m.Name == cible {
			return id, m.Size
		}
	}
	return 0, 0
}

// smm2GetCourses (70) : « donne-moi la fiche de ces niveaux ».
//
// C'est l'appel que le jeu fait JUSTE APRES avoir publie, pour afficher le code du
// niveau. Y repondre une liste vide donnait « Niveau publie (ID : ) ».
//
// Requete : List<Uint64> data_ids, puis Uint32 « result options » — le masque qui dit
// quels champs facultatifs de CourseInfo le client veut recevoir.
// Reponse : List<CourseInfo>, puis List<Result> — un code par identifiant demande.
func smm2GetCourses(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	in := nex.NewStreamIn(req.Body, s)
	_ = in.U8()
	p := in.Substream()

	ids := nex.ReadList(p, func(i *nex.StreamIn) uint64 { return i.U64() })
	options := p.U32()

	// Trace brute : la fiche CourseInfo est longue et un seul champ de travers la rend
	// illisible. Mieux vaut regarder les octets que deviner lequel.
	fmt.Printf("[SMM2 Courses] get_courses(70) brut len=%d: %x\n", len(req.Body), req.Body)

	if err := p.Err(); err != nil {
		fmt.Printf("[SMM2 Courses] get_courses(70) : parametre illisible (%v)\n", err)
		return nex.NewRMCError(s, 0x73, req.CallID, 0x00690002)
	}

	// On separe avant d'ecrire : le client attend les fiches trouvees d'abord, puis un
	// resultat PAR IDENTIFIANT DEMANDE, dans l'ordre. Melanger desynchronise sa lecture.
	type trouvaille struct {
		m  *courseMeta
		ok bool
	}
	res := make([]trouvaille, 0, len(ids))
	n := 0
	courses.mu.Lock()
	for _, id := range ids {
		m := courses.byID[id]
		// Un niveau pas encore confirme n'existe pas pour le reste du monde.
		if m != nil && m.Ready {
			res = append(res, trouvaille{m, true})
			n++
		} else {
			res = append(res, trouvaille{})
		}
	}
	courses.mu.Unlock()

	// FILET DE SECURITE. Repondre une liste vide laissait publier sans afficher le code —
	// genant, mais fonctionnel. Repondre une fiche MAL FORMEE fait echouer la publication
	// entiere : c'est strictement pire. Tant que la forme n'est pas sure, on ne rend une
	// fiche que si SMM2_COURSEINFO=1 est pose ; sinon on revient au comportement qui
	// marchait. On n'echange pas une fonctionnalite manquante contre une regression.
	if os.Getenv("SMM2_COURSEINFO") != "1" {
		out := nex.NewStreamOut(s)
		out.U32(0)
		out.U32(uint32(len(res)))
		for range res {
			out.Result(0x00690004)
		}
		fmt.Printf("[SMM2 Courses] get_courses(70) options=0x%x -> liste vide (CourseInfo desactive)\n", options)
		return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
	}

	out := nex.NewStreamOut(s)
	out.U32(uint32(n))
	for _, r := range res {
		if r.ok {
			ecrireCourseInfo(out, r.m, options)
		}
	}
	out.U32(uint32(len(res)))
	for _, r := range res {
		if r.ok {
			out.Result(0)
		} else {
			out.Result(0x00690004) // DataStore::NotFound
		}
	}

	fmt.Printf("[SMM2 Courses] get_courses(70) options=0x%x demande %d -> %d fiche(s)\n",
		options, len(ids), n)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}

// smm2SearchCoursesPostedBy (74) : les niveaux publies par un createur.
//
// Reponse : List<CourseInfo> puis un booleen. Le masque d'options est le dernier Uint32
// du parametre ; on le lit sans chercher a interpreter le reste, qui porte des criteres
// de tri et de pagination qu'on n'applique pas encore.
func smm2SearchCoursesPostedBy(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	in := nex.NewStreamIn(req.Body, s)
	_ = in.U8()
	p := in.Substream()

	// resultOption D'ABORD : c'est le masque des champs facultatifs. Je l'avais ignore
	// et j'ecrivais les fiches avec un masque de zero — le client en reclamait neuf
	// champs et n'en recevait aucun, donc il rejetait la reponse. La fiche etait
	// pourtant trouvee : « 1 niveau » dans le journal, et une erreur a l'ecran.
	options := p.U32()

	// ResultRange (offset + nombre) : desormais APPLIQUEE, et non plus seulement lue.
	//
	// Elle etait consommee pour ne pas se decaler, avec la note « la pagination n'est pas
	// encore appliquee ». Tant que personne n'avait plus d'une page de niveaux, cela ne se
	// voyait pas. Des qu'un createur en a assez pour deux pages, le jeu demande la seconde
	// et recoit la premiere : les memes niveaux en double, et ceux qu'il attendait absents.
	var depart, combien uint32
	if s.StructHeader {
		_ = p.U8()
		rr := p.Substream()
		depart = rr.U32()
		combien = rr.U32()
	} else {
		depart = p.U32()
		combien = p.U32()
	}
	demandes := nex.ReadList(p, func(i *nex.StreamIn) uint64 { return i.PID() })
	// La console designe le createur par son identifiant NSA, pas par son PID NEX. Voir
	// smm2_identifiants.go : c'est ce qui empechait de publier un super monde.
	proprios := pidsJoueurs(demandes)

	if err := p.Err(); err != nil {
		fmt.Printf("[SMM2 Courses] search_posted_by(74) : parametre illisible (%v)\n", err)
		return nex.NewRMCError(s, 0x73, req.CallID, 0x00690002)
	}
	// Liste vide = « mes niveaux ». Meme convention que GetUsers.
	if len(proprios) == 0 {
		proprios = []uint64{conn.PID}
	}
	vise := map[uint64]bool{}
	for _, pid := range proprios {
		vise[pid] = true
	}

	var liste []*courseMeta
	courses.mu.Lock()
	for _, m := range courses.byID {
		if m.Ready && vise[m.OwnerPID] && !estFichierRattache(m.Name) {
			liste = append(liste, m)
		}
	}
	courses.mu.Unlock()

	// TRI STABLE, du plus recent au plus ancien. On parcourait `courses.byID`, une carte
	// Go, dont l'ordre de parcours est VOLONTAIREMENT aleatoire : « mes niveaux » sortaient
	// dans un ordre different a chaque appel. Combine avec la pagination ci-dessous, le jeu
	// ne pouvait pas retrouver deux fois le meme niveau a la meme place.
	//
	// C'est la troisieme fois que ce depot se fait mordre par l'ordre d'une carte Go, apres
	// le departage du Ranking2 et le choix du rassemblement dans MessageDelivery. Le
	// data_id departage les ex aequo : il est unique et ne bouge jamais.
	sort.Slice(liste, func(a, b int) bool {
		if liste[a].CreatedAt != liste[b].CreatedAt {
			return liste[a].CreatedAt > liste[b].CreatedAt
		}
		return liste[a].DataID > liste[b].DataID
	})

	total := len(liste)
	// Une etendue hors des bornes rend une page VIDE, pas la liste entiere : c'est la
	// reponse juste a « donne-moi ce qui vient apres la fin ».
	if depart >= uint32(total) {
		liste = nil
	} else {
		liste = liste[depart:]
		if combien > 0 && combien < uint32(len(liste)) {
			liste = liste[:combien]
		}
	}

	if os.Getenv("SMM2_COURSEINFO") != "1" {
		liste = nil // meme raison que pour la 70
	}
	out := nex.NewStreamOut(s)
	out.U32(uint32(len(liste)))
	for _, m := range liste {
		ecrireCourseInfo(out, m, options)
	}
	out.Bool(true)

	// On journalise le createur INTERROGE, pas seulement celui qui demande. La ligne
	// precedente n'imprimait que conn.PID : un « 0 niveau » ne disait donc pas si le joueur
	// n'avait rien publie ou s'il consultait quelqu'un d'autre, et j'ai perdu une mesure
	// entiere a confondre les deux.
	fmt.Printf("[SMM2 Courses] search_posted_by(74) demandeur=%d demandes=%v -> createurs=%v etendue=%d+%d options=0x%x -> %d/%d niveau(x)\n",
		conn.PID, demandes, proprios, depart, combien, options, len(liste), total)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}

// estFichierRattache distingue un vrai niveau de ses miniatures. Elles vivent dans le
// meme catalogue, sous un nom « rel-<parent>-<type> », et ne doivent JAMAIS apparaitre
// dans une liste de niveaux.
func estFichierRattache(nom string) bool {
	return len(nom) > 4 && nom[:4] == "rel-"
}

// smm2CanPostRatingAndComment (61) : le jeu demande s'il a le droit de noter et de
// commenter CE niveau. Il la pose juste apres avoir telecharge le cours, avant de
// lancer la partie — c'est-a-dire qu'un echec ici se voit a l'ecran comme une
// impossibilite de jouer, alors que la question porte sur les notes et commentaires.
//
// Requete  : Uint64 (data_id) + Uint32 (drapeaux).
// Reponse  : Uint64, Bool, Uint32, Map<Uint8,Uint32>, Bool, Uint32, Map<Uint8,Uint32>.
//
//	Les deux triplets identiques sont, selon toute apparence, l'un pour la
//	note et l'autre pour le commentaire. Structure prise de la documentation
//	PretendoNetwork, pas devinee.
//
// On repond oui aux deux. Nextendo n'a ni moderation de commentaires ni quota de
// notes ; pretendre le contraire inventerait une restriction qui n'existe pas.
func smm2CanPostRatingAndComment(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings

	// Le parametre de CETTE methode est une structure encadree, mesuree le 2026-08-29 :
	//
	//	000c000000 6f0a000000000000 03000000
	//	 ^version   ^data_id 2671    ^un u32 de sens inconnu
	//
	// Ces octets ont d'abord ete attribues par erreur a GetDeathPositions (103) : le
	// volcado avait ete insere dans CETTE fonction-ci en croyant modifier l'autre. Le
	// « data_id=0 » de la 103 reste donc entier et non mesure.
	in := nex.NewStreamIn(req.Body, s)
	var dataID uint64
	if s.StructHeader {
		_ = in.U8()
		sous := in.Substream()
		dataID = sous.U64()
	} else {
		dataID = in.U64()
	}

	// LES DEUX Uint32 SONT UNE INCONNUE, ET LEUR VALEUR COMPTE.
	//
	// La structure documentee est Bool, Uint32, Map — deux fois, l'une pour la note et
	// l'autre pour le commentaire. Le wiki dit « Unknown » pour les trois, et la
	// bibliotheque de kinnay ne l'implemente pas (MariOver ne lit que des donnees, il ne
	// commente rien). J'avais mis zero en lisant ce Uint32 comme « aucune raison de
	// refus ». Mais s'il s'agit d'un QUOTA RESTANT, zero veut dire l'inverse : « il ne
	// t'en reste aucun » — et les commentaires apparaissent desactives.
	//
	// On ne peut pas trancher par la documentation, alors on le rend commutable :
	//   echo 0 > /opt/smm2/smm2_61.forme   -> zero (comportement precedent)
	//   echo 1 > ...                       -> quota de 1
	//   echo 2 > ...                       -> quota de 100   (defaut)
	//   echo 3 > ...                       -> booleens inverses
	//
	// La FORME ne change dans aucun cas, seules les valeurs : une forme fausse casse la
	// lecture du niveau entier (on l'a vu), une valeur fausse ne fait qu'afficher autre
	// chose. C'est ce qui rend l'essai sans danger pour ce qui marche deja.
	// LE BOOLEEN VEUT DIRE « RESTREINT », PAS « AUTORISE ». Mesure : a true, le jeu
	// refusait de commenter ; a false, il accepte. Le wiki les donne « Unknown » et
	// personne ne les a jamais nommes publiquement.
	// LE TROU ENTRE LES VARIANTES. Aucune ne combinait « non restreint » et un quota :
	// les trois premieres restreignaient, la quatrieme laissait passer mais annoncait un
	// quota de ZERO. Les commentaires marchaient donc — c'est le booleen qui les commande —
	// et les boutons « J'aime » et « Bouh » restaient morts, affiches et insensibles, parce
	// qu'il ne restait aucune note a donner.
	//
	// La variante 4 fait les deux, et devient le defaut. Les anciennes restent pour pouvoir
	// revenir en arriere d'un `echo` :
	//   echo 3 > /opt/smm2/smm2_61.forme   -> l'ancien defaut (sans quota)
	//   echo 4 > ...                       -> non restreint, quota 100  (defaut)
	//
	// `restreint` porte enfin le nom de ce qu'il veut dire. Il s'appelait `autorise` et
	// valait l'inverse de sa lecture : le journal affichait « autorise=false » sur le seul
	// reglage qui autorisait quelque chose.
	variante := formeEssai(61, 4)
	restreint, quota := true, uint32(0)
	switch variante {
	case 1:
		quota = 1
	case 2:
		quota = 100
	case 3:
		restreint = false
	case 4:
		restreint, quota = false, 100
	}

	corps := nex.NewStreamOut(s)
	corps.U64(dataID)
	corps.Bool(restreint) // notation restreinte
	corps.U32(quota)
	corps.U32(0)          // Map<Uint8,Uint32> vide
	corps.Bool(restreint) // commentaires restreints
	corps.U32(quota)
	corps.U32(0)

	fmt.Printf("[SMM2 Courses] can_post_rating_and_comment(61) data_id=%d -> variante %d : restreint=%v quota=%d\n",
		dataID, variante, restreint, quota)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, frameStruct(s, 0, corps.Bytes()))
}

// smm2GetDeathPositions (103) : ou les joueurs sont morts dans un niveau.
//
// Requete : Uint64 (data_id).  Reponse : List<DeathPositionInfo>.
func smm2GetDeathPositions(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings

	in := nex.NewStreamIn(req.Body, s)
	var dataID uint64
	if s.StructHeader {
		_ = in.U8()
		p := in.Substream()
		dataID = p.U64()
	} else {
		dataID = in.U64()
	}

	out := nex.NewStreamOut(s)
	out.U32(0) // aucune mort enregistree

	fmt.Printf("[SMM2 Courses] get_death_positions(103) data_id=%d -> 0\n", dataID)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}
