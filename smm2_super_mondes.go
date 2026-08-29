package main

// Les SUPER MONDES : la carte du monde qu'un createur assemble avec ses propres niveaux,
// publie sur son profil, et que les autres joueurs parcourent comme un jeu Mario complet.
//
// HUIT METHODES, 159 a 166. Avant ce fichier, aucune n'existait vraiment : la 160 et la 162
// rendaient une liste VIDE — un serveur vierge, ce qui etait honnete tant que personne ne
// pouvait en creer — et les six autres tombaient sur le repli generique `U32(0)`, qui n'est
// la forme d'AUCUNE d'entre elles.
//
// CE QU'ON N'A PAS EU A FAIRE, et c'est ce qui rend ce morceau abordable : le plan de la
// carte (`MapLayout`) est un QBuffer OPAQUE. On le range et on le rend tel quel, sans jamais
// l'ouvrir — contrairement aux niveaux, qu'il a fallu dechiffrer en AES-CBC pour en lire le
// style et le theme. Le serveur n'a pas besoin de comprendre la carte pour la servir.
//
// Les formes des structures viennent de la documentation PretendoNetwork et d'ocw-server,
// lus pour les FAITS DU PROTOCOLE ; le code ci-dessous est ecrit de zero. ocw-server n'a pas
// de licence, donc rien n'en est recopie.
//
// LES CHAMPS QU'ON NE COMPREND PAS SONT CONSERVES, PAS REMIS A ZERO. Unk5 sur le monde,
// Unk1 et Unk20 a Unk23 sur la progression : c'est le JEU qui les a ecrits, lui seul
// sait ce qu'il y met. Les rendre a zero serait corrompre silencieusement le monde d'un
// joueur ; on les fait donc simplement l'aller-retour. C'est aussi ce qui rendra la premiere
// mesure sur console lisible — on verra ce que la console envoie vraiment.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

// superMonde : un monde publie. Un joueur en a AU PLUS UN, comme dans le vrai jeu.
type superMonde struct {
	ID        string   `json:"id"`
	OwnerPID  uint64   `json:"owner_pid"`
	Plan      []byte   `json:"plan"`      // MapLayout — opaque, jamais ouvert
	Niveaux   []uint64 `json:"niveaux"`   // data_id des niveaux qui le composent
	Mondes    uint8    `json:"mondes"`    // nombre de mondes (les « W1, W2… »)
	Miniature string   `json:"miniature"` // data_id de la vignette (param.Unk2)
	// AncienUnk2 relit les mondes ecrits avant qu'on sache ce que ce champ contenait.
	// Renommer une cle JSON sans prevoir la relecture, c'est effacer en silence ce que les
	// joueurs avaient deja publie.
	AncienUnk2  string `json:"unk2,omitempty"`
	TypePlanete uint8  `json:"type_planete"` // param.Unk4 : type de planete de la carte
	Unk5        uint32 `json:"unk5"`         // conserve tel quel
	CreeLe      int64  `json:"cree_le"`      // Unix, pour le DateTime de la fiche
	MajLe       int64  `json:"maj_le"`       // derniere republication
}

// progressionMonde : ou en est UN joueur dans UN monde. La cle est « pid:id » — ce n'est
// pas la progression du proprietaire, c'est celle de chaque visiteur, et deux personnes
// parcourent le meme monde en meme temps sans se marcher dessus.
type progressionMonde struct {
	Vies     uint8  `json:"vies"`
	Pieces   uint8  `json:"pieces"`
	Points   uint32 `json:"points"`
	CourseID uint64 `json:"course_id"` // le niveau ou le joueur se trouve
	Unk1     uint64 `json:"unk1"`
	Unk10    uint32 `json:"unk10"`
	Unk11    []byte `json:"unk11"`
	Unk20    uint8  `json:"unk20"`
	Unk21    uint8  `json:"unk21"`
	Unk22    uint8  `json:"unk22"`
	Unk23    uint8  `json:"unk23"`
}

