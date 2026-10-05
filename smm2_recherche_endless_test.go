package main

import "testing"

func TestNiveauxEndlessFiltreAvantDeLimiter(t *testing.T) {
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
