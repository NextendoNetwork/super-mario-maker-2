package main

import (
	"encoding/hex"
	"testing"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

func TestNiveauxEndlessFiltreAvantDeLimiter(t *testing.T) {
	m := melangerEndless
	defer func() { melangerEndless = m }()
	melangerEndless = func(int, func(int, int)) {} // ordre fixe pour ce test
	// L'ordre est deja celui de niveauxPublics : le niveau le plus recent est facile.
	liste := []*courseMeta{
		{DataID: 3, EnteteLue: true, EssaisAuteur: 1},
		{DataID: 2, EnteteLue: true, EssaisAuteur: 5},
		{DataID: 1, EnteteLue: true, EssaisAuteur: 6},
	}
	retenus := niveauxEndless(liste, diffNormal, 1)
	if len(retenus) != 1 || retenus[0].DataID != 2 {
		t.Fatalf("normal, limite 1 : got %+v; want data_id 2", retenus)
	}
	if got := niveauxEndless(liste, diffExpert, 1); len(got) != 0 {
		t.Fatalf("expert sans niveau : got %+v; want empty", got)
	}
	if got := niveauxEndless(liste, 255, 1); len(got) != 0 {
		t.Fatalf("difficulte invalide : got %+v; want empty", got)
	}
}

// TestNiveauxEndlessAleatoire : la reserve change d'une partie a l'autre, et ne contient
// que des niveaux de la difficulte demandee. Avant, chaque partie commencait par le meme
// niveau (signale le 2026-10-05).
func TestNiveauxEndlessAleatoire(t *testing.T) {
	var liste []*courseMeta
	for i := 0; i < 40; i++ {
		liste = append(liste, &courseMeta{DataID: uint64(100 + i), EnteteLue: true, EssaisAuteur: 5})
	}
	liste = append(liste, &courseMeta{DataID: 1, EnteteLue: true, EssaisAuteur: 1}) // facile
	premiers := map[uint64]bool{}
	for i := 0; i < 30; i++ {
		r := niveauxEndless(liste, diffNormal, 10)
		if len(r) != 10 {
			t.Fatalf("%d niveaux, attendu 10", len(r))
		}
		for _, m := range r {
			if m.DataID == 1 {
				t.Fatal("un niveau facile dans la reserve normale")
			}
		}
		premiers[r[0].DataID] = true
	}
	if len(premiers) < 5 {
		t.Fatalf("seulement %d premiers niveaux differents en 30 parties : pas aleatoire", len(premiers))
	}
}

// TestPickUpRespecteLeNombre : la 84 recoit {options, nombre, Uint8} — parametre mesure
// chez Nintendo, 0x1ff, 100, 4 — et ne rend jamais plus que le nombre demande. Nous
// lisions un ResultRange et rendions tout le catalogue (1043 niveaux, ~700 Ko).
func TestPickUpRespecteLeNombre(t *testing.T) {
	ancien := courses
	defer func() { courses = ancien }()
	courses = &courseStore{byID: map[uint64]*courseMeta{}}
	for i := uint64(0); i < 150; i++ {
		courses.byID[1000+i] = &courseMeta{DataID: 1000 + i, Name: "n", Ready: true}
	}
	t.Setenv("SMM2_COURSEINFO", "1")
	s := nex.NewSwitchSettings(accessKey, nexVersion)
	corps, _ := hex.DecodeString("0009000000ff0100006400000004")
	r := smm2SearchCoursesPickUp(&nex.Connection{Settings: s, PID: 1},
		&nex.RMCMessage{Settings: s, Mode: nex.RMCRequest, Protocol: 0x73, Method: 84, CallID: 1, Body: corps})
	if n := nex.NewStreamIn(r.Body, s).U32(); n != 100 {
		t.Fatalf("84 : %d niveaux, attendu 100", n)
	}
}