type magasinSuperMondes struct {
	mu      sync.RWMutex
	parPID  map[uint64]*superMonde
	parID   map[string]*superMonde
	progres map[string]*progressionMonde
	chemin  string
}

type fichierSuperMondes struct {
	Mondes  map[string]*superMonde       `json:"mondes"`
	Progres map[string]*progressionMonde `json:"progres"`
}

var superMondes = &magasinSuperMondes{
	parPID:  map[uint64]*superMonde{},
	parID:   map[string]*superMonde{},
	progres: map[string]*progressionMonde{},
}

func cleProgres(pid uint64, id string) string { return fmt.Sprintf("%d:%s", pid, id) }

func (m *magasinSuperMondes) charger(dir string) {
	m.chemin = filepath.Join(dir, "smm2_super_mondes.json")
	b, err := os.ReadFile(m.chemin)
	if err != nil {
		return
	}
	// Fichier illisible : PRESERVE, jamais ecrase. C'est le seul exemplaire des mondes
	// construits par les joueurs, et un monde represente des heures de travail — bien plus
	// qu'un niveau isole. Mieux vaut demarrer sans eux et pouvoir les recuperer a la main.
	var f fichierSuperMondes
	if err := json.Unmarshal(b, &f); err != nil || f.Mondes == nil {
		fmt.Printf("[SMM2 SuperMondes] %s illisible (%v) — conserve tel quel, demarrage a vide\n", m.chemin, err)
		return
	}
	m.parID = f.Mondes
	if f.Progres != nil {
		m.progres = f.Progres
	}
	for _, sm := range m.parID {
		m.parPID[sm.OwnerPID] = sm
		if sm.Miniature == "" && sm.AncienUnk2 != "" {
			sm.Miniature, sm.AncienUnk2 = sm.AncienUnk2, ""
		}
	}
	fmt.Printf("[SMM2 SuperMondes] %d monde(s), %d progression(s)\n", len(m.parID), len(m.progres))
}

// ecrire enregistre le magasin. Ecriture atomique — temporaire puis renommage — pour qu'une
// coupure laisse l'ancien fichier intact plutot qu'un JSON tronque.
func (m *magasinSuperMondes) ecrire() {
	m.mu.RLock()
	if m.chemin == "" {
		m.mu.RUnlock()
		return
	}
	b, err := json.Marshal(fichierSuperMondes{Mondes: m.parID, Progres: m.progres})
	m.mu.RUnlock()
	if err != nil {
		fmt.Printf("[SMM2 SuperMondes] serialisation impossible: %v\n", err)
		return
	}
	tmp := m.chemin + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		fmt.Printf("[SMM2 SuperMondes] ecriture impossible: %v\n", err)
		return
	}
	if err := os.Rename(tmp, m.chemin); err != nil {
		fmt.Printf("[SMM2 SuperMondes] renommage impossible: %v\n", err)
	}
}

func (m *magasinSuperMondes) parProprietaire(pid uint64) *superMonde {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.parPID[pid]
}

func (m *magasinSuperMondes) parIdentifiant(id string) *superMonde {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.parID[id]
}

// identifiantMondeDe fabrique l'identifiant d'un super monde.
//
// TRENTE caracteres, en forme d'UUID. Ce n'est pas un choix esthetique : ocw-server tronque
// explicitement son UUID a trente (« Only use the first 30 charactors of the UUID ») et
// distingue ensuite ses mondes de ceux des serveurs de Nintendo par la LONGUEUR — au-dela de
// trente-cinq caracteres, c'est un identifiant Nintendo. La largeur porte donc du sens, et
// nos trente-deux hexadecimaux nus tombaient du mauvais cote.
//
// Il reste DERIVE DU PID, donc stable : republier un monde modifie ne lui donne pas une
// nouvelle identite, et les visiteurs gardent leur progression. ocw-server, lui, tire un
// UUID neuf a chaque enregistrement et perd ce lien.
func identifiantMondeDe(pid uint64) string {
	h := fmt.Sprintf("%016x%016x", pid, pid*0x9E3779B97F4A7C15)
	// 8-4-4-4-6 : trente caracteres tirets compris.
	return fmt.Sprintf("%s-%s-%s-%s-%s", h[0:8], h[8:12], h[12:16], h[16:20], h[20:26])
}

