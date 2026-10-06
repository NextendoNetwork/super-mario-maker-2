package main

import (
	"testing"
	"time"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

func TestPaysChoisiSurLeSiteRemplaceLeDeclare(t *testing.T) {
	c := &cachePays{m: map[uint64]entreePays{}, enCours: map[uint64]bool{}}
	c.retenir(1800000301, "mx")
	if got := c.afficher(1800000301, "FR"); got != "MX" {
		t.Fatalf("pays affiche = %q, attendu MX", got)
	}
}

func TestPaysInconnuGardeLeDeclareEtSeCherche(t *testing.T) {
	c := &cachePays{m: map[uint64]entreePays{}, enCours: map[uint64]bool{}}
	demande := make(chan uint64, 1)
	c.lire = func(pid uint64) { demande <- pid; c.retenir(pid, "AR") }
	if got := c.afficher(1800000302, "FR"); got != "" {
		t.Fatalf("sans donnee, l'affichage ne doit rien inventer: %q", got)
	}
	select {
	case <-demande:
	case <-time.After(2 * time.Second):
		t.Fatal("aucune lecture lancee en arriere-plan")
	}
	for i := 0; i < 100 && c.afficher(1800000302, "FR") != "AR"; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if got := c.afficher(1800000302, "FR"); got != "AR" {
		t.Fatalf("apres lecture: %q", got)
	}
}

func TestPaysVideOuInvalideNeRemplacePas(t *testing.T) {
	c := &cachePays{m: map[uint64]entreePays{}, enCours: map[uint64]bool{}}
	c.retenir(1800000303, "")
	c.retenir(1800000304, "France")
	for _, p := range []uint64{1800000303, 1800000304} {
		if got := c.afficher(p, "FR"); got != "" {
			t.Fatalf("pid %d : %q", p, got)
		}
	}
}

// Bout a bout : le profil servi au client porte le pays choisi, pas celui declare.
func TestUserInfoPorteLePaysChoisi(t *testing.T) {
	c := &cachePays{m: map[uint64]entreePays{}, enCours: map[uint64]bool{}}
	c.retenir(1800000305, "CL")
	ancien := nex.SMM2PaysFn
	nex.SMM2PaysFn = c.afficher
	defer func() { nex.SMM2PaysFn = ancien }()

	if got := nex.SMM2Pays(1800000305, "FR"); got != "CL" {
		t.Fatalf("SMM2Pays = %q", got)
	}
	if got := nex.SMM2Pays(1800000306, "FR"); got != "FR" {
		t.Fatalf("sans choix, le declare doit rester: %q", got)
	}
}
