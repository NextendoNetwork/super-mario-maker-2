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
func (m *magasinCommentaires) ajouterDessin(dataID, pid, image uint64, taille uint32, x, y uint16, sousMonde bool) int {
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
		X: x, Y: y, SousMonde: sousMonde,
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
// MESURE CHEZ NINTENDO le 2026-10-02 (capturas-smm2, quatre CommentInfo dans les 95) :
//
//	unk1   Uint64   le data_id du NIVEAU — pas un identifiant de commentaire
//	unk2   String   l'identifiant du commentaire :
//	                <AAAAMMJJhhmmss + 6 chiffres>_<PID auteur en hex>_<data_id en hex>
//	unk3   Uint8    0 ou 1, sens inconnu ; on garde 0, qui s'affiche
//	unk4   Uint8    le TYPE : 1 texte, 2 tampon, 0 dessin
//	unk5   Uint64   le PID de l'auteur
//	unk6/7 Uint16   la position X, Y dans le niveau
//	unk14  qBuffer  VIDE chez Nintendo
//	unk15  String   le texte
//	unk16  Uint16   le numero du tampon
//
// Nous mettions un compteur dans unk1, le pseudo dans unk2, une position nulle et le Mii
// dans unk14. Le type (0,1) pour le texte, trouve en balayant les combinaisons, est
// confirme ; celui du dessin, (0,0), aussi par elimination.
func ecrireCommentInfo(out *nex.StreamOut, c commentaire, i int) {
	s := out.Settings
	unk3, unk4 := typeCommentaire(c)
	image, tailleImg := imageDuCommentaire(c)

	f := nex.NewStreamOut(s)
	f.U64(c.DataID)
	f.String(identifiantCommentaire(c, i))
	f.U8(unk3)
	f.U8(unk4)
	f.U64(s.Publique(c.PID)) // U64 brut : traduit a la main, PID() le ferait seul
	f.U16(c.X)
	f.U16(c.Y)
	f.U8(0)       // unk8
	f.U8(0)       // unk9
	f.U16(0)      // unk10
	f.Bool(false) // unk11
	f.Bool(false) // unk12
	f.DateTime(uint64(nex.MakeDateTime(dateDe(c.Quand))))
	f.QBuffer(nil) // unk14 : vide chez Nintendo
	f.String(c.Texte)

	// La structure de l'image : imbriquee, donc sa propre en-tete.
	//
	// Champs releves dans CommentPictureReqGetInfoWithoutHeaders (kinnay) : url, type de
	// donnee, taille, certificat, nom de fichier — exactement la meme forme que le
	// descripteur des vignettes de niveau, qui fonctionne deja.
	//
	// Une adresse vide quand le commentaire n'a pas de dessin n'est pas un trou : c'est
	// la verite, et le jeu ne va rien chercher.
	pic := nex.NewStreamOut(s)
	if image != 0 {
		pic.String(fmt.Sprintf("%s/object/%d", storageURL, image))
		pic.U8(10) // type de donnee : /ds/1/comment/ selon la documentation
		pic.U32(tailleImg)
		pic.Buffer(courses.rootCA)
		pic.String(fmt.Sprintf("%d.bin", image))
	} else {
		// Chaines vides en longueur UN — un terminateur nul —, mesure chez Nintendo le
		// 2026-10-02 : 0100 00 · 00 · 00000000 · 00000000 · 0100 00, quinze octets. On
		// les ecrivait en longueur zero d'apres ocw-server ; la mesure gagne.
		pic.String("")
		pic.U8(0)
		pic.U32(0)
		pic.Buffer(nil)
		pic.String("")
	}
	if s.StructHeader {
		f.U8(0)
		f.Buffer(pic.Bytes())
	} else {
		f.Write(pic.Bytes())
	}

	f.U16(c.Tampon) // unk16 : le numero du tampon
	f.U8(0)         // unk17

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

	// La 95 recoit un Uint64 NU, la 94 une structure (data_id + ResultRange) : documente
	// chez kinnay et mesure chez Nintendo le 2026-10-02 (c442 : huit octets, le data_id,
	// rien d'autre). Lire la 95 comme une structure prenait le premier octet du data_id
	// pour une version, ses quatre suivants pour une longueur, et rendait les
	// commentaires du niveau 0 : aucun. C'est la liste que le jeu affiche DANS le niveau.
	in := nex.NewStreamIn(req.Body, s)
	p := in
	if req.Method == 94 && s.StructHeader {
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

// typeCommentaire rend (unk3, unk4). unk4 est le type, mesure chez Nintendo : 1 texte,
// 2 tampon ; 0 pour un dessin, par elimination et parce que c'est la valeur avec laquelle
// le jeu essayait de telecharger l'image.
func typeCommentaire(c commentaire) (uint8, uint8) {
	switch {
	case c.Image != 0:
		return 0, 0
	case c.Tampon != 0:
		return 0, 2
	default:
		return 0, 1
	}
}

// identifiantCommentaire : la forme de Nintendo, <date + 6 chiffres>_<auteur>_<niveau>.
// Les six chiffres sont les microsecondes chez Nintendo ; nous ne gardons que la seconde,
// donc on y met le rang du commentaire, ce qui suffit a rendre l'identifiant unique.
func identifiantCommentaire(c commentaire, i int) string {
	an, mois, jour, h, mi, se := dateDe(c.Quand)
	return fmt.Sprintf("%04d%02d%02d%02d%02d%02d%06d_%x_%x", an, mois, jour, h, mi, se, i%1000000, c.PID, c.DataID)
}

// imageDuCommentaire rend l'objet a servir pour un dessin.
//
// Les dessins enregistres AVANT la correction de la 90 designent la MINIATURE DE
// SIGNALEMENT (le JPEG de la 132) au lieu du dessin. Celui-ci est l'objet alloue juste
// avant, nomme rel-<niveau>-10 : on le reprend ici plutot que de reecrire le fichier.
func imageDuCommentaire(c commentaire) (uint64, uint32) {
	if c.Image == 0 {
		return 0, 0
	}
	attendu := fmt.Sprintf("rel-%d-10", c.DataID)
	courses.mu.RLock()
	defer courses.mu.RUnlock()
	if m := courses.byID[c.Image]; m != nil && m.Name == attendu {
		return c.Image, c.TailleImg
	}
	if m := courses.byID[c.Image-1]; m != nil && m.Name == attendu {
		return c.Image - 1, m.Size
	}
	return c.Image, c.TailleImg
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
	champs, _ := lirePosteCommentaire(p)
	dataID := champs.DataID
	// Puis une SECONDE structure, de douze octets : la taille du dessin, 64, 0 (mesure
	// chez Nintendo le 2026-10-02, c446 : 00 0c000000 13020000 40000000 00000000). On
	// lisait son en-tete comme deux Uint32 — « 12 » puis une taille decalee d'un octet,
	// 531 devenant 135936.
	_ = in.U8()
	q := in.Substream()
	taille := q.U32()

	if err := q.Err(); err != nil || taille == 0 || taille > 8<<20 {
		// Une taille absurde signale une lecture fausse, pas un dessin geant. On refuse
		// plutot que d'allouer un objet a partir d'un nombre qu'on ne comprend pas.
		fmt.Printf("[SMM2 Commentaires] prepare_picture(88) pid=%d : parametre douteux (taille=%d, err=%v)\n",
			conn.PID, taille, err)
		return nex.NewRMCError(s, 0x73, req.CallID, 0x00690002)
	}

	id := courses.alloc(conn.PID, fmt.Sprintf("rel-%d-10", dataID), 10, nil, nil, taille)
	url := fmt.Sprintf("%s/object/%d", storageURL, id)
	dessinsEnAttente.Store(id, dessinEnAttente{Niveau: dataID, PID: conn.PID, Taille: taille,
		X: champs.X, Y: champs.Y, SousMonde: champs.SousMonde})

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

// dessinEnAttente : ce que la 88 sait d'un dessin et que la 90 doit rattacher.
type dessinEnAttente struct {
	Niveau    uint64
	PID       uint64
	Taille    uint32
	X, Y      uint16
	SousMonde bool
}

// dessinsEnAttente : objet alloue par la 88 -> dessin. En memoire : la 90 suit la 88 de
// quelques secondes.
var dessinsEnAttente sync.Map

// smm2CompletePostObjectCommentPicture (89 et 90) : le dessin est arrive en entier.
//
// MESURE CHEZ NINTENDO le 2026-10-02 (capturas-smm2 c446, c450, c453). La sequence est :
//
//	88   preparer le DESSIN          -> une clef et un formulaire de televersement
//	132  preparer une autre image    -> « report-thumbnail_… » : la miniature de SIGNALEMENT
//	90   confirmer : la clef de la 88, la clef de la 132, puis le parametre de la 88
//
// Le dessin est donc l'objet de la 88, designe par la PREMIERE chaine de la 90. Nous
// prenions « le JPEG le plus recent du joueur » — c'est-a-dire la miniature de
// signalement de la 132 — et le jeu, qui attend le dessin, chargeait sans fin.
func smm2CompletePostObjectCommentPicture(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	in := nex.NewStreamIn(req.Body, s)
	p := in
	if s.StructHeader {
		_ = in.U8()
		p = in.Substream()
	}
	clef := p.String()
	signalement := p.String()

	var objet uint64
	if _, err := fmt.Sscanf(clef, "%d", &objet); err != nil || p.Err() != nil {
		fmt.Printf("[SMM2 Commentaires] complete_picture(%d) pid=%d : clef illisible %q (%v)\n", req.Method, conn.PID, clef, p.Err())
		return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, nil)
	}
	v, ok := dessinsEnAttente.LoadAndDelete(objet)
	if !ok {
		fmt.Printf("[SMM2 Commentaires] complete_picture(%d) pid=%d objet=%d : aucune 88 correspondante\n", req.Method, conn.PID, objet)
		return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, nil)
	}
	d := v.(dessinEnAttente)
	if d.PID != conn.PID {
		fmt.Printf("[SMM2 Commentaires] complete_picture(%d) pid=%d objet=%d appartient a pid=%d — refuse\n", req.Method, conn.PID, objet, d.PID)
		return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, nil)
	}
	taille := d.Taille
	if st, err := os.Stat(blobPath(objet)); err == nil && st.Size() > 0 {
		taille = uint32(st.Size())
	}
	courses.complete(objet, true)
	n := commentaires.ajouterDessin(d.Niveau, conn.PID, objet, taille, d.X, d.Y, d.SousMonde)
	fmt.Printf("[SMM2 Commentaires] complete_picture(%d) pid=%d dessin=%d (%d octets) niveau=%d position=(%d,%d) signalement=%q -> %d commentaire(s)\n",
		req.Method, conn.PID, objet, taille, d.Niveau, d.X, d.Y, signalement, n)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, nil)
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