// SMM2SuperMondeIDDe : ce que la bibliotheque appelle pour remplir UserInfo.SuperWorldId.
// C'est CE champ qui fait apparaitre le bouton « super monde » sur le profil d'un createur ;
// il etait ecrit en chaine vide avec la note « personne n'a de super monde chez nous », et
// cette phrase vient de cesser d'etre vraie.
func SMM2SuperMondeIDDe(pid uint64) string {
	if sm := superMondes.parProprietaire(pid); sm != nil {
		return sm.ID
	}
	return ""
}

// --- ecriture des fiches ----------------------------------------------------

// miniatureRattachee cherche le fichier televerse pour un parent NOMME (le super monde a
// un identifiant en chaine, pas un data_id numerique, donc `miniatureDe` ne s'applique pas).
//
// Le TYPE de relation d'une vignette de super monde n'est documente nulle part : on prend
// donc le premier fichier rattache a cet identifiant, quel que soit son type, et on
// journalise ce type. Une seule publication depuis une console suffira a le fixer.
func miniatureRattachee(parent string) (uint64, uint32, uint8) {
	prefixe := "rel-" + parent + "-"
	courses.mu.Lock()
	defer courses.mu.Unlock()
	for id, m := range courses.byID {
		if !strings.HasPrefix(m.Name, prefixe) {
			continue
		}
		var t uint32
		fmt.Sscanf(strings.TrimPrefix(m.Name, prefixe), "%d", &t)
		return id, m.Size, uint8(t)
	}
	return 0, 0, 0
}

// miniatureDuMonde rend l'objet de la vignette : son data_id, sa taille et son type de
// relation, tous LUS dans le catalogue. Zero si elle manque — auquel cas on annonce quand
// meme une adresse, faute de pouvoir omettre le champ, mais au moins on le journalise.
func miniatureDuMonde(sm *superMonde) (uint64, uint32, uint8) {
	var id uint64
	if _, err := fmt.Sscanf(sm.Miniature, "%d", &id); err != nil || id == 0 {
		// Repli : l'ancienne recherche par prefixe, pour un monde enregistre avant que
		// l'on comprenne d'ou venait cet identifiant.
		return miniatureRattachee(sm.ID)
	}
	courses.mu.Lock()
	m := courses.byID[id]
	courses.mu.Unlock()
	if m == nil {
		fmt.Printf("[SMM2 SuperMondes] vignette %d annoncee par le monde %s mais ABSENTE du catalogue\n", id, sm.ID)
		return 0, 0, 0
	}
	var t uint32
	if i := strings.LastIndex(m.Name, "-"); i >= 0 {
		fmt.Sscanf(m.Name[i+1:], "%d", &t)
	}
	return id, m.Size, uint8(t)
}

