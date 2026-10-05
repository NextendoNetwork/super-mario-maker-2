package main

// Les onglets du profil d'un createur, et les abonnements.
//
// Mesure chez Nintendo le 2026-10-05 (capturas-smm2, session smm2c), en ouvrant le profil
// d'un createur puis en le suivant :
//
//	75 SearchCoursesPositiveRatedBy  { options, nombre, joueur }        -> List<CourseInfo>
//	80 SearchCoursesFirstClear       { joueur, options, ResultRange }   -> List<CourseInfo>, Bool
//	81 SearchCoursesBestTime         { joueur, options, ResultRange }   -> List<CourseInfo>, Bool
//	82 SearchCoursesFolloweePostedBy { options, ResultRange }           -> List<CourseInfo>, Bool
//	123 FollowUser / 124 UnfollowUser   Uint64 NU (le joueur)           -> rien
//	56 SearchUsersFollowee           { options, ResultRange }           -> List<UserInfo>, Bool
//
// 75, 80 et 81 rendaient des listes vides, 82 aussi, et 123/124 tombaient sur le repli :
// suivre quelqu'un ne servait a rien. Le joueur arrive sous son identifiant NSA ; on le
// traduit comme pour la 74 (smm2_identifiants.go).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

type magasinSuivis struct {
	mu     sync.RWMutex
	Suit   map[uint64]map[uint64]bool `json:"suit"` // abonne -> createurs suivis
	chemin string
}

var suivis = &magasinSuivis{Suit: map[uint64]map[uint64]bool{}}

func (m *magasinSuivis) charger(dir string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.chemin = filepath.Join(dir, "smm2_suivis.json")
	if b, err := os.ReadFile(m.chemin); err == nil {
		var c magasinSuivis
		if json.Unmarshal(b, &c) == nil && c.Suit != nil {
			m.Suit = c.Suit
		}
	}
	fmt.Printf("[SMM2 Suivis] %d joueur(s) abonne(s)\n", len(m.Suit))
}

func (m *magasinSuivis) ecrireLocked() {
	if m.chemin == "" {
		return
	}
	if b, err := json.Marshal(m); err == nil && os.WriteFile(m.chemin+".tmp", b, 0o644) == nil {
		os.Rename(m.chemin+".tmp", m.chemin)
	}
}

func (m *magasinSuivis) suivre(abonne, createur uint64, oui bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Suit[abonne] == nil {
		m.Suit[abonne] = map[uint64]bool{}
	}
	if oui {
		m.Suit[abonne][createur] = true
	} else {
		delete(m.Suit[abonne], createur)
	}
	m.ecrireLocked()
}

func (m *magasinSuivis) createurs(abonne uint64) []uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var l []uint64
	for pid := range m.Suit[abonne] {
		l = append(l, pid)
	}
	sort.Slice(l, func(a, b int) bool { return l[a] < l[b] })
	return l
}

// niveauxOu : les niveaux publics qui satisfont garder, du plus recent au plus ancien.
func niveauxOu(garder func(m *courseMeta) bool) []*courseMeta {
	var l []*courseMeta
	for _, m := range niveauxPublics() {
		if garder(m) {
			l = append(l, m)
		}
	}
	return l
}

func ecrireNiveaux(conn *nex.Connection, out *nex.StreamOut, l []*courseMeta, options uint32) {
	out.U32(uint32(len(l)))
	for _, m := range l {
		ecrireCourseInfo(out, m, options, conn.PID)
	}
}

// 75 SearchCoursesPositiveRatedBy : les niveaux que le joueur a aimes.
func smm2SearchCoursesPositiveRatedBy(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	in := nex.NewStreamIn(req.Body, s)
	_ = in.U8()
	p := in.Substream()
	options := p.U32()
	nombre := p.U32()
	joueur := pidJoueur(p.PID())
	l := niveauxOu(func(m *courseMeta) bool {
		notes.mu.RLock()
		defer notes.mu.RUnlock()
		n, ok := notes.Notes[m.DataID][joueur]
		return ok && n == noteAime
	})
	l = trancher(l, 0, nombre)
	out := nex.NewStreamOut(s)
	ecrireNiveaux(conn, out, l, options)
	fmt.Printf("[SMM2 Profil] positive_rated_by(75) pid=%d joueur=%d -> %d niveau(x)\n", conn.PID, joueur, len(l))
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}

