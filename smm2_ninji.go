package main

// Les NINJI SPEEDRUNS : methodes 85, 86, 102, 153, 154, 156, 157 et 169.
//
// Avant ce fichier, seules la 153 et la 154 repondaient — « aucun evenement » — et les six
// autres tombaient sur le repli generique : le menu des Ninji s'ouvrait vide.
//
// FORMES MESUREES CHEZ NINTENDO le 2026-10-02 (capturas-smm2, connexion k03), toutes
// relues sans un octet de reste, et confrontees a la documentation de kinnay
// (Data-Store-Protocol-(SMM-2)). Deux ecarts avec elle, tranches par la mesure :
// EventCourseInfo porte TOUJOURS ses deux booleens et ses trois Uint32 de revision 1, que
// la documentation donne pour conditionnels (options 0x40 et 0x100) ; le jeu ne les
// demande pas et Nintendo les envoie quand meme.
//
// D'OU VIENNENT LES EVENEMENTS. Chez Nintendo, les 21 evenements sont des niveaux faits
// par Nintendo, servis depuis son CDN. Nous ne les copions pas : un evenement Nextendo est
// un niveau DE NOTRE CATALOGUE, designe dans smm2_ninji.json (dans le repertoire des
// objets) :
//
//	[{"data_id": 1234, "debut": 1790000000, "fin": 1790604800}]
//
// debut et fin en secondes Unix. Sans ce fichier, aucun evenement — exactement le
// comportement d'avant. Les temps et les fantomes sont ceux des joueurs de Nextendo.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

// typeRelationFantome : le type que la 132 annonce pour un fantome d'evenement (mesure,
// c217 : 0x0d, avec la taille du fantome juste apres). La fiche, elle, le designe par le
// type 40 — /ds/1/relation_data/event_course_ghost/ dans la documentation.
const (
	typeRelationFantome = 13
	typeFantomeFiche    = 40
)

// Les bornes de l'histogramme (156), mesurees : de 10 s a 120 s par pas d'une seconde,
// soit 110 cases. Les medailles se lisent aux centiles 10 (or), 30 (argent), 50 (bronze).
const (
	histoDebutMs = 10000
	histoFinMs   = 120000
	histoPasMs   = 1000
)

type evenementNinji struct {
	DataID uint64 `json:"data_id"`
	Debut  int64  `json:"debut"`
	Fin    int64  `json:"fin"`
}

type resultatNinji struct {
	TempsMs uint32 `json:"temps_ms"`
	Fantome uint64 `json:"fantome,omitempty"` // objet televerse par la 132, type 13
	Taille  uint32 `json:"taille,omitempty"`
	Envois  uint32 `json:"envois"`
}

type magasinNinji struct {
	mu         sync.RWMutex
	evenements []evenementNinji
	// resultats : evenement -> joueur -> meilleur resultat.
	resultats map[uint64]map[uint64]*resultatNinji
	dir       string
}

var ninji = &magasinNinji{resultats: map[uint64]map[uint64]*resultatNinji{}}

func (m *magasinNinji) charger(dir string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dir = dir
	if b, err := os.ReadFile(filepath.Join(dir, "smm2_ninji.json")); err == nil {
		var ev []evenementNinji
		if err := json.Unmarshal(b, &ev); err != nil {
			fmt.Printf("[SMM2 Ninji] smm2_ninji.json illisible (%v) — aucun evenement\n", err)
		} else {
			m.evenements = ev
		}
	}
	if b, err := os.ReadFile(filepath.Join(dir, "smm2_ninji_resultats.json")); err == nil {
		var r map[uint64]map[uint64]*resultatNinji
		if json.Unmarshal(b, &r) == nil && r != nil {
			m.resultats = r
		}
	}
	fmt.Printf("[SMM2 Ninji] %d evenement(s), resultats sur %d\n", len(m.evenements), len(m.resultats))
}

func (m *magasinNinji) ecrireLocked() {
	if m.dir == "" {
		return
	}
	b, err := json.Marshal(m.resultats)
	if err != nil {
		return
	}
	chemin := filepath.Join(m.dir, "smm2_ninji_resultats.json")
	if os.WriteFile(chemin+".tmp", b, 0o644) == nil {
		os.Rename(chemin+".tmp", chemin)
	}
}