// ecrireWorldMapInfo serialise un super monde. Revision 0 : contrairement a UserInfo, qui
// exige la 3, ocw-server ne pose aucune etiquette de revision sur cette structure.
func ecrireWorldMapInfo(out *nex.StreamOut, sm *superMonde) {
	s := out.Settings
	f := nex.NewStreamOut(s)

	f.String(sm.ID)
	f.PID(sm.OwnerPID)
	f.QBuffer(sm.Plan) // le plan de la carte, rendu tel qu'il est arrive

	// La vignette : un RelationObjectReqGetInfo, c'est-a-dire l'ADRESSE de l'image, pas
	// l'image. Meme forme que pour les niveaux (voir ecrireMiniature).
	// La vignette est designee par le data_id que la console nous a donne a
	// l'enregistrement, pas par une recherche sur l'identifiant du monde. Le type de
	// relation est relu dans le catalogue plutot que suppose : ocw-server ecrit 50 a cet
	// endroit, mais leur vignette vient d'un volcado importe, alors que 15 est ce que
	// CETTE console vient de televerser. Entre les deux, la mesure gagne.
	idVig, taille, typeVig := miniatureDuMonde(sm)

	r := nex.NewStreamOut(s)
	r.String(fmt.Sprintf("%s/object/%d", storageURL, idVig))
	r.U8(typeVig)
	r.U32(taille)
	r.Buffer(courses.rootCA)
	r.String(fmt.Sprintf("%d.bin", idVig))
	if s.StructHeader {
		f.U8(0)
		f.Buffer(r.Bytes())
	} else {
		f.Write(r.Bytes())
	}

	f.U8(sm.Mondes)
	f.U8(uint8(len(sm.Niveaux)))
	// Unk2 de la fiche = le TYPE DE PLANETE, et il vient du champ Unk4 du parametre
	// d'enregistrement. J'avais range ce champ dans Unk6 et ecrit zero ici : les deux
	// etaient faux. ocw-server les relie explicitement (param.Unk4 -> planet_type -> Unk2).
	f.U8(sm.TypePlanete)

	// DateTime NEX = CHAMP DE BITS, pas un instant Unix. Ecrire les secondes telles quelles
	// est le defaut qui affichait « 06/10/0026 » sur les niveaux.
	t := time.Unix(sm.MajLe, 0).UTC()
	f.DateTime(uint64(nex.MakeDateTime(t.Year(), int(t.Month()), t.Day(), t.Hour(), t.Minute(), t.Second())))

	nex.WriteList(f, sm.Niveaux, func(o *nex.StreamOut, v uint64) { o.U64(v) })
	nex.WriteMap(f, map[uint8]uint32{}, func(o *nex.StreamOut, k uint8) { o.U8(k) }, func(o *nex.StreamOut, v uint32) { o.U32(v) })
	// La queue de la fiche. Dans le volcado reel ces trois valeurs valent toujours 3, 1 et 1 ;
	// on rend le Unk5 depose par le jeu et on fixe les deux derniers a UN. Nous ecrivions un
	// zero final, qui n'apparait dans aucune mesure.
	f.U32(sm.Unk5)
	f.U8(1)
	f.U8(1)

	if s.StructHeader {
		out.U8(0)
		out.Buffer(f.Bytes())
		return
	}
	out.Write(f.Bytes())
}

func ecrireWorldMapProgressInfo(out *nex.StreamOut, id string, p *progressionMonde) {
	s := out.Settings
	f := nex.NewStreamOut(s)

	f.String(id)
	f.U8(p.Unk20)
	f.U8(p.Vies)
	f.U8(p.Pieces)
	f.U32(p.Points)
	f.U8(p.Unk21)
	f.U8(p.Unk22)
	f.U8(p.Unk23)
	f.U64(p.CourseID)
	f.U64(p.Unk1)
	f.QBuffer(p.Unk11)
	f.U32(p.Unk10)

	if s.StructHeader {
		out.U8(0)
		out.Buffer(f.Bytes())
		return
	}
	out.Write(f.Bytes())
}

// --- les huit methodes ------------------------------------------------------

