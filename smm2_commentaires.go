package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

// Les commentaires laisses sur un niveau.
//
// CE QU'ON SAIT, ET C'EST PEU. PostCommentText (91) n'est documente nulle part : ni le
// wiki, ni la bibliotheque de kinnay (MariOver ne fait que lire, il ne commente pas).
// La requete ci-dessous vient d'une vraie console, mesuree le 2026-08-24 :
//
//	00 16000000              en-tete de structure
//	1704000000000000         Uint64 : data_id du niveau (0x417 = 1047)
//	…                        champs dont on ignore le sens
//	0500 "test"              String : le texte tape par le joueur
//
// CommentInfo, la fiche a rendre, a dix-sept champs et la documentation les nomme TOUS
// « Unknown ». On ne sait donc pas encore lequel porte le texte ni lequel l'auteur.
//
// CE QU'ON FAIT MALGRE TOUT. On ENREGISTRE le commentaire. Meme sans savoir le rendre,
// le garder change deux choses : le compteur affiche un nombre vrai au lieu du 9999 que
// le jeu montre quand on lui envoie une table vide, et le jour ou la forme de
// CommentInfo sera connue, les commentaires deja ecrits par les joueurs seront la.
// Les jeter en attendant serait perdre ce qu'on ne sait que stocker.

type commentaire struct {
	DataID uint64 `json:"data_id"`
	PID    uint64 `json:"pid"`
	Texte  string `json:"texte"`
	Quand  int64  `json:"quand"`
	// Image : le data_id du JPEG d'un commentaire DESSINE, zero pour un commentaire
	// texte. Un dessin televerse deux objets — les traits compresses en zlib et leur
	// rendu en JPEG — et c'est le second que le jeu affiche.
	Image     uint64 `json:"image,omitempty"`
	TailleImg uint32 `json:"taille_img,omitempty"`
	// Tampon : l'identifiant de l'image de reaction d'un commentaire TAMPON, zero pour
	// les autres. La methode 92 qui le porte tombait sur le repli generique : le jeu
	// annoncait « publie » et le tampon disparaissait.
	Tampon uint16 `json:"tampon,omitempty"`
	// X, Y : l'endroit du niveau ou le commentaire est pose. Les DEUX methodes les
	// portent, texte comme tampon, dans la meme structure de parametre — et nous les
	// jetions dans les deux cas. Un commentaire sans position n'est pas un commentaire
	// de SMM2 : le jeu les affiche a l'endroit ou ils ont ete laisses.
	X         uint16 `json:"x,omitempty"`
	Y         uint16 `json:"y,omitempty"`
	SousMonde bool   `json:"sous_monde,omitempty"`
}

// posteCommentaire : les champs fixes que portent les methodes 91 et 92, a l'identique.
//
// Vingt-deux octets, mesures sur une vraie console le 2026-08-29 : la longueur annoncee
// dans l'en-tete valait exactement 0x16, et les neuf champs ci-dessous la remplissent
// sans reste. C'est ce qui a permis de lire un tampon pose en jeu — niveau 2636, X 2513,
// image 6 — au lieu de le deviner.
type posteCommentaire struct {
	DataID        uint64
	Unk1          uint8
	X, Y          uint16
	SousMonde     bool
	Reaction      uint8
	Unk2          uint16
	ReussiteExige bool
	Unk3          uint32
}

// lirePosteCommentaire lit ces champs. Rend faux si le parametre est illisible : mieux
// vaut enregistrer un commentaire sans position que refuser de l'enregistrer.
func lirePosteCommentaire(p *nex.StreamIn) (posteCommentaire, bool) {
	var c posteCommentaire
	c.DataID = p.U64()
	c.Unk1 = p.U8()
	c.X = p.U16()
	c.Y = p.U16()
	c.SousMonde = p.Bool()
	c.Reaction = p.U8()
	c.Unk2 = p.U16()
	c.ReussiteExige = p.Bool()
	c.Unk3 = p.U32()
	return c, p.Err() == nil
}