// liste rend les evenements commences, du plus recent au plus ancien — l'ordre de la 86.
func (m *magasinNinji) liste(maintenant int64) []evenementNinji {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var l []evenementNinji
	for _, e := range m.evenements {
		if e.Debut <= maintenant {
			l = append(l, e)
		}
	}
	sort.Slice(l, func(a, b int) bool {
		if l[a].Debut != l[b].Debut {
			return l[a].Debut > l[b].Debut
		}
		return l[a].DataID > l[b].DataID
	})
	return l
}

func (m *magasinNinji) evenement(dataID uint64) (evenementNinji, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, e := range m.evenements {
		if e.DataID == dataID {
			return e, true
		}
	}
	return evenementNinji{}, false
}

// enregistrer garde le MEILLEUR temps d'un joueur, avec son fantome. Un temps moins bon
// est compte (Envois) mais ne remplace rien.
func (m *magasinNinji) enregistrer(dataID, pid uint64, tempsMs uint32, fantome uint64, taille uint32) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	parPID := m.resultats[dataID]
	if parPID == nil {
		parPID = map[uint64]*resultatNinji{}
		m.resultats[dataID] = parPID
	}
	r := parPID[pid]
	if r == nil {
		r = &resultatNinji{}
		parPID[pid] = r
	}
	r.Envois++
	meilleur := r.TempsMs == 0 || tempsMs < r.TempsMs
	if meilleur {
		r.TempsMs, r.Fantome, r.Taille = tempsMs, fantome, taille
	}
	m.ecrireLocked()
	return meilleur
}

func (m *magasinNinji) resultat(dataID, pid uint64) *resultatNinji {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if r := m.resultats[dataID][pid]; r != nil {
		c := *r
		return &c
	}
	return nil
}

// temps rend les meilleurs temps de tous les joueurs d'un evenement, avec leur PID.
type tempsJoueur struct {
	PID uint64
	resultatNinji
}

func (m *magasinNinji) temps(dataID uint64) []tempsJoueur {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var l []tempsJoueur
	for pid, r := range m.resultats[dataID] {
		if r.TempsMs > 0 {
			l = append(l, tempsJoueur{pid, *r})
		}
	}
	sort.Slice(l, func(a, b int) bool {
		if l[a].TempsMs != l[b].TempsMs {
			return l[a].TempsMs < l[b].TempsMs
		}
		return l[a].PID < l[b].PID
	})
	return l
}

func dateTimeUnix(u int64) uint64 {
	t := time.Unix(u, 0).UTC()
	return uint64(nex.MakeDateTime(t.Year(), int(t.Month()), t.Day(), t.Hour(), t.Minute(), t.Second()))
}

// encadrer ecrit f comme structure de version v.
func encadrer(out *nex.StreamOut, v uint8, f *nex.StreamOut) {
	out.U8(v)
	out.Buffer(f.Bytes())
}

// ecrireReferenceFantome : RelationObjectReqGetInfo, vide si le joueur n'a pas de fantome.
// Vide = chaines de longueur un, comme le bloc d'image d'un commentaire (mesure).
func ecrireReferenceFantome(out *nex.StreamOut, objet uint64, taille uint32) {
	s := out.Settings
	f := nex.NewStreamOut(s)
	if objet != 0 {
		f.String(fmt.Sprintf("%s/object/%d", storageURL, objet))
		f.U8(typeFantomeFiche)
		f.U32(taille)
		f.Buffer(courses.rootCA)
		f.String(fmt.Sprintf("%d.bin", objet))
	} else {
		f.String("")
		f.U8(0)
		f.U32(0)
		f.Buffer(nil)
		f.String("")
	}
	encadrer(out, 0, f)
}

// ecrireMiniatureEvenement : EventCourseThumbnail — url, en-tetes, taille, certificat,
// nom. La meme adresse que la vignette du niveau ; Nintendo n'envoie aucun en-tete.
func ecrireMiniatureEvenement(out *nex.StreamOut, dataID uint64, relType uint8) {
	s := out.Settings
	id, taille := miniatureDe(dataID, relType)
	f := nex.NewStreamOut(s)
	f.String(fmt.Sprintf("%s/object/%d", storageURL, id))
	f.U32(0)
	f.U32(taille)
	f.Buffer(courses.rootCA)
	f.String(fmt.Sprintf("%d.bin", id))
	encadrer(out, 0, f)
}