// 159 RegisterWorldMap : le createur publie (ou republie) son monde.
func smm2RegisterWorldMap(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	in := nex.NewStreamIn(req.Body, s)
	_ = in.U8()
	p := in.Substream()

	plan := p.QBuffer()
	niveaux := nex.ReadList(p, func(i *nex.StreamIn) uint64 { return i.U64() })
	// Unk2 n'est PAS un champ mysterieux : c'est le data_id de la vignette du monde, en
	// chaine. La console la televerse juste avant, par la methode 132, avec le type de
	// relation 15 — puis nous en donne l'identifiant ici. Mesure le 2026-08-29 :
	//
	//   prepare-relation(132) parent=2660 type=15 taille=51204 -> data_id=2667
	//   register(159) ... unk2="2667"
	//
	// Nous cherchions la vignette par l'identifiant du MONDE, ne la trouvions pas, et
	// annoncions /object/0 : une image inexistante. La console la demandait en boucle et
	// affichait « service non disponible ».
	miniature := p.String()
	mondes := p.U8()
	typePlanete := p.U8()
	unk5 := p.U32()

	if err := p.Err(); err != nil {
		fmt.Printf("[SMM2 SuperMondes] register(159) pid=%d : parametre illisible (%v) brut=%x\n",
			conn.PID, err, req.Body)
		return nex.NewRMCError(s, 0x73, req.CallID, 0x00690002)
	}

	id := identifiantMondeDe(conn.PID)
	maintenant := nowUnix()

	superMondes.mu.Lock()
	sm := superMondes.parID[id]
	if sm == nil {
		sm = &superMonde{ID: id, OwnerPID: conn.PID, CreeLe: maintenant}
		superMondes.parID[id] = sm
		superMondes.parPID[conn.PID] = sm
	}
	sm.Plan, sm.Niveaux, sm.Mondes = plan, niveaux, mondes
	sm.Miniature, sm.TypePlanete, sm.Unk5 = miniature, typePlanete, unk5
	sm.MajLe = maintenant
	superMondes.mu.Unlock()
	superMondes.ecrire()

	fmt.Printf("[SMM2 SuperMondes] register(159) pid=%d id=%s : %d niveau(x), %d monde(s), plan %do, vignette=%s planete=%d unk5=%d\n",
		conn.PID, id, len(niveaux), mondes, len(plan), miniature, typePlanete, unk5)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, nil)
}

// 160 GetWorldMap : les fiches demandees, plus un resultat PAR IDENTIFIANT DEMANDE, dans
// l'ordre. Melanger les deux listes desynchronise la lecture du client — meme piege que
// GetCourses(70).
func smm2GetWorldMap(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	in := nex.NewStreamIn(req.Body, s)
	_ = in.U8()
	p := in.Substream()

	ids := nex.ReadList(p, func(i *nex.StreamIn) string { return i.String() })
	option := p.U32()

	if err := p.Err(); err != nil {
		fmt.Printf("[SMM2 SuperMondes] get(160) : parametre illisible (%v) brut=%x\n", err, req.Body)
		return nex.NewRMCError(s, 0x73, req.CallID, 0x00690002)
	}

	trouves := make([]*superMonde, 0, len(ids))
	n := 0
	for _, id := range ids {
		sm := superMondes.parIdentifiant(id)
		trouves = append(trouves, sm)
		if sm != nil {
			n++
		}
	}

	out := nex.NewStreamOut(s)
	out.U32(uint32(n))
	for _, sm := range trouves {
		if sm != nil {
			ecrireWorldMapInfo(out, sm)
		}
	}
	out.U32(uint32(len(trouves)))
	for _, sm := range trouves {
		if sm != nil {
			out.Result(0)
		} else {
			out.Result(0x00690004) // DataStore::NotFound
		}
	}

	fmt.Printf("[SMM2 SuperMondes] get(160) option=0x%x demande %d -> %d fiche(s)\n", option, len(ids), n)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}

// 162 SearchWorldMapPickUp : la selection proposee dans Course World. On rend les mondes
// les plus recemment publies, plafonnes au nombre demande.
func smm2SearchWorldMapPickUp(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	in := nex.NewStreamIn(req.Body, s)
	_ = in.U8()
	p := in.Substream()
	combien := p.U32()

	if err := p.Err(); err != nil {
		fmt.Printf("[SMM2 SuperMondes] pickup(162) : parametre illisible (%v) brut=%x\n", err, req.Body)
		return nex.NewRMCError(s, 0x73, req.CallID, 0x00690002)
	}
	// Un plafond meme quand le client n'en demande pas : sans lui, la reponse grandit avec
	// le catalogue et finit par depasser ce qu'une trame peut porter.
	if combien == 0 || combien > 50 {
		combien = 50
	}

	superMondes.mu.RLock()
	choix := make([]*superMonde, 0, len(superMondes.parID))
	for _, sm := range superMondes.parID {
		choix = append(choix, sm)
	}
	superMondes.mu.RUnlock()

	// Tri du plus recent au plus ancien. L'egalite se departage par l'identifiant : sans
	// cette seconde cle, l'ordre de parcours d'une carte Go etant aleatoire, la selection
	// sauterait a chaque consultation sous les yeux du joueur.
	trierMondes(choix)
	if uint32(len(choix)) > combien {
		choix = choix[:combien]
	}

	out := nex.NewStreamOut(s)
	out.U32(uint32(len(choix)))
	for _, sm := range choix {
		ecrireWorldMapInfo(out, sm)
	}

	fmt.Printf("[SMM2 SuperMondes] pickup(162) demande %d -> %d monde(s)\n", combien, len(choix))
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}

