package main

import (
	"testing"
	"time"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

func TestPopularRankingAndCourseStats(t *testing.T) {
	defer isolerNotes(t)()
	s := nex.NewSwitchSettings(accessKey, nexVersion)

	// Creer 3 niveaux :
	// Niv 100 : Cree il y a 3 jours, 10 coeurs, 50 parties
	// Niv 101 : Cree il y a 1 jour, 2 coeurs, 10 parties
	// Niv 102 : Cree aujourd'hui, 0 coeur, 1 partie
	now := time.Now().Unix()
	courses.byID[100] = &courseMeta{DataID: 100, OwnerPID: 1, Name: "Niveau Populaire", Ready: true, CreatedAt: now - 3*86400, EssaisAuteur: 5, TempsAuteur: 30000}
	courses.byID[101] = &courseMeta{DataID: 101, OwnerPID: 2, Name: "Niveau Moyen", Ready: true, CreatedAt: now - 86400, EssaisAuteur: 2, TempsAuteur: 15000}
	courses.byID[102] = &courseMeta{DataID: 102, OwnerPID: 3, Name: "Niveau Nouveau", Ready: true, CreatedAt: now, EssaisAuteur: 1, TempsAuteur: 10000}

	// Ajouter des notes
	for i := uint64(10); i < 20; i++ {
		notes.noter(100, i, noteAime)
		notes.avancer(100, i, 3) // joue et reussi
	}
	notes.noter(101, 11, noteAime)
	notes.noter(101, 12, noteAime)
	notes.avancer(101, 11, 3)
	notes.avancer(101, 12, 3)

	resultats.parNiv[100] = &resultatNiveau{Parties: 50, Tentatives: 120, Reussites: 40}
	resultats.parNiv[101] = &resultatNiveau{Parties: 10, Tentatives: 25, Reussites: 8}
	resultats.parNiv[102] = &resultatNiveau{Parties: 1, Tentatives: 2, Reussites: 1}

	// 1. Verifier que niveauxPopulaires classe Niv 100 en premier (plus de coeurs/parties),
	// alors que par date de creation le Niv 102 est le plus recent.
	pop := niveauxPopulaires()
	if len(pop) < 3 {
		t.Fatalf("attendu au moins 3 niveaux populaires, obtenu %d", len(pop))
	}
	if pop[0].DataID != 100 || pop[1].DataID != 101 || pop[2].DataID != 102 {
		t.Fatalf("classement populaire incorrect: [0]=%d [1]=%d [2]=%d", pop[0].DataID, pop[1].DataID, pop[2].DataID)
	}

	// 2. Verifier CourseInfo pour le niveau 100 :
	// - Cle 4 de play_stats doit valoir 10 (empreintes / joueurs uniques) et NON 0 !
	// - Cle 0 de ratings doit valoir 10 (coeurs) et NON 0 !
	out := nex.NewStreamOut(s)
	ecrireCourseInfo(out, courses.byID[100], 0x1ff, 1)

	in := nex.NewStreamIn(out.Bytes(), s)
	_ = in.U8()
	p := in.Substream()
	_ = p.U64() // dataID
	_ = p.String() // code
	_ = p.PID()
	_ = p.String()
	_ = p.String()
	_ = p.U8() // style
	_ = p.U8() // theme
	_ = p.DateTime()
	for i := 0; i < 4; i++ {
		p.U8()
	}
	_ = p.U32() // condition
	_ = p.U16() // cond ampleur
	_ = p.U16()
	_ = p.QBuffer() // meta 48 octets

	// Table 0 : play_stats
	cnt0 := p.U32()
	var key4Val uint32
	var hasKey4 bool
	for i := uint32(0); i < cnt0; i++ {
		k := p.U8()
		v := p.U32()
		if k == 4 {
			hasKey4 = true
			key4Val = v
		}
	}
	if !hasKey4 || key4Val != 10 {
		t.Fatalf("cle 4 (empreintes/joueurs) = %d (present=%v), attendu 10 joueurs uniques", key4Val, hasKey4)
	}

	// Table 1 : ratings
	cnt1 := p.U32()
	var key0Likes uint32
	var hasKey0 bool
	for i := uint32(0); i < cnt1; i++ {
		k := p.U8()
		v := p.U32()
		if k == 0 {
			hasKey0 = true
			key0Likes = v
		}
	}
	if !hasKey0 || key0Likes != 10 {
		t.Fatalf("cle 0 de ratings (coeurs) = %d (present=%v), attendu 10 coeurs", key0Likes, hasKey0)
	}

	// 3. Verifier la requete SearchCoursesTermsRanking (Method 83)
	param83 := nex.NewStreamOut(s)
	param83.U32(0x1ff) // options
	rr := nex.NewStreamOut(s)
	rr.U32(0)  // depart
	rr.U32(10) // nombre
	param83.Write(frameStruct(s, 0, rr.Bytes()))
	param83.U8(0) // terms / filtre hebdomadaire

	r83 := smm2SearchCoursesRanking(&nex.Connection{Settings: s, PID: 1}, &nex.RMCMessage{
		Settings: s, Mode: nex.RMCRequest, Protocol: 0x73, Method: 83, CallID: 1,
		Body: frameStruct(s, 0, param83.Bytes()),
	})
	if r83.IsError {
		t.Fatalf("83 a echoue: %+v", r83)
	}

	in83 := nex.NewStreamIn(r83.Body, s)
	numCourses := in83.U32()
	if numCourses != 3 {
		t.Fatalf("attendu 3 cours dans la 83, obtenu %d", numCourses)
	}
}
