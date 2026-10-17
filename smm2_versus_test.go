package main

import (
	"testing"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

func TestVersusMatchAndLeaderboard(t *testing.T) {
	defer isolerNotes(t)()
	s := nex.NewSwitchSettings(accessKey, nexVersion)

	// Creer 3 profils
	p1 := uint64(1001)
	p2 := uint64(1002)
	p3 := uint64(1003)

	profilDe = func(pid uint64) (nex.SMM2Profil, bool) {
		return nex.SMM2Profil{PID: pid, Nom: "Joueur", Pays: "US"}, true
	}

	// 1. Avant de jouer : joueur non classe
	classe, notes117 := valeursVersusForPID(p1)
	if classe || notes117[0].valeur != 0xFFFFFFFF || notes117[0].valide {
		t.Fatalf("joueur neuf ne doit pas etre classe: classe=%v notes=%+v", classe, notes117)
	}

	// 2. Simuler une fin de match via la Methode 119
	// Parametre EndBattleModeParam :
	// Map de resultats : p1 = 1er, p2 = 2e
	sub := nex.NewStreamOut(s)
	sub.U32(2) // 2 resultats
	sub.U64(p1)
	sub.U8(1) // 1er
	sub.U64(p2)
	sub.U8(2) // 2e
	sub.U32(3) // kills
	sub.U32(1) // deaths
	sub.U32(1540) // glickoRate
	sub.U32(330)  // glickoDev
	sub.U32(60000)// glickoVol
	sub.U32(42)   // gid

	reqBody := frameStruct(s, 0, sub.Bytes())

	conn1 := &nex.Connection{Settings: s, PID: p1}
	r119 := smm2DataStoreHandler()(conn1, &nex.RMCMessage{
		Settings: s, Mode: nex.RMCRequest, Protocol: 0x73, Method: 119, CallID: 1,
		Body: reqBody,
	})
	if r119.IsError {
		t.Fatalf("119 a echoue: %+v", r119)
	}

	// Verifier que p1 a gagne
	st1 := versus.statsDe(p1)
	if st1.Parties != 1 || st1.Victoires != 1 || st1.Streak != 1 || st1.Rating <= 1000 {
		t.Fatalf("stats p1 incorrectes: %+v", st1)
	}

	// Simuler la reponse pour p2 (perdant)
	conn2 := &nex.Connection{Settings: s, PID: p2}
	_ = smm2DataStoreHandler()(conn2, &nex.RMCMessage{
		Settings: s, Mode: nex.RMCRequest, Protocol: 0x73, Method: 119, CallID: 2,
		Body: reqBody,
	})
	st2 := versus.statsDe(p2)
	if st2.Parties != 1 || st2.Victoires != 0 || st2.Defaites != 1 || st2.Streak != 0 {
		t.Fatalf("stats p2 incorrectes: %+v", st2)
	}

	// 3. Apres le match : le joueur est desormais classe en 117
	classeApres, notesApres := valeursVersusForPID(p1)
	if !classeApres || notesApres[0].valeur != st1.Rating || !notesApres[0].valide {
		t.Fatalf("joueur doit etre classe apres son match: classe=%v notes=%+v", classeApres, notesApres)
	}

	// 4. Verifier la table multiplayerStats pour UserInfo
	mpStats := versus.multiplayerStats(p1)
	if mpStats[0] != st1.Rating || mpStats[1] != 2 || mpStats[2] != 1 || mpStats[3] != 1 {
		t.Fatalf("multiplayerStats incorrect: %+v (tier attendu 2 pour rating %d)", mpStats, st1.Rating)
	}

	// 5. Verifier le classement Versus (Methode 52)
	param52 := nex.NewStreamOut(s)
	param52.U32(0) // option
	param52.Buffer(nil)
	rr := nex.NewStreamOut(s)
	rr.U32(0)   // depart
	rr.U32(100) // nombre
	param52.Write(frameStruct(s, 0, rr.Bytes()))

	r52 := smm2SearchUsersBattleMode(&nex.Connection{Settings: s, PID: p3}, &nex.RMCMessage{
		Settings: s, Mode: nex.RMCRequest, Protocol: 0x73, Method: 52, CallID: 5,
		Body: frameStruct(s, 0, param52.Bytes()),
	})
	if r52.IsError {
		t.Fatalf("52 a echoue: %+v", r52)
	}

	in52 := nex.NewStreamIn(r52.Body, s)
	numUsers := in52.U32()
	if numUsers != 2 {
		t.Fatalf("52 : attendu 2 joueurs classes (p1 et p2), obtenu %d", numUsers)
	}
}

// TestCoopMatchTracking verifie le suivi des parties et victoires en mode cooperatif.
func TestCoopMatchTracking(t *testing.T) {
	defer isolerNotes(t)()
	s := nex.NewSwitchSettings(accessKey, nexVersion)

	p1 := uint64(2001)
	p2 := uint64(2002)

	// Simuler une fin de match Coop reussi (tous deux arrivent avec statut MultiClear = 3)
	sub := nex.NewStreamOut(s)
	sub.U32(2)
	sub.U64(p1)
	sub.U8(3) // MultiClear
	sub.U64(p2)
	sub.U8(3) // MultiClear
	sub.U32(0) // kills
	sub.U32(0) // deaths
	sub.U32(0)
	sub.U32(0)
	sub.U32(0)
	sub.U32(99) // gid

	conn1 := &nex.Connection{Settings: s, PID: p1}
	r119 := smm2DataStoreHandler()(conn1, &nex.RMCMessage{
		Settings: s, Mode: nex.RMCRequest, Protocol: 0x73, Method: 119, CallID: 10,
		Body: frameStruct(s, 0, sub.Bytes()),
	})
	if r119.IsError {
		t.Fatalf("119 coop a echoue: %+v", r119)
	}

	st1 := versus.statsDe(p1)
	if st1.PartiesCoop != 1 || st1.VictoiresCoop != 1 || st1.StreakCoop != 1 {
		t.Fatalf("stats coop p1 incorrectes: %+v", st1)
	}

	mpStats := versus.multiplayerStats(p1)
	if mpStats[10] != 1 || mpStats[11] != 1 || mpStats[12] != 1 {
		t.Fatalf("multiplayer_stats coop incorrect: %+v", mpStats)
	}
}