// ecrireEventCourseInfo : la fiche d'un evenement, revision 1. `option` est le masque du
// jeu (0x23b dans toutes les mesures) ; on n'omet que les blocs que la documentation dit
// conditionnels ET que la mesure n'a jamais montres absents — c'est-a-dire aucun avec
// 0x23b.
func ecrireEventCourseInfo(out *nex.StreamOut, e evenementNinji, c *courseMeta, pid uint64, option uint32) {
	s := out.Settings
	f := nex.NewStreamOut(s)
	f.U64(e.DataID)
	f.String(c.Name)
	f.String(c.Description)
	f.U8(c.Style)
	f.U8(c.Theme)
	f.Bool(true)  // toujours vrai dans les 21 fiches mesurees
	f.Bool(false) // toujours faux
	f.DateTime(dateTimeUnix(e.Debut))
	if option&0x2 != 0 {
		g := nex.NewStreamOut(s)
		g.String(fmt.Sprintf("%s/object/%d", storageURL, e.DataID))
		g.U32(0) // aucun en-tete
		g.U32(c.Size)
		g.Buffer(courses.rootCA)
		g.U64(e.DataID)
		encadrer(f, 0, g)
	}
	tous := ninji.temps(e.DataID)
	if option&0x1 != 0 {
		// Trois compteurs, cles 0, 1, 2. Leur sens n'est pas documente ; chez Nintendo la
		// cle 1 est toujours la plus grande, puis la 2, puis la 0. On y met, a titre de
		// deduction : 0 les joueurs classes, 1 les envois, 2 les envois aussi (nous ne
		// distinguons pas encore reussite et tentative).
		var envois uint32
		for _, t := range tous {
			envois += t.Envois
		}
		f.U32(3)
		f.U8(0)
		f.U32(uint32(len(tous)))
		f.U8(1)
		f.U32(envois)
		f.U8(2)
		f.U32(envois)
	}
	{
		u := nex.NewStreamOut(s)
		u.U64(0)
		u.U32(0xFFFFFFFF)
		encadrer(f, 0, u) // UnknownStruct6 : ces deux valeurs dans les 21 fiches
	}
	f.U8(1) // 1 dans 19 fiches sur 21
	if option&0x10 != 0 {
		ecrireMiniatureEvenement(f, e.DataID, 2)
	}
	if option&0x20 != 0 {
		// Nintendo envoie ici une petite icone propre a l'evenement ; nous n'en avons
		// pas, la vignette du niveau entier en tient lieu.
		ecrireMiniatureEvenement(f, e.DataID, 3)
	}

	// Revision 1.
	f.DateTime(dateTimeUnix(e.Fin))
	f.U8(0)
	f.U32(0)
	f.U16(0)
	f.U16(0)
	r := ninji.resultat(e.DataID, pid)
	if r != nil && r.TempsMs > 0 {
		f.U32(r.TempsMs) // meilleur temps du joueur
	} else {
		f.U32(0xFFFFFFFF)
	}
	f.U32(0xFFFFFFFF)
	f.U32(0xFFFFFFFF) // « temps requis pour la medaille » : FFFFFFFF dans toutes les mesures
	if r != nil {
		ecrireReferenceFantome(f, r.Fantome, r.Taille)
	} else {
		ecrireReferenceFantome(f, 0, 0)
	}
	encadrer(out, 1, f)
}

// 86 SearchCoursesEvent : tous les evenements, du plus recent au plus ancien.
func smm2SearchCoursesEvent(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	in := nex.NewStreamIn(req.Body, s)
	_ = in.U8()
	option := in.Substream().U32()

	out := nex.NewStreamOut(s)
	l := ninji.liste(time.Now().Unix())
	fiches := nex.NewStreamOut(s)
	n := 0
	for _, e := range l {
		if c := courses.get(e.DataID); c != nil {
			ecrireEventCourseInfo(fiches, e, c, conn.PID, option)
			n++
		}
	}
	out.U32(uint32(n))
	out.Write(fiches.Bytes())
	fmt.Printf("[SMM2 Ninji] search_courses_event(86) pid=%d option=0x%x -> %d evenement(s)\n", conn.PID, option, n)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}