// 161 SearchWorldMapPlayedBy : les mondes qu'un joueur a parcourus. On les deduit des
// progressions enregistrees — c'est exactement ce qu'elles savent.
func smm2SearchWorldMapPlayedBy(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	prefixe := fmt.Sprintf("%d:", conn.PID)

	// Les deux Uint32 du parametre sont une ETENDUE — depart et nombre — et non l'identifiant
	// d'un autre joueur, comme je l'avais craint. ocw-server les passe tels quels en LIMIT et
	// OFFSET. La reponse reste celle du joueur CONNECTE.
	in := nex.NewStreamIn(req.Body, s)
	_ = in.U8()
	p := in.Substream()
	depart := p.U32()
	combien := p.U32()
	if err := p.Err(); err != nil {
		fmt.Printf("[SMM2 SuperMondes] played-by(161) : parametre illisible (%v) brut=%x\n", err, req.Body)
		return nex.NewRMCError(s, 0x73, req.CallID, 0x00690002)
	}

	superMondes.mu.RLock()
	choix := make([]*superMonde, 0, 8)
	for cle := range superMondes.progres {
		if !strings.HasPrefix(cle, prefixe) {
			continue
		}
		if sm := superMondes.parID[strings.TrimPrefix(cle, prefixe)]; sm != nil {
			choix = append(choix, sm)
		}
	}
	superMondes.mu.RUnlock()
	trierMondes(choix)

	// L'etendue est APPLIQUEE, pas seulement lue — c'est le defaut qu'on vient de corriger
	// sur « mes niveaux » (methode 74), et il n'y a aucune raison de le refaire ici.
	total := len(choix)
	if depart >= uint32(total) {
		choix = nil
	} else {
		choix = choix[depart:]
		if combien > 0 && combien < uint32(len(choix)) {
			choix = choix[:combien]
		}
	}

	out := nex.NewStreamOut(s)
	out.U32(uint32(len(choix)))
	for _, sm := range choix {
		ecrireWorldMapInfo(out, sm)
	}
	fmt.Printf("[SMM2 SuperMondes] played-by(161) pid=%d etendue=%d+%d -> %d/%d monde(s)\n",
		conn.PID, depart, combien, len(choix), total)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}

// 163 GetWorldMapProgress : ou en est CE joueur dans CE monde.
func smm2GetWorldMapProgress(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	in := nex.NewStreamIn(req.Body, s)
	_ = in.U8()
	p := in.Substream()
	id := p.String()

	if err := p.Err(); err != nil {
		fmt.Printf("[SMM2 SuperMondes] progress(163) : parametre illisible (%v) brut=%x\n", err, req.Body)
		return nex.NewRMCError(s, 0x73, req.CallID, 0x00690002)
	}

	superMondes.mu.RLock()
	pr := superMondes.progres[cleProgres(conn.PID, id)]
	superMondes.mu.RUnlock()
	// Pas encore commence : une progression VIDE, pas une erreur. Le joueur qui ouvre un
	// monde pour la premiere fois est le cas normal, pas une panne.
	if pr == nil {
		pr = &progressionMonde{}
	}

	out := nex.NewStreamOut(s)
	ecrireWorldMapProgressInfo(out, id, pr)
	fmt.Printf("[SMM2 SuperMondes] progress(163) pid=%d id=%s -> niveau=%d vies=%d pieces=%d points=%d\n",
		conn.PID, id, pr.CourseID, pr.Vies, pr.Pieces, pr.Points)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}