// lireJoueurOptionsPlage : { joueur, options, ResultRange } — la forme des 80 et 81.
func lireJoueurOptionsPlage(s *nex.Settings, corps []byte) (uint64, uint32, uint32, uint32) {
	in := nex.NewStreamIn(corps, s)
	_ = in.U8()
	p := in.Substream()
	joueur := pidJoueur(p.PID())
	options := p.U32()
	depart, nombre := lirePlage(p)
	return joueur, options, depart, nombre
}

func repondreNiveauxEtSuite(conn *nex.Connection, req *nex.RMCMessage, nom string, l []*courseMeta, options, depart, nombre uint32) *nex.RMCMessage {
	s := conn.Settings
	total := len(l)
	l = trancher(l, depart, nombre)
	out := nex.NewStreamOut(s)
	ecrireNiveaux(conn, out, l, options)
	out.Bool(int(depart)+len(l) < total)
	fmt.Printf("[SMM2 Profil] %s pid=%d -> %d/%d niveau(x)\n", nom, conn.PID, len(l), total)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}

// 80 SearchCoursesFirstClear : les niveaux dont le joueur est le premier finisseur.
func smm2SearchCoursesFirstClear(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	joueur, options, depart, nombre := lireJoueurOptionsPlage(conn.Settings, req.Body)
	l := niveauxOu(func(m *courseMeta) bool { return resultats.lire(m.DataID).PremierPID == joueur })
	return repondreNiveauxEtSuite(conn, req, fmt.Sprintf("first_clear(80) joueur=%d", joueur), l, options, depart, nombre)
}

// 81 SearchCoursesBestTime : les niveaux dont le joueur detient le record.
func smm2SearchCoursesBestTime(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	joueur, options, depart, nombre := lireJoueurOptionsPlage(conn.Settings, req.Body)
	l := niveauxOu(func(m *courseMeta) bool { return resultats.lire(m.DataID).RecordPID == joueur })
	return repondreNiveauxEtSuite(conn, req, fmt.Sprintf("best_time(81) joueur=%d", joueur), l, options, depart, nombre)
}

// 82 SearchCoursesFolloweePostedBy : les niveaux des createurs suivis.
func smm2SearchCoursesFolloweePostedBy(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	in := nex.NewStreamIn(req.Body, conn.Settings)
	_ = in.U8()
	p := in.Substream()
	options := p.U32()
	depart, nombre := lirePlage(p)
	suit := map[uint64]bool{}
	for _, c := range suivis.createurs(conn.PID) {
		suit[c] = true
	}
	l := niveauxOu(func(m *courseMeta) bool { return suit[m.OwnerPID] })
	return repondreNiveauxEtSuite(conn, req, "followee_posted_by(82)", l, options, depart, nombre)
}

// 56 SearchUsersFollowee : les createurs suivis.
func smm2SearchUsersFollowee(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	in := nex.NewStreamIn(req.Body, s)
	_ = in.U8()
	p := in.Substream()
	_ = p.U32()
	depart, nombre := lirePlage(p)
	var tous []uint64
	for _, c := range suivis.createurs(conn.PID) {
		if _, ok := profilDe(c); ok {
			tous = append(tous, c)
		}
	}
	l := tous
	if int(depart) >= len(l) {
		l = nil
	} else {
		l = l[depart:]
		if nombre > 0 && int(nombre) < len(l) {
			l = l[:nombre]
		}
	}
	out := nex.NewStreamOut(s)
	out.U32(uint32(len(l)))
	for _, c := range l {
		prof, _ := profilDe(c)
		nex.EcrireUserInfo(out, prof)
	}
	out.Bool(int(depart)+len(l) < len(tous))
	fmt.Printf("[SMM2 Profil] users_followee(56) pid=%d -> %d createur(s)\n", conn.PID, len(l))
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}

// 123 FollowUser et 124 UnfollowUser : un Uint64 nu, le createur. Reponse vide.
func smm2FollowUser(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	createur := pidJoueur(nex.NewStreamIn(req.Body, s).PID())
	oui := req.Method == 123
	if createur != 0 && createur != conn.PID {
		suivis.suivre(conn.PID, createur, oui)
	}
	fmt.Printf("[SMM2 Profil] follow(%d) pid=%d createur=%d suivre=%v\n", req.Method, conn.PID, createur, oui)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, nil)
}