type magasinCommentaires struct {
	mu     sync.RWMutex
	parNiv map[uint64][]commentaire
	chemin string
}

var commentaires = &magasinCommentaires{parNiv: map[uint64][]commentaire{}}

func (m *magasinCommentaires) charger(dir string) {
	m.chemin = filepath.Join(dir, "smm2_commentaires.json")
	b, err := os.ReadFile(m.chemin)
	if err != nil {
		return
	}
	var charge map[uint64][]commentaire
	if err := json.Unmarshal(b, &charge); err != nil {
		fmt.Printf("[SMM2 Commentaires] %s illisible (%v) — conserve tel quel\n", m.chemin, err)
		return
	}
	m.parNiv = charge
	var n int
	for _, l := range charge {
		n += len(l)
	}
	fmt.Printf("[SMM2 Commentaires] %d commentaire(s) sur %d niveau(x)\n", n, len(charge))
}

func (m *magasinCommentaires) ajouter(c commentaire) int {
	m.mu.Lock()
	m.parNiv[c.DataID] = append(m.parNiv[c.DataID], c)
	n := len(m.parNiv[c.DataID])
	b, err := json.Marshal(m.parNiv)
	chemin := m.chemin
	m.mu.Unlock()

	if err != nil || chemin == "" {
		return n
	}
	// Ecriture atomique, comme pour les resultats : une coupure au mauvais moment laisse
	// l'ancien fichier entier plutot qu'un JSON tronque.
	tmp := chemin + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		os.Rename(tmp, chemin)
	}
	return n
}

// attacherImage rattache un dessin au dernier commentaire de ce joueur sur ce niveau.
//
// UN DESSIN EST UN COMMENTAIRE A LUI SEUL. La premiere version cherchait un commentaire
// texte a qui rattacher l'image ; c'etait faux, et la mesure l'a montre : un commentaire
// dessine n'envoie JAMAIS PostCommentText(91). Sa sequence complete est 88 (preparer),
// deux televersements, puis 90 (confirmer) — et rien d'autre. Le rattachement collait
// donc le dessin sur un ancien commentaire texte du meme joueur.
//
// On cree donc une entree, sans texte. Et on verifie qu'on ne l'a pas deja creee : le
// jeu confirme parfois deux fois le meme envoi, ce qui afficherait le dessin en double.
func (m *magasinCommentaires) ajouterDessin(dataID, pid, image uint64, taille uint32) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Deja rattache : le jeu confirme parfois deux fois le meme televersement, et
	// creer un second commentaire pour un seul dessin le ferait apparaitre en double.
	for _, c := range m.parNiv[dataID] {
		if c.Image == image {
			return 0
		}
	}
	m.parNiv[dataID] = append(m.parNiv[dataID], commentaire{
		DataID: dataID, PID: pid, Image: image, TailleImg: taille, Quand: nowUnix(),
	})
	m.ecrireLocked()
	return len(m.parNiv[dataID])
}

// ecrireLocked persiste. A appeler verrou tenu.
func (m *magasinCommentaires) ecrireLocked() {
	if m.chemin == "" {
		return
	}
	b, err := json.Marshal(m.parNiv)
	if err != nil {
		return
	}
	tmp := m.chemin + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		os.Rename(tmp, m.chemin)
	}
}

// nombreDe rend le nombre de commentaires d'un niveau, pour comment_stats.
func (m *magasinCommentaires) nombreDe(dataID uint64) uint32 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return uint32(len(m.parNiv[dataID]))
}

