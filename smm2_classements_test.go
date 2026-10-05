package main

import (
	"testing"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

// TestClassementsDeJoueurs : 57 classe par reussites, 50 et 58 par coeurs recus, 51 par
// record du mode sans fin dans la difficulte demandee. Les joueurs sans score ou sans
// profil n'apparaissent pas ; deux egaux partagent leur rang ; l'etendue est appliquee.
// Et 58 rend des JOUEURS (UserInfo), plus des niveaux.
func TestClassementsDeJoueurs(t *testing.T) {
	defer isolerNotes(t)()
	r, e, p := resultats, endless, profilDe
	defer func() { resultats, endless, profilDe = r, e, p }()
	resultats = &magasinResultats{parNiv: map[uint64]*resultatNiveau{}, parJoueur: map[uint64]*statsJoueur{
		1: {Reussites: 5}, 2: {Reussites: 9}, 3: {Reussites: 5}, 4: {Reussites: 0}, 5: {Reussites: 7},
	}}
	endless = &magasinEndless{parPID: map[uint64]*[4]partieEndless{1: {{}, {}, {Record: 12}, {}}}}
	profilDe = func(pid uint64) (nex.SMM2Profil, bool) {
		if pid == 5 { // pas de profil : ecarte
			return nex.SMM2Profil{}, false
		}
		return nex.SMM2Profil{PID: pid, Nom: "j", Pays: "PE"}, true
	}
	courses.byID[500] = &courseMeta{DataID: 500, Name: "niveau", OwnerPID: 3, Ready: true}
	notes.noter(500, 1, noteAime)
	notes.noter(500, 2, noteAime)

	pids := func(l []entreeClassement) []uint64 {
		var o []uint64
		for _, x := range l {
			o = append(o, x.pid)
		}
		return o
	}
	l, rangs := classer(func(pid uint64) uint32 { return resultats.statsDe(pid).Reussites }, 0, 10)
	if got := pids(l); len(got) != 3 || got[0] != 2 || got[1] != 1 || got[2] != 3 {
		t.Fatalf("57 : ordre %v, attendu [2 1 3] (4 sans score, 5 sans profil)", got)
	}
	if rangs[0] != 1 || rangs[1] != 2 || rangs[2] != 2 {
		t.Fatalf("57 : rangs %v, attendu [1 2 2]", rangs)
	}
	if l, _ := classer(func(pid uint64) uint32 { return resultats.statsDe(pid).Reussites }, 1, 1); len(l) != 1 || l[0].pid != 1 {
		t.Fatalf("etendue 1+1 : %v", pids(l))
	}
	pts := pointsCreateur()
	if l, _ := classer(func(pid uint64) uint32 { return pts[pid] }, 0, 10); len(l) != 1 || l[0].pid != 3 || l[0].score != 2 {
		t.Fatalf("50/58 : %+v, attendu le joueur 3 avec 2 coeurs", l)
	}
	// Sans aucun coeur, les parties jouees sur ses niveaux comptent (les J'aime ne
	// fonctionnent pas encore : sans cela, « No makers found » pour tous).
	resultats.parNiv[500] = &resultatNiveau{Parties: 40}
	if pts := pointsCreateur(); pts[3] != 42 {
		t.Fatalf("points du createur 3 : %d, attendu 40 parties + 2 coeurs", pts[3])
	}
	if l, _ := classer(func(pid uint64) uint32 { return endless.recordsDe(pid)[2] }, 0, 10); len(l) != 1 || l[0].pid != 1 {
		t.Fatalf("51 expert : %v, attendu [1]", pids(l))
	}

	// 58 avec le parametre de la forme documentee rend des UserInfo, relus sans reste.
	s := nex.NewSwitchSettings(accessKey, nexVersion)
	pp := nex.NewStreamOut(s)
	pp.U32(0x6625)
	rr := nex.NewStreamOut(s)
	rr.U32(0)
	rr.U32(100)
	pp.Write(frameStruct(s, 0, rr.Bytes()))
	pp.Buffer(nil)
	rep := smm2SearchUsersTermsRanking(&nex.Connection{Settings: s, PID: 1},
		&nex.RMCMessage{Settings: s, Mode: nex.RMCRequest, Protocol: 0x73, Method: 58, CallID: 1, Body: frameStruct(s, 0, pp.Bytes())})
	in := nex.NewStreamIn(rep.Body, s)
	if n := in.U32(); n != 1 {
		t.Fatalf("58 : %d joueur(s), attendu 1", n)
	}
	_ = in.U8()
	if pid := in.Substream().PID(); pid != 3 {
		t.Fatalf("58 : premier UserInfo pid=%d, attendu 3", pid)
	}
	if n := in.U32(); n != 1 || in.U32() != 1 || in.Bool() || in.Remaining() != 0 || in.Err() != nil {
		t.Fatal("58 : rangs ou booleen final mal formes")
	}
}
