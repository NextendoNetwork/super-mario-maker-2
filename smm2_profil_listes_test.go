package main

import (
	"testing"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

// TestOngletsDuProfilEtAbonnements : avec les formes de requete mesurees chez Nintendo
// (2026-10-05), 75 rend les niveaux aimes par le joueur, 80 ceux dont il est le premier
// finisseur, 81 ceux dont il a le record ; 123 abonne, 124 desabonne, et 82 rend les
// niveaux des createurs suivis.
func TestOngletsDuProfilEtAbonnements(t *testing.T) {
	defer isolerNotes(t)()
	r, sv, p := resultats, suivis, profilDe
	defer func() { resultats, suivis, profilDe = r, sv, p }()
	suivis = &magasinSuivis{Suit: map[uint64]map[uint64]bool{}}
	profilDe = func(pid uint64) (nex.SMM2Profil, bool) { return nex.SMM2Profil{PID: pid, Nom: "j"}, true }
	courses.byID[10] = &courseMeta{DataID: 10, Name: "a", OwnerPID: 50, Ready: true}
	courses.byID[11] = &courseMeta{DataID: 11, Name: "b", OwnerPID: 51, Ready: true}
	resultats = &magasinResultats{parNiv: map[uint64]*resultatNiveau{
		10: {PremierPID: 7}, 11: {RecordPID: 7},
	}, parJoueur: map[uint64]*statsJoueur{}}
	notes.noter(11, 7, noteAime)

	s := nex.NewSwitchSettings(accessKey, nexVersion)
	conn := &nex.Connection{Settings: s, PID: 7}
	appel := func(m uint32, corps []byte) *nex.RMCMessage {
		return &nex.RMCMessage{Settings: s, Mode: nex.RMCRequest, Protocol: 0x73, Method: m, CallID: 1, Body: corps}
	}
	plage := func(o *nex.StreamOut) {
		rr := nex.NewStreamOut(s)
		rr.U32(0)
		rr.U32(100)
		o.Write(frameStruct(s, 0, rr.Bytes()))
	}
	premier := func(rep *nex.RMCMessage) (uint32, uint64) {
		in := nex.NewStreamIn(rep.Body, s)
		n := in.U32()
		if n == 0 {
			return 0, 0
		}
		_ = in.U8()
		return n, in.Substream().U64()
	}

	// 75 : { options, nombre, joueur }
	o := nex.NewStreamOut(s)
	o.U32(0x1ff)
	o.U32(100)
	o.PID(7)
	if n, id := premier(smm2SearchCoursesPositiveRatedBy(conn, appel(75, frameStruct(s, 0, o.Bytes())))); n != 1 || id != 11 {
		t.Fatalf("75 : %d niveau(x), premier %d ; attendu le niveau aime 11", n, id)
	}
	// 80 et 81 : { joueur, options, ResultRange }
	jop := func() []byte {
		o := nex.NewStreamOut(s)
		o.PID(7)
		o.U32(0x1ff)
		plage(o)
		return frameStruct(s, 0, o.Bytes())
	}
	if n, id := premier(smm2SearchCoursesFirstClear(conn, appel(80, jop()))); n != 1 || id != 10 {
		t.Fatalf("80 : %d, %d ; attendu 10", n, id)
	}
	if n, id := premier(smm2SearchCoursesBestTime(conn, appel(81, jop()))); n != 1 || id != 11 {
		t.Fatalf("81 : %d, %d ; attendu 11", n, id)
	}
	// 123 suivre 51, puis 82 ; 124 ne plus suivre, puis 82 vide.
	pid := func(v uint64) []byte { o := nex.NewStreamOut(s); o.PID(v); return o.Bytes() }
	op := func() []byte {
		o := nex.NewStreamOut(s)
		o.U32(0x1ff)
		plage(o)
		return frameStruct(s, 0, o.Bytes())
	}
	smm2FollowUser(conn, appel(123, pid(51)))
	if n, id := premier(smm2SearchCoursesFolloweePostedBy(conn, appel(82, op()))); n != 1 || id != 11 {
		t.Fatalf("82 apres suivi : %d, %d ; attendu 11", n, id)
	}
	if n, _ := premier(smm2SearchUsersFollowee(conn, appel(56, op()))); n != 1 {
		t.Fatalf("56 : %d createur(s), attendu 1", n)
	}
	smm2FollowUser(conn, appel(124, pid(51)))
	if n, _ := premier(smm2SearchCoursesFolloweePostedBy(conn, appel(82, op()))); n != 0 {
		t.Fatalf("82 apres desabonnement : %d, attendu 0", n)
	}
}
