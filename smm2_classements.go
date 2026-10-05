package main

// Les CLASSEMENTS de joueurs (Leaderboards) : methodes 50, 51, 57 et 58.
//
// Toutes rendent la meme chose (documentation kinnay) : List<UserInfo>, List<Uint32>
// (les rangs), Bool. Avant ce fichier, 57 rendait deux listes vides, 50 et 51 tombaient
// sur le repli generique, et 58 — SearchUsersTermsRanking, un classement de JOUEURS —
// etait servie comme un classement de NIVEAUX : le jeu recevait des CourseInfo la ou il
// attend des UserInfo.
//
// Les scores sont ceux que Nextendo connait vraiment :
//
//	50 points de createur  coeurs recus sur ses niveaux publies
//	51 mode sans fin       record de niveaux enchaines, dans la difficulte demandee
//	57 reussites           niveaux termines
//	58 sur la periode      points de createur (pas d'historique par semaine : deduit)
//
// Un joueur sans score ou sans profil n'apparait pas : le fabriquer afficherait un nom
// vide ou un zero qui n'est pas un classement.

import (
	"fmt"
	"sort"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

// profilDe : remplacable dans les tests, qui ne peuvent pas peupler les profils de la
// bibliotheque.
var profilDe = nex.SMM2ProfilDe

type entreeClassement struct {
	pid   uint64
	score uint32
}

// candidatsClassement : tous les joueurs dont Nextendo sait quelque chose.
func candidatsClassement() map[uint64]bool {
	pids := map[uint64]bool{}
	resultats.mu.RLock()
	for pid := range resultats.parJoueur {
		pids[pid] = true
	}
	resultats.mu.RUnlock()
	endless.mu.RLock()
	for pid := range endless.parPID {
		pids[pid] = true
	}
	endless.mu.RUnlock()
	for _, m := range niveauxPublics() {
		pids[m.OwnerPID] = true
	}
	return pids
}

// pointsCreateur : les coeurs recus sur les niveaux publies de chaque joueur.
func pointsCreateur() map[uint64]uint32 {
	pts := map[uint64]uint32{}
	for _, m := range niveauxPublics() {
		aime, _, _ := notes.compte(m.DataID)
		pts[m.OwnerPID] += aime
	}
	return pts
}

// classer trie par score decroissant (PID croissant a egalite, pour un ordre stable),
// ecarte les scores nuls et les joueurs sans profil, puis applique l'etendue demandee.
// Les rangs sont ceux d'une competition : deux egaux partagent le rang, le suivant saute.
func classer(score func(pid uint64) uint32, depart, nombre uint32) ([]entreeClassement, []uint32) {
	var l []entreeClassement
	for pid := range candidatsClassement() {
		v := score(pid)
		if v == 0 {
			continue
		}
		if _, ok := profilDe(pid); !ok {
			continue
		}
		l = append(l, entreeClassement{pid, v})
	}
	sort.Slice(l, func(a, b int) bool {
		if l[a].score != l[b].score {
			return l[a].score > l[b].score
		}
		return l[a].pid < l[b].pid
	})
	rangs := make([]uint32, len(l))
	for i := range l {
		if i > 0 && l[i].score == l[i-1].score {
			rangs[i] = rangs[i-1]
		} else {
			rangs[i] = uint32(i + 1)
		}
	}
	if int(depart) >= len(l) {
		return nil, nil
	}
	fin := len(l)
	if nombre > 0 && int(depart)+int(nombre) < fin {
		fin = int(depart) + int(nombre)
	}
	return l[depart:fin], rangs[depart:fin]
}

// lirePlage lit un ResultRange encadre : { Uint32 depart, Uint32 nombre }.
func lirePlage(p *nex.StreamIn) (uint32, uint32) {
	_ = p.U8()
	rr := p.Substream()
	return rr.U32(), rr.U32()
}

func repondreClassement(conn *nex.Connection, req *nex.RMCMessage, nom string, l []entreeClassement, rangs []uint32) *nex.RMCMessage {
	s := conn.Settings
	out := nex.NewStreamOut(s)
	out.U32(uint32(len(l)))
	for _, e := range l {
		p, _ := profilDe(e.pid)
		nex.EcrireUserInfo(out, p)
	}
	out.U32(uint32(len(rangs)))
	for _, r := range rangs {
		out.U32(r)
	}
	out.Bool(false)
	fmt.Printf("[SMM2 Classements] %s pid=%d -> %d joueur(s)\n", nom, conn.PID, len(l))
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}

// 50 SearchUsersUserPoint : { Uint32 option, Buffer, ResultRange }.
func smm2SearchUsersUserPoint(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	in := nex.NewStreamIn(req.Body, conn.Settings)
	_ = in.U8()
	p := in.Substream()
	_ = p.U32()
	_ = p.Buffer()
	depart, nombre := lirePlage(p)
	pts := pointsCreateur()
	l, r := classer(func(pid uint64) uint32 { return pts[pid] }, depart, nombre)
	return repondreClassement(conn, req, "search_users_user_point(50)", l, r)
}

// 51 SearchUsersEndlessMode : { Uint8 difficulte, Uint32 option, Buffer, ResultRange }.
func smm2SearchUsersEndlessMode(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	in := nex.NewStreamIn(req.Body, conn.Settings)
	_ = in.U8()
	p := in.Substream()
	difficulte := p.U8()
	_ = p.U32()
	_ = p.Buffer()
	depart, nombre := lirePlage(p)
	l, r := classer(func(pid uint64) uint32 { return endless.recordsDe(pid)[difficulte%4] }, depart, nombre)
	return repondreClassement(conn, req, fmt.Sprintf("search_users_endless_mode(51) difficulte=%d", difficulte), l, r)
}

// 57 SearchUsersClearRanking : { Uint8, Uint32 option, Buffer, ResultRange }. Le Uint8
// n'est pas documente ; il est journalise pour l'identifier.
func smm2SearchUsersClearRanking(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	in := nex.NewStreamIn(req.Body, conn.Settings)
	_ = in.U8()
	p := in.Substream()
	categorie := p.U8()
	_ = p.U32()
	_ = p.Buffer()
	depart, nombre := lirePlage(p)
	l, r := classer(func(pid uint64) uint32 { return resultats.statsDe(pid).Reussites }, depart, nombre)
	return repondreClassement(conn, req, fmt.Sprintf("search_users_clear_ranking(57) u8=%d", categorie), l, r)
}

// 58 SearchUsersTermsRanking : { Uint32 option, ResultRange, Buffer }.
func smm2SearchUsersTermsRanking(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	in := nex.NewStreamIn(req.Body, conn.Settings)
	_ = in.U8()
	p := in.Substream()
	_ = p.U32()
	depart, nombre := lirePlage(p)
	pts := pointsCreateur()
	l, r := classer(func(pid uint64) uint32 { return pts[pid] }, depart, nombre)
	return repondreClassement(conn, req, "search_users_terms_ranking(58)", l, r)
}