// 85 GetCoursesEvent : des evenements precis. GetCoursesParam (liste d'identifiants +
// option), puis une structure vide. Fiches trouvees, puis un resultat PAR identifiant :
// 0x00690001 trouve, comme chez Nintendo ; NotFound sinon.
func smm2GetCoursesEvent(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	in := nex.NewStreamIn(req.Body, s)
	_ = in.U8()
	p := in.Substream()
	ids := nex.ReadList(p, func(i *nex.StreamIn) uint64 { return i.U64() })
	option := p.U32()
	if err := p.Err(); err != nil {
		fmt.Printf("[SMM2 Ninji] get_courses_event(85) : parametre illisible (%v) brut=%x\n", err, req.Body)
		return nex.NewRMCError(s, 0x73, req.CallID, 0x00690002)
	}

	fiches := nex.NewStreamOut(s)
	n := 0
	codes := make([]uint32, 0, len(ids))
	for _, id := range ids {
		e, ok := ninji.evenement(id)
		c := courses.get(id)
		if !ok || c == nil {
			codes = append(codes, 0x00690004)
			continue
		}
		ecrireEventCourseInfo(fiches, e, c, conn.PID, option)
		n++
		codes = append(codes, 0x00690001)
	}
	out := nex.NewStreamOut(s)
	out.U32(uint32(n))
	out.Write(fiches.Bytes())
	out.U32(uint32(len(codes)))
	for _, c := range codes {
		out.Result(c)
	}
	fmt.Printf("[SMM2 Ninji] get_courses_event(85) pid=%d %d demande(s) -> %d fiche(s)\n", conn.PID, len(ids), n)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}

// 154 GetEventCourseStatus : l'evenement courant. Mesure : son data_id, faux, et la date
// du 1er janvier 1970.
func smm2GetEventCourseStatus(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	var courant uint64
	if l := ninji.liste(time.Now().Unix()); len(l) > 0 {
		courant = l[0].DataID
	}
	corps := nex.NewStreamOut(s)
	corps.U64(courant)
	corps.Bool(false)
	corps.U64(dateTimeEpoque)
	fmt.Printf("[SMM2 Ninji] get_event_course_status(154) pid=%d -> %d\n", conn.PID, courant)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, frameStruct(s, 0, corps.Bytes()))
}

// 153 GetEventCourseStamp : le nombre de tampons. On compte les evenements ou le joueur a
// un temps : Nintendo rendait 1 au joueur qui en avait termine un seul.
func smm2GetEventCourseStamp(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	var n uint32
	ninji.mu.RLock()
	for _, parPID := range ninji.resultats {
		if r := parPID[conn.PID]; r != nil && r.TempsMs > 0 {
			n++
		}
	}
	ninji.mu.RUnlock()
	out := nex.NewStreamOut(s)
	out.U32(n)
	fmt.Printf("[SMM2 Ninji] get_event_course_stamp(153) pid=%d -> %d\n", conn.PID, n)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}

// centile rend le temps au centile p (0-100) d'une liste triee.
func centile(l []tempsJoueur, p int) uint32 {
	if len(l) == 0 {
		return 0
	}
	i := (len(l)*p + 99) / 100
	if i < 1 {
		i = 1
	}
	if i > len(l) {
		i = len(l)
	}
	return l[i-1].TempsMs
}

// 156 GetEventCourseHistogram : la repartition des temps et les seuils de medaille.
func smm2GetEventCourseHistogram(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	in := nex.NewStreamIn(req.Body, s)
	_ = in.U8()
	dataID := in.Substream().U64()

	tous := ninji.temps(dataID)
	cases := make([]uint32, (histoFinMs-histoDebutMs)/histoPasMs)
	for _, t := range tous {
		i := (int(t.TempsMs) - histoDebutMs) / histoPasMs
		if i < 0 {
			i = 0
		}
		if i >= len(cases) {
			i = len(cases) - 1
		}
		cases[i]++
	}

	corps := nex.NewStreamOut(s)
	corps.U64(dataID)
	corps.U32(histoDebutMs)
	corps.U32(histoFinMs)
	corps.U32(histoPasMs)
	corps.U32(uint32(len(cases)))
	for _, c := range cases {
		corps.U32(c)
	}
	// Les seuils, dans l'ordre 10, 30, 50 comme chez Nintendo. Sans aucun temps on les
	// omet : un seuil a zero ferait de chaque arrivee une medaille d'or.
	if len(tous) == 0 {
		corps.U32(0)
	} else {
		corps.U32(3)
		for _, p := range []uint8{10, 30, 50} {
			corps.U8(p)
			corps.U32(centile(tous, int(p)))
		}
	}
	corps.U32(0)
	fmt.Printf("[SMM2 Ninji] get_event_course_histogram(156) pid=%d evenement=%d -> %d temps\n", conn.PID, dataID, len(tous))
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, frameStruct(s, 0, corps.Bytes()))
}

