package main

import "testing"

func TestRecordSurvitAUneNouvellePartie(t *testing.T) {
	m := &magasinEndless{parPID: map[uint64]*[4]partieEndless{}}
	const pid, diff = 1, uint8(0)

	// Primera partida: completa 10 niveles.
	m.demarrer(pid, diff)
	for i := 0; i < 10; i++ {
		m.demarrerCours(pid, diff, uint64(i+1))
		m.reussirCours(pid, diff, 0, 0, 0)
	}
	m.terminer(pid, diff) // record deberia quedar en 10

	if got := m.recordsDe(pid)[diff]; got != 10 {
		t.Fatalf("record tras la primera partida = %d, queria 10", got)
	}

	// Segunda partida: solo 8 niveles, peor que el record anterior.
	m.demarrer(pid, diff)
	for i := 0; i < 8; i++ {
		m.demarrerCours(pid, diff, uint64(i+1))
		m.reussirCours(pid, diff, 0, 0, 0)
	}
	m.terminer(pid, diff)

	if got := m.recordsDe(pid)[diff]; got != 10 {
		t.Fatalf("record tras la segunda partida = %d, queria que siguiera en 10 (una partida peor no debe bajar el record)", got)
	}
}

func TestRecordSubeConUnaPartidaMejor(t *testing.T) {
	m := &magasinEndless{parPID: map[uint64]*[4]partieEndless{}}
	const pid, diff = 1, uint8(0)

	m.demarrer(pid, diff)
	for i := 0; i < 10; i++ {
		m.demarrerCours(pid, diff, uint64(i+1))
		m.reussirCours(pid, diff, 0, 0, 0)
	}
	m.terminer(pid, diff)

	m.demarrer(pid, diff)
	for i := 0; i < 15; i++ {
		m.demarrerCours(pid, diff, uint64(i+1))
		m.reussirCours(pid, diff, 0, 0, 0)
	}
	m.terminer(pid, diff)

	if got := m.recordsDe(pid)[diff]; got != 15 {
		t.Fatalf("record = %d, queria 15 (una partida mejor SI debe subir el record)", got)
	}
}