// 164 DeleteWorldMap : le createur retire son monde. AUCUN parametre — c'est toujours le
// sien, identifie par la connexion.
func smm2DeleteWorldMap(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings

	superMondes.mu.Lock()
	sm := superMondes.parPID[conn.PID]
	if sm != nil {
		delete(superMondes.parID, sm.ID)
		delete(superMondes.parPID, conn.PID)
		// Les progressions des VISITEURS partent avec le monde : elles designent des
		// niveaux d'une carte qui n'existe plus, et les garder ferait revenir un joueur
		// dans un monde disparu.
		for cle := range superMondes.progres {
			if strings.HasSuffix(cle, ":"+sm.ID) {
				delete(superMondes.progres, cle)
			}
		}
	}
	superMondes.mu.Unlock()
	if sm != nil {
		superMondes.ecrire()
		fmt.Printf("[SMM2 SuperMondes] delete(164) pid=%d id=%s supprime\n", conn.PID, sm.ID)
	} else {
		fmt.Printf("[SMM2 SuperMondes] delete(164) pid=%d : aucun monde a supprimer\n", conn.PID)
	}
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, nil)
}

// 165 InitializeWorldMapProgress : le joueur (re)commence un monde depuis le debut.
func smm2InitializeWorldMapProgress(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	in := nex.NewStreamIn(req.Body, s)
	_ = in.U8()
	p := in.Substream()
	id := p.String()
	unk1 := p.U32()

	if err := p.Err(); err != nil {
		fmt.Printf("[SMM2 SuperMondes] init(165) : parametre illisible (%v) brut=%x\n", err, req.Body)
		return nex.NewRMCError(s, 0x73, req.CallID, 0x00690002)
	}

	superMondes.mu.Lock()
	superMondes.progres[cleProgres(conn.PID, id)] = &progressionMonde{Unk20: 1}
	superMondes.mu.Unlock()
	superMondes.ecrire()

	fmt.Printf("[SMM2 SuperMondes] init(165) pid=%d id=%s unk1=%d : progression remise a zero\n",
		conn.PID, id, unk1)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, nil)
}

// 166 UpdateWorldMapProgress : le joueur avance. Appelee souvent — a chaque niveau termine.
func smm2UpdateWorldMapProgress(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	in := nex.NewStreamIn(req.Body, s)
	_ = in.U8()
	p := in.Substream()

	pr := &progressionMonde{}
	id := p.String()
	pr.CourseID = p.U64()
	pr.Unk1 = p.U64()
	pr.Unk20 = p.U8()
	pr.Unk21 = p.U8()
	pr.Unk22 = p.U8()
	pr.Unk23 = p.U8()
	pr.Vies = p.U8()
	pr.Pieces = p.U8()
	pr.Points = p.U32()
	pr.Unk10 = p.U32()
	pr.Unk11 = p.QBuffer()

	if err := p.Err(); err != nil {
		fmt.Printf("[SMM2 SuperMondes] update(166) : parametre illisible (%v) brut=%x\n", err, req.Body)
		return nex.NewRMCError(s, 0x73, req.CallID, 0x00690002)
	}

	superMondes.mu.Lock()
	superMondes.progres[cleProgres(conn.PID, id)] = pr
	superMondes.mu.Unlock()
	superMondes.ecrire()

	fmt.Printf("[SMM2 SuperMondes] update(166) pid=%d id=%s niveau=%d vies=%d pieces=%d points=%d\n",
		conn.PID, id, pr.CourseID, pr.Vies, pr.Pieces, pr.Points)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, nil)
}

// trierMondes ordonne du plus recemment publie au plus ancien.
//
// L'egalite se departage par l'identifiant. Sans cette seconde cle l'ordre serait celui du
// parcours d'une carte Go, volontairement aleatoire : la selection de Course World changerait
// a chaque consultation, ce qui se voit immediatement a l'ecran.
func trierMondes(l []*superMonde) {
	sort.Slice(l, func(a, b int) bool {
		if l[a].MajLe != l[b].MajLe {
			return l[a].MajLe > l[b].MajLe
		}
		return l[a].ID < l[b].ID
	})
}