// smm2PostCommentText (91) : le joueur laisse un commentaire.
//
// On lit ce qu'on sait identifier — le data_id au debut, le texte a la fin — et on
// ignore franchement le reste plutot que de compter des champs dont on ignore le sens.
// C'est la meme methode qui a marche pour PostPlayResult (96).
func smm2PostCommentText(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings

	in := nex.NewStreamIn(req.Body, s)
	p := in
	if s.StructHeader {
		_ = in.U8()
		p = in.Substream()
	}
	// Le parametre de la 91 est le MEME que celui de la 92 : on y lit donc aussi la
	// position. Elle etait jetee, alors que le jeu affiche chaque commentaire a l'endroit
	// exact ou il a ete laisse.
	//
	// Le TEXTE, lui, continue d'etre pris par derniereChaine : c'est une heuristique, mais
	// une heuristique qui marche depuis le premier jour, et on ne remplace pas une chose
	// verifiee par l'usage au motif qu'on vient de comprendre la structure d'a cote.
	champs, ok := lirePosteCommentaire(p)
	dataID := champs.DataID
	texte := derniereChaine(req.Body)

	n := commentaires.ajouter(commentaire{
		DataID: dataID, PID: conn.PID, Texte: texte, Quand: nowUnix(),
		X: champs.X, Y: champs.Y, SousMonde: champs.SousMonde,
	})

	fmt.Printf("[SMM2 Commentaires] post_comment_text(91) pid=%d data_id=%d texte=%q position=(%d,%d) lisible=%v -> %d sur ce niveau\n",
		conn.PID, dataID, texte, champs.X, champs.Y, ok, n)

	// LA REPONSE EST UN Uint32 A ZERO, ET C'EST MESURE, PAS DEDUIT.
	//
	// Avant cette implementation, la methode tombait sur le repli generique qui renvoie
	// U32(0) — et le jeu affichait « commentaire publie ». J'ai voulu rendre un succes
	// SANS corps, par analogie avec PostPlayResult (96) qui l'accepte : le jeu a
	// repondu « impossible de publier le commentaire ».
	//
	// L'analogie etait fausse, et elle etait gratuite : le comportement precedent etait
	// deja verifie par l'usage. Remplacer une chose qui marche par une supposition
	// elegante, c'est echanger une certitude contre un pari — et ici le pari a coute une
	// fonctionnalite qui marchait.
	out := nex.NewStreamOut(s)
	out.U32(0)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}

// timeUnixUTC : un instant Unix en temps UTC.
func timeUnixUTC(unix int64) time.Time { return time.Unix(unix, 0).UTC() }

