package main

import (
	"strings"
	"testing"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

// TestTagOuZero : les etiquettes rangees par la 69 (codes decimaux) doivent ressortir dans
// CourseInfo. Avant, tagOuZero rendait zero dans tous les cas et aucune etiquette ne
// s'affichait chez nous ; chez Nintendo, « SUPER RACE!!! » porte 3 et 14.
func TestTagOuZero(t *testing.T) {
	cas := []struct {
		tags []string
		n    int
		veut uint32
	}{
		{[]string{"3", "14"}, 0, 3},
		{[]string{"3", "14"}, 1, 14},
		{[]string{"15"}, 0, 15},
		{[]string{"16"}, 0, 0},       // hors de l'enumeration
		{[]string{"speedrun"}, 0, 0}, // pas un code
		{[]string{"3"}, 1, 0},        // absente
		{nil, 0, 0},
	}
	for _, c := range cas {
		if got := tagOuZero(c.tags, c.n); got != c.veut {
			t.Errorf("tagOuZero(%q, %d) = %d, attendu %d", c.tags, c.n, got, c.veut)
		}
	}
}

// TestMiniatureDeChoisitLaPrete : avec plusieurs entrees pour la meme vignette (une prete,
// des tentatives jamais terminees), on rend toujours la prete, et la plus recente des
// pretes. Mesure 2026-10-05 sur le niveau 6257 : 6262 prete, 6264 jamais terminee.
func TestMiniatureDeChoisitLaPrete(t *testing.T) {
	ancien := courses
	defer func() { courses = ancien }()
	courses = &courseStore{byID: map[uint64]*courseMeta{
		6262: {DataID: 6262, Name: "rel-6257-2", Ready: true, Size: 15673},
		6264: {DataID: 6264, Name: "rel-6257-2", Ready: false, Size: 15673},
		6260: {DataID: 6260, Name: "rel-6257-3", Ready: true, Size: 18998},
		6261: {DataID: 6261, Name: "rel-6257-3", Ready: false, Size: 18998},
		6263: {DataID: 6263, Name: "rel-6257-3", Ready: false, Size: 18998},
		7000: {DataID: 7000, Name: "rel-1-2", Ready: true, Size: 1},
		7001: {DataID: 7001, Name: "rel-1-2", Ready: true, Size: 2},
	}}
	// La carte est parcourue dans un ordre aleatoire : on repete pour l'exposer.
	for i := 0; i < 50; i++ {
		if id, _ := miniatureDe(6257, 2); id != 6262 {
			t.Fatalf("vignette d'un ecran : %d, attendu 6262 (la prete)", id)
		}
		if id, _ := miniatureDe(6257, 3); id != 6260 {
			t.Fatalf("vignette entiere : %d, attendu 6260 (la prete)", id)
		}
		if id, taille := miniatureDe(1, 2); id != 7001 || taille != 2 {
			t.Fatalf("deux pretes : %d, attendu la plus recente 7001", id)
		}
	}
	if id, _ := miniatureDe(99, 2); id != 0 {
		t.Fatalf("aucune vignette : %d, attendu 0", id)
	}
}

// TestVignettesDeCourseInfo : la fiche annonce, pour la vignette d'un ecran (type 2 du
// protocole), le fichier televerse en type 1 (640x360, signe), et pour celle du niveau
// entier (type 3), le fichier televerse en type 2. Mesure 2026-10-05 : nous servions
// l'inverse et le jeu affichait des frimousses tristes.
func TestVignettesDeCourseInfo(t *testing.T) {
	ancien, url := courses, storageURL
	defer func() { courses, storageURL = ancien, url }()
	storageURL = "https://exemple"
	courses = &courseStore{byID: map[uint64]*courseMeta{
		101: {DataID: 101, Name: "rel-77-1", Ready: true, Size: 114688},
		102: {DataID: 102, Name: "rel-77-2", Ready: true, Size: 15673},
		103: {DataID: 103, Name: "rel-77-3", Ready: true, Size: 18998},
	}}
	s := nex.NewSwitchSettings(accessKey, nexVersion)
	out := nex.NewStreamOut(s)
	ecrireMiniature(out, &courseMeta{DataID: 77}, 2)
	ecrireMiniature(out, &courseMeta{DataID: 77}, 3)
	b := string(out.Bytes())
	un, entier := strings.Index(b, "/object/101"), strings.Index(b, "/object/102")
	if un < 0 || entier < 0 || un > entier || strings.Contains(b, "/object/103") {
		t.Fatalf("vignettes : un ecran en %d, niveau entier en %d, signalement present=%v",
			un, entier, strings.Contains(b, "/object/103"))
	}
}