// ecrireFantome : EventCourseGhostInfo — la reference du fichier, le temps, le joueur.
func ecrireFantome(out *nex.StreamOut, t tempsJoueur) {
	s := out.Settings
	f := nex.NewStreamOut(s)
	ecrireReferenceFantome(f, t.Fantome, t.Taille)
	f.U32(t.TempsMs)
	f.PID(t.PID)
	encadrer(out, 0, f)
}

// 157 GetEventCourseGhost : jusqu'a N fantomes, les plus proches du temps donne. Chez
// Nintendo le fantome du joueur lui-meme peut y figurer.
func smm2GetEventCourseGhost(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	in := nex.NewStreamIn(req.Body, s)
	_ = in.U8()
	p := in.Substream()
	dataID := p.U64()
	approx := p.U32()
	combien := int(p.U8())

	var avec []tempsJoueur
	for _, t := range ninji.temps(dataID) {
		if t.Fantome != 0 {
			avec = append(avec, t)
		}
	}
	ecart := func(t tempsJoueur) int64 {
		d := int64(t.TempsMs) - int64(approx)
		if d < 0 {
			return -d
		}
		return d
	}
	sort.SliceStable(avec, func(a, b int) bool { return ecart(avec[a]) < ecart(avec[b]) })
	if len(avec) > combien {
		avec = avec[:combien]
	}

	out := nex.NewStreamOut(s)
	out.U32(uint32(len(avec)))
	for _, t := range avec {
		ecrireFantome(out, t)
	}
	fmt.Printf("[SMM2 Ninji] get_event_course_ghost(157) pid=%d evenement=%d autour de %d ms -> %d fantome(s)\n",
		conn.PID, dataID, approx, len(avec))
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}

// 169 GetEventCourseFriendGhost : les fantomes des amis nommes dans le parametre.
// Parametre mesure : data_id, liste de PID, puis deux octets (0x1e, 0) inexpliques.
func smm2GetEventCourseFriendGhost(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	in := nex.NewStreamIn(req.Body, s)
	_ = in.U8()
	p := in.Substream()
	dataID := p.U64()
	amis := nex.ReadList(p, func(i *nex.StreamIn) uint64 { return i.PID() })

	voulus := map[uint64]bool{}
	for _, a := range amis {
		voulus[a] = true
	}
	var l []tempsJoueur
	for _, t := range ninji.temps(dataID) {
		if voulus[t.PID] && t.Fantome != 0 {
			l = append(l, t)
		}
	}
	out := nex.NewStreamOut(s)
	out.U32(uint32(len(l)))
	for _, t := range l {
		ecrireFantome(out, t)
	}
	fmt.Printf("[SMM2 Ninji] get_event_course_friend_ghost(169) pid=%d evenement=%d %d ami(s) -> %d fantome(s)\n",
		conn.PID, dataID, len(amis), len(l))
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}

// 102 PostPlayResultEventCourse : le joueur a fini l'evenement.
//
// Parametre mesure (c220) : une structure de revision 1 qui commence par le data_id,
// un Uint32 (2), puis le TEMPS en millisecondes (56359 ; le meme nombre revient ensuite
// comme meilleur temps dans la 85) ; puis une seconde structure dont la premiere chaine
// est la clef du fantome, celle que la 132 a rendue. Reponse vide.
func smm2PostPlayResultEventCourse(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	in := nex.NewStreamIn(req.Body, s)
	_ = in.U8()
	p := in.Substream()
	dataID := p.U64()
	_ = p.U32()
	temps := p.U32()
	_ = in.U8()
	clef := in.Substream().String()
	if err := in.Err(); err != nil || p.Err() != nil || temps == 0 {
		fmt.Printf("[SMM2 Ninji] post_result_event(102) pid=%d : parametre illisible (%v) brut=%x\n", conn.PID, err, req.Body)
		return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, nil)
	}

	var fantome uint64
	var taille uint32
	if _, err := fmt.Sscanf(clef, "%d", &fantome); err == nil {
		if m := courses.get(fantome); m != nil && m.OwnerPID == conn.PID {
			taille = m.Size
			if st, err := os.Stat(blobPath(fantome)); err == nil {
				taille = uint32(st.Size())
			}
			courses.complete(fantome, true)
		} else {
			fantome = 0
		}
	}
	meilleur := ninji.enregistrer(dataID, conn.PID, temps, fantome, taille)
	fmt.Printf("[SMM2 Ninji] post_result_event(102) pid=%d evenement=%d temps=%d ms fantome=%d meilleur=%v\n",
		conn.PID, dataID, temps, fantome, meilleur)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, nil)
}