// ecrireCommentInfo serialise un commentaire.
//
// LA FORME EST SURE, LE CONTENU NON. Les dix-sept champs et leurs types viennent de la
// bibliotheque de kinnay (CommentInfo.save), qui est la reference publique la plus
// complete — donc la structure sera acceptee par le client. Ce que personne ne
// documente, c'est le SENS de chaque champ : tous s'appellent « unk ». On ne sait donc
// pas lequel porte le texte du commentaire ni lequel le nom de son auteur.
//
// Cette distinction est ce qui rend l'essai sans danger. Une forme fausse fait rejeter
// TOUTE la reponse — on l'a paye trois fois cette nuit. Un contenu mal place affiche au
// pire le texte a la place du pseudo, ce qui se voit et se corrige d'un `echo` :
//
//	echo 0 > /opt/smm2/smm2_9997.forme   -> texte dans unk2, auteur dans unk15 (defaut)
//	echo 1 > ...                         -> l'inverse
//	echo 2 > ...                         -> texte dans les deux (pour voir lequel sort)
func ecrireCommentInfo(out *nex.StreamOut, c commentaire, i int) {
	s := out.Settings
	// TEXTE DANS unk15, PSEUDO DANS unk2. Determine le 2026-08-24 en mettant le pseudo
	// dans l'un et le texte dans l'autre : le jeu affichait « Juanjo » a la place du
	// commentaire. Aucune source publique ne nomme ces deux champs.
	variante := formeEssai(9997, 1)

	pseudo := nex.SMM2PseudoDe(c.PID)
	texteA, texteB := c.Texte, pseudo
	switch variante {
	case 1:
		texteA, texteB = pseudo, c.Texte
	case 2:
		texteA, texteB = c.Texte, c.Texte
	}

	f := nex.NewStreamOut(s)
	f.U64(uint64(1_000_000 + i)) // unk1 : un identifiant propre au commentaire
	f.String(texteA)             // unk2
	// unk3 ET unk4 : deux Uint8 juste apres la premiere chaine, et l'un des deux est
	// tres probablement le TYPE du commentaire — texte, tampon, ou image.
	//
	// Le jeu affichait trois cadres vides avec une frimousse triste : c'est sa facon de
	// dire « ce commentaire est une image et je ne la trouve pas ». Il ne rejetait pas
	// la reponse — la forme etait donc bonne — il lisait un type qui n'est pas « texte ».
	// Comme nous envoyions zero aux deux, zero ne veut pas dire texte.
	//
	// L'espace a fouiller est minuscule, d'ou un commutateur plutot qu'une supposition :
	//   echo 0 > /opt/smm2/smm2_9996.forme   -> (0,0) comportement precedent
	//   echo 1 > ...                         -> (1,0)      <- defaut
	//   echo 2 > ...                         -> (0,1)
	//   echo 3 > ...                         -> (1,1)
	//   echo 4 > ...                         -> (2,0)
	//   echo 5 > ...                         -> (0,2)
	//   echo 6 > ...                         -> (3,0)
	// Un commentaire DESSINE n'a pas le meme type qu'un commentaire texte : (0,1) rend
	// bien le texte, et le dessin reste invisible avec la meme valeur. Comme il n'y en a
	// qu'UN pour l'instant, on ne peut pas comparer plusieurs combinaisons d'un coup —
	// alors elle AVANCE a chaque consultation. Le joueur ouvre la liste, sort, revient,
	// et voit une combinaison differente a chaque fois ; le journal ecrit laquelle.
	var unk3, unk4 uint8
	if c.Image != 0 {
		unk3, unk4 = typeDessinSuivant()
	} else {
		unk3, unk4 = typeCommentaire(i)
	}
	f.U8(unk3)
	f.U8(unk4)
	f.U64(c.PID)  // unk5 : vraisemblablement l'auteur
	f.U16(0)      // unk6
	f.U16(0)      // unk7
	f.U8(0)       // unk8
	f.U8(0)       // unk9
	f.U16(0)      // unk10
	f.Bool(false) // unk11
	f.Bool(false) // unk12
	f.DateTime(uint64(nex.MakeDateTime(dateDe(c.Quand))))
	f.QBuffer(nex.SMM2MiiDe(c.PID)) // unk14 : vraisemblablement le Mii de l'auteur
	f.String(texteB)                // unk15

	// La structure de l'image : imbriquee, donc sa propre en-tete.
	//
	// Champs releves dans CommentPictureReqGetInfoWithoutHeaders (kinnay) : url, type de
	// donnee, taille, certificat, nom de fichier — exactement la meme forme que le
	// descripteur des vignettes de niveau, qui fonctionne deja.
	//
	// Une adresse vide quand le commentaire n'a pas de dessin n'est pas un trou : c'est
	// la verite, et le jeu ne va rien chercher.
	pic := nex.NewStreamOut(s)
	if c.Image != 0 {
		pic.String(fmt.Sprintf("%s/object/%d", storageURL, c.Image))
		pic.U8(10) // type de donnee : /ds/1/comment/ selon la documentation
		pic.U32(c.TailleImg)
		pic.Buffer(courses.rootCA)
		pic.String(fmt.Sprintf("%d.bin", c.Image))
	} else {
		// Chaines vides en longueur ZERO, comme ocw-server. Nous ecrivions longueur 1
		// et un terminateur nul : un octet de trop, DEUX fois dans cette structure. Le
		// meme ecart d'un octet decalait tout UserInfo, et c'est peut-etre ce qui
		// empechait le jeu d'aller chercher les dessins.
		pic.StringVideZero("")
		pic.U8(0)
		pic.U32(0)
		pic.Buffer(nil)
		pic.StringVideZero("")
	}
	if s.StructHeader {
		f.U8(0)
		f.Buffer(pic.Bytes())
	} else {
		f.Write(pic.Bytes())
	}

	f.U16(0) // unk16
	f.U8(0)  // unk17

	if s.StructHeader {
		out.U8(0)
		out.Buffer(f.Bytes())
		return
	}
	out.Write(f.Bytes())
}

