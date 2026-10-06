package main

import (
	"encoding/binary"
	"testing"
	"time"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

// Identifiants INVENTES : jamais un NSA reel dans un test.
const (
	idTestPID uint64 = 1800000077
	idTestNSA uint64 = 0xC0FFEE0012345678
)

func nouvelleTable(t *testing.T) *tableIdentites {
	t.Helper()
	tb := &tableIdentites{NSA: map[uint64]uint64{}, pid: map[uint64]uint64{}, enCours: map[uint64]bool{}}
	tb.charger(t.TempDir())
	return tb
}

func TestIdentiteDepuisLAuth(t *testing.T) {
	tb := nouvelleTable(t)
	nex.RememberLoginName("14051321617087157880", idTestPID+1) // 0xC3018B..., invente
	if got := tb.public(idTestPID + 1); got != 14051321617087157880 {
		t.Fatalf("le joueur authentifie doit sortir sous son nom de connexion: %d", got)
	}
	if got := tb.interne(14051321617087157880); got != idTestPID+1 {
		t.Fatalf("et rentrer sous son PID interne: %d", got)
	}
}

func TestIdentiteInconnueSortTelleQuelleEtSeCherche(t *testing.T) {
	tb := nouvelleTable(t)
	trouve := make(chan uint64, 1)
	tb.chercher = func(p uint64) (uint64, bool) { trouve <- p; return idTestNSA, true }
	if got := tb.public(idTestPID); got != idTestPID {
		t.Fatalf("sans correspondance, la sortie ne doit pas bloquer ni inventer: %d", got)
	}
	select {
	case p := <-trouve:
		if p != idTestPID {
			t.Fatalf("recherche lancee pour %d", p)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("aucune recherche en arriere-plan")
	}
	for i := 0; i < 100 && tb.public(idTestPID) != idTestNSA; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if got := tb.public(idTestPID); got != idTestNSA {
		t.Fatalf("apres la recherche, le PID doit sortir en NSA: %d", got)
	}
}

func TestIdentiteNeTouchePasAuxPIDDeService(t *testing.T) {
	tb := nouvelleTable(t)
	tb.chercher = func(uint64) (uint64, bool) { t.Fatal("pas de recherche pour un PID de service"); return 0, false }
	for _, p := range []uint64{0, 2, 257049437023956657} {
		if got := tb.public(p); got != p {
			t.Fatalf("public(%d) = %d", p, got)
		}
	}
	if got := tb.interne(2); got != 2 {
		t.Fatalf("interne(2) = %d", got)
	}
}

func TestIdentitePersisteeEtIdempotente(t *testing.T) {
	dir := t.TempDir()
	tb := &tableIdentites{NSA: map[uint64]uint64{}, pid: map[uint64]uint64{}, enCours: map[uint64]bool{}}
	tb.charger(dir)
	tb.lier(idTestPID, idTestNSA)
	tb.sauver()

	re := &tableIdentites{NSA: map[uint64]uint64{}, pid: map[uint64]uint64{}, enCours: map[uint64]bool{}}
	re.charger(dir)
	if re.public(idTestPID) != idTestNSA || re.interne(idTestNSA) != idTestPID {
		t.Fatal("la correspondance doit survivre au redemarrage, dans les deux sens")
	}
	if re.public(idTestNSA) != idTestNSA || re.interne(idTestPID) != idTestPID {
		t.Fatal("les deux traductions doivent etre idempotentes")
	}
}

// Un NSA reattribue par le service de comptes ne doit pas laisser deux PID pour lui.
func TestIdentiteReattribueeRemplaceLAncienne(t *testing.T) {
	tb := nouvelleTable(t)
	tb.lier(idTestPID, idTestNSA)
	tb.lier(idTestPID+5, idTestNSA)
	if tb.interne(idTestNSA) != idTestPID+5 {
		t.Fatal("le NSA doit designer le dernier PID")
	}
	tb.mu.Lock()
	_, reste := tb.NSA[idTestPID]
	tb.mu.Unlock()
	if reste {
		t.Fatal("l'ancien PID garde un NSA qui n'est plus le sien")
	}
}

// L'auteur d'un commentaire sort en U64 brut : il doit etre traduit comme les autres.
func TestCommentaireAuteurPublic(t *testing.T) {
	s := nex.NewSwitchSettings("testkey0", 40000)
	s.PIDPublic = func(p uint64) uint64 {
		if p == idTestPID {
			return idTestNSA
		}
		return p
	}
	out := nex.NewStreamOut(s)
	ecrireCommentInfo(out, commentaire{DataID: 1, PID: idTestPID}, 0)
	b := out.Bytes()
	var tampon [8]byte
	binary.LittleEndian.PutUint64(tampon[:], idTestNSA)
	if !contient(b, tampon[:]) {
		t.Fatal("l'auteur du commentaire ne sort pas sous son identite publique")
	}
	binary.LittleEndian.PutUint64(tampon[:], idTestPID)
	if contient(b, tampon[:]) {
		t.Fatal("le PID interne de l'auteur fuit dans le commentaire")
	}
}

func contient(b, motif []byte) bool {
	for i := 0; i+len(motif) <= len(b); i++ {
		if string(b[i:i+len(motif)]) == string(motif) {
			return true
		}
	}
	return false
}
