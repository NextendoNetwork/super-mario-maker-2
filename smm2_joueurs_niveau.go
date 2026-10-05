package main

// Qui a JOUE, REUSSI ou AIME un niveau : methodes 53, 54 et 55.
//
// Parametre (documentation kinnay) : { Uint64 dataId, Uint32 options, Uint32 nombre }.
// Reponse : List<UserInfo>, rien d'autre. Elles rendaient des listes vides : on ne
// savait pas qui avait joue quoi. Depuis le suivi par joueur (smm2_notes.go, Progres) et
// les notes, on le sait.

import (
	"fmt"
	"sort"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

const (
	listeJoue = iota
	listeReussi
	listeAime
)

// joueursDuNiveau rend les joueurs d'un niveau selon le critere, PID croissant.
func joueursDuNiveau(dataID uint64, critere int) []uint64 {
	vus := map[uint64]bool{}
	notes.mu.RLock()
	for pid, etat := range notes.Progres[dataID] {
		switch critere {
		case listeJoue:
			vus[pid] = etat >= 2
		case listeReussi:
			if etat == 3 {
				vus[pid] = true
			}
		}
	}
	for pid, n := range notes.Notes[dataID] {
		switch critere {
		case listeJoue:
			vus[pid] = true
		case listeReussi:
			if n == noteAucune { // la note « aucune » n'arrive qu'a la reussite
				vus[pid] = true
			}
		case listeAime:
			if n == noteAime {
				vus[pid] = true
			}
		}
	}
	notes.mu.RUnlock()
	if critere == listeReussi || critere == listeJoue {
		r := resultats.lire(dataID)
		for _, pid := range []uint64{r.PremierPID, r.RecordPID} {
			if pid != 0 {
				vus[pid] = true
			}
		}
	}
	var l []uint64
	for pid, ok := range vus {
		if ok {
			l = append(l, pid)
		}
	}
	sort.Slice(l, func(a, b int) bool { return l[a] < l[b] })
	return l
}

func repondreJoueursDuNiveau(conn *nex.Connection, req *nex.RMCMessage, critere int, nom string) *nex.RMCMessage {
	s := conn.Settings
	in := nex.NewStreamIn(req.Body, s)
	_ = in.U8()
	p := in.Substream()
	dataID := p.U64()
	_ = p.U32()
	nombre := p.U32()

	out := nex.NewStreamOut(s)
	corps := nex.NewStreamOut(s)
	n := 0
	for _, pid := range joueursDuNiveau(dataID, critere) {
		if nombre > 0 && uint32(n) >= nombre {
			break
		}
		prof, ok := profilDe(pid)
		if !ok {
			continue
		}
		nex.EcrireUserInfo(corps, prof)
		n++
	}
	out.U32(uint32(n))
	out.Write(corps.Bytes())
	fmt.Printf("[SMM2 Joueurs] %s pid=%d niveau=%d -> %d joueur(s)\n", nom, conn.PID, dataID, n)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}

// 53 SearchUsersPlayedCourse
func smm2SearchUsersPlayedCourse(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	return repondreJoueursDuNiveau(conn, req, listeJoue, "search_users_played_course(53)")
}

// 54 SearchUsersClearedCourse
func smm2SearchUsersClearedCourse(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	return repondreJoueursDuNiveau(conn, req, listeReussi, "search_users_cleared_course(54)")
}

// 55 SearchUsersPositiveRatedCourse
func smm2SearchUsersPositiveRatedCourse(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	return repondreJoueursDuNiveau(conn, req, listeAime, "search_users_positive_rated_course(55)")
}