// dateDe decompose un instant Unix pour MakeDateTime.
func dateDe(unix int64) (an, mois, jour, heure, minute, seconde int) {
	t := timeUnixUTC(unix)
	return t.Year(), int(t.Month()), t.Day(), t.Hour(), t.Minute(), t.Second()
}

// smm2SearchComments (94 et 95) : les commentaires d'un niveau.
//
// La 94 rend List<CommentInfo> + Bool, la 95 rend seulement la liste. Le data_id se lit
// dans les octets bruts plutot qu'en comptant des champs dont on ignore le sens — meme
// methode que pour la 96 et la 91, et pour la meme raison.
func smm2SearchComments(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings

	in := nex.NewStreamIn(req.Body, s)
	p := in
	if s.StructHeader {
		_ = in.U8()
		p = in.Substream()
	}
	dataID := p.U64()

	commentaires.mu.RLock()
	liste := append([]commentaire(nil), commentaires.parNiv[dataID]...)
	commentaires.mu.RUnlock()

	out := nex.NewStreamOut(s)
	out.U32(uint32(len(liste)))
	for i, c := range liste {
		ecrireCommentInfo(out, c, i)
	}
	if req.Method == 94 {
		out.Bool(false)
	}

	fmt.Printf("[SMM2 Commentaires] search_comments(%d) pid=%d data_id=%d -> %d commentaire(s)\n",
		req.Method, conn.PID, dataID, len(liste))
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}

// combinaisonsType : les couples (unk3, unk4) a essayer, dans l'ordre.
var combinaisonsType = [][2]uint8{
	{0, 0}, {1, 0}, {0, 1}, {1, 1}, {2, 0}, {0, 2}, {3, 0}, {0, 3}, {2, 1}, {1, 2},
}

// typeCommentaire rend le couple a employer pour le commentaire d'indice i.
//
// MODE EVENTAIL (defaut). Plutot que d'essayer une combinaison a la fois — un aller-
// retour avec la console pour chacune —, on en donne une DIFFERENTE a chaque
// commentaire de la liste. Le joueur en a trois : il voit trois essais d'un coup et dit
// lequel s'affiche. Le journal ecrit la correspondance, donc on n'a rien a recouper de
// tete.
//
// Une valeur fixe reste possible une fois le bon couple connu :
//
//	echo 3 > /opt/smm2/smm2_9996.forme   -> tous les commentaires en combinaison 3
//	echo -1 > ...                        -> eventail (defaut)
func typeCommentaire(i int) (uint8, uint8) {
	// (0,1) = COMMENTAIRE TEXTE. Trouve en donnant une combinaison differente a chaque
	// commentaire de la liste et en regardant lequel s'affichait. On sait aussi, par le
	// meme balayage, que (x,2) est un TAMPON et (x,0) une IMAGE.
	n := formeEssai(9996, 2)
	if n < 0 {
		n = i % len(combinaisonsType)
	}
	if n >= len(combinaisonsType) {
		n = 0
	}
	c := combinaisonsType[n]
	fmt.Printf("[SMM2 Commentaires]   commentaire %d -> combinaison %d = (%d,%d)\n", i, n, c[0], c[1])
	return c[0], c[1]
}

// smm2PreparePostObjectCommentPicture (88) : le joueur envoie un commentaire DESSINE.
//
// Requete mesuree le 2026-08-24 sur une vraie console :
//
//	00 16000000              en-tete de structure, 22 octets
//	1704000000000000         Uint64 : data_id du niveau
//	…                        les memes champs que la methode 91
//	0c000000                 Uint32 : 12
//	7a020000                 Uint32 : 634  <- la taille du dessin
//
// Elle n'est documentee nulle part, mais elle n'a rien d'inedit : c'est le meme geste
// que PrepareRelationUpload (132) pour les miniatures — « je vais televerser tant
// d'octets, donne-moi ou ». On rend donc le meme descripteur, produit par le meme code
// qui fait deja arriver les niveaux et leurs vignettes sur le disque.
func smm2PreparePostObjectCommentPicture(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings

	in := nex.NewStreamIn(req.Body, s)
	_ = in.U8()
	p := in.Substream()
	dataID := p.U64()
	_ = in.U32() // 12 : sens inconnu
	taille := in.U32()

	if err := in.Err(); err != nil || taille == 0 || taille > 8<<20 {
		// Une taille absurde signale une lecture fausse, pas un dessin geant. On refuse
		// plutot que d'allouer un objet a partir d'un nombre qu'on ne comprend pas.
		fmt.Printf("[SMM2 Commentaires] prepare_picture(88) pid=%d : parametre douteux (taille=%d, err=%v)\n",
			conn.PID, taille, err)
		return nex.NewRMCError(s, 0x73, req.CallID, 0x00690002)
	}

	id := courses.alloc(conn.PID, fmt.Sprintf("rel-%d-10", dataID), 10, nil, nil, taille)
	url := fmt.Sprintf("%s/object/%d", storageURL, id)

	body := nex.NewStreamOut(s)
	body.String(fmt.Sprintf("%d", id))
	body.String(url)
	body.U32(0) // headers
	body.U32(0) // champs de formulaire
	body.Buffer(courses.rootCA)

	fmt.Printf("[SMM2 Commentaires] prepare_picture(88) pid=%d data_id=%d taille=%d -> objet %d\n",
		conn.PID, dataID, taille, id)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, frameStruct(s, 0, body.Bytes()))
}

// smm2CompletePostObjectCommentPicture (89 et 90) : le dessin est arrive en entier.
//
// C'est ici qu'on peut enfin le rattacher a un commentaire : l'image est sur le disque
// et sa taille est connue. On cherche le JPEG parmi les objets que ce joueur vient de
// televerser — un dessin en produit DEUX, les traits compresses en zlib et leur rendu,
// et c'est le rendu que le jeu affiche.
func smm2CompletePostObjectCommentPicture(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	image, taille, niveau := dernierDessinDe(conn.PID)
	if image != 0 && niveau != 0 {
		if n := commentaires.ajouterDessin(niveau, conn.PID, image, taille); n > 0 {
			fmt.Printf("[SMM2 Commentaires] dessin %d (%d octets) de pid=%d sur le niveau %d -> %d commentaire(s)\n",
				image, taille, conn.PID, niveau, n)
		}
	}
	return smm2CompletePostObject(conn, req)
}

// dernierDessinDe cherche le JPEG le plus recent televerse par ce joueur pour un
// commentaire. On reconnait le JPEG a ses premiers octets plutot qu'a sa taille : les
// traits compresses commencent par 78da (zlib), le rendu par ffd8 (JPEG), et se fier a
// la taille marcherait jusqu'au jour ou un dessin serait plus petit que ses traits.
func dernierDessinDe(pid uint64) (uint64, uint32, uint64) {
	var meilleur, niveau uint64
	var taille uint32
	courses.mu.RLock()
	for id, m := range courses.byID {
		if m.OwnerPID != pid || typeRelation(m.Name) != 10 {
			continue
		}
		// L'objet du dessin suit immediatement celui des traits.
		for _, cand := range []uint64{id, id + 1} {
			b, err := os.ReadFile(blobPath(cand))
			if err != nil || len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
				continue
			}
			if cand >= meilleur {
				meilleur, taille = cand, uint32(len(b))
				fmt.Sscanf(m.Name, "rel-%d-10", &niveau)
			}
		}
	}
	courses.mu.RUnlock()
	return meilleur, taille, niveau
}

// combinaisonsDessin : les couples (unk3, unk4) a essayer pour un commentaire dessine.
// (0,1) est exclu : c'est celui du texte, et il laisse le dessin invisible.
var combinaisonsDessin = [][2]uint8{
	{0, 2}, {1, 1}, {0, 3}, {2, 0}, {1, 2}, {1, 0}, {2, 1}, {0, 0}, {3, 0}, {2, 2},
}

var compteurDessin int
var compteurDessinMu sync.Mutex

// typeDessinSuivant rend la combinaison suivante, et la dit dans le journal.
//
// Une valeur fixe reste possible une fois la bonne connue :
//
//	echo 3 > /opt/smm2/smm2_9995.forme   -> toujours la combinaison 3
//	echo -1 > ...                        -> balayage (defaut)
func typeDessinSuivant() (uint8, uint8) {
	// (0,0) = IMAGE. Le « cadre qui charge sans fin » etait le jeu qui essayait de la
	// telecharger : le type etait donc deja bon, et c'est l'adresse qui ne lui suffit pas.
	n := formeEssai(9995, 7)
	if n < 0 {
		compteurDessinMu.Lock()
		n = compteurDessin % len(combinaisonsDessin)
		compteurDessin++
		compteurDessinMu.Unlock()
	}
	if n >= len(combinaisonsDessin) {
		n = 0
	}
	c := combinaisonsDessin[n]
	fmt.Printf("[SMM2 Commentaires]   DESSIN -> combinaison %d = (%d,%d)\n", n, c[0], c[1])
	return c[0], c[1]
}

// smm2PostCommentStamp : la methode 92, le commentaire TAMPON.
//
// Elle tombait sur le repli generique — « UNCAPTURED 0x73.92 » dans le journal — donc le
// jeu affichait « publie » et rien n'etait garde. CLAUDE.md donnait pourtant les
// commentaires, texte ET tampons, pour fonctionnels : c'etait vrai de l'affichage, pas de
// l'enregistrement.
//
// Le parametre est celui de la 91, suivi d'une SECONDE structure de deux octets portant
// l'identifiant de l'image. On repond comme la 91 — un Uint32 a zero, valeur mesuree et
// non deduite ; l'analogie avec PostPlayResult avait deja coute une fonctionnalite.
func smm2PostCommentStamp(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	in := nex.NewStreamIn(req.Body, s)
	p := in
	if s.StructHeader {
		_ = in.U8()
		p = in.Substream()
	}
	champs, ok := lirePosteCommentaire(p)

	// La seconde structure, au niveau du flux PRINCIPAL et non du sous-flux : elle suit le
	// parametre, elle n'est pas dedans.
	var tampon uint16
	if s.StructHeader {
		_ = in.U8()
		q := in.Substream()
		tampon = q.U16()
	} else {
		tampon = in.U16()
	}

	n := commentaires.ajouter(commentaire{
		DataID: champs.DataID, PID: conn.PID, Quand: nowUnix(),
		Tampon: tampon, X: champs.X, Y: champs.Y, SousMonde: champs.SousMonde,
	})

	fmt.Printf("[SMM2 Commentaires] post_comment_stamp(92) pid=%d data_id=%d tampon=%d position=(%d,%d) sous_monde=%v lisible=%v -> %d sur ce niveau\n",
		conn.PID, champs.DataID, tampon, champs.X, champs.Y, champs.SousMonde, ok, n)

	out := nex.NewStreamOut(s)
	out.U32(0)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}
