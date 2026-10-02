package main

import (
	"bytes"
	"testing"
	"time"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

func isolerNotes(t *testing.T) func() {
	restaurer := isolerCommentaires(t)
	n := notes
	notes = &magasinNotes{Notes: map[uint64]map[uint64]uint8{}, Donnees: map[uint64]uint32{}, Morts: map[uint64][]marqueMort{}}
	return func() { notes = n; restaurer() }
}

// TestCanPostRatingCommeNintendo : la 61 donne les MEMES octets que Nintendo (capturas-smm2
// c440, niveau 0x396e893) pour un joueur qui a deja donne quatre notes et laisse deux
// commentaires dans la journee : 99999995 notes et 98 commentaires restants.
func TestCanPostRatingCommeNintendo(t *testing.T) {
	defer isolerNotes(t)()
	s := nex.NewSwitchSettings(accessKey, nexVersion)
	notes.Donnees[1] = 4
	maintenant := time.Now().Unix()
	commentaires.parNiv[42] = []commentaire{{DataID: 42, PID: 1, Quand: maintenant}, {DataID: 42, PID: 1, Quand: maintenant}, {DataID: 42, PID: 1, Quand: maintenant - 2*86400}}
	attendu := vec("002e00000093e896030000000001000000000200000000fbe0f505010100000001000000000200000000620000000102000000")
	if obtenu := encode61(s, 0x396e893, 1); !bytes.Equal(obtenu, attendu) {
		t.Fatalf("61 differe de Nintendo :\n obtenu  %x\n attendu %x", obtenu, attendu)
	}
}

// TestMarquesDeMortDepuisLa96 : un essai RATE de Nintendo (c455) laisse une marque en
// (344,169) ; un essai REUSSI (c518) n'en laisse aucune ; la 103, interrogee avec le
// parametre de Nintendo (un Uint64 nu, c441), la rend dans la forme mesuree.
func TestMarquesDeMortDepuisLa96(t *testing.T) {
	defer isolerNotes(t)()
	s := nex.NewSwitchSettings(accessKey, nexVersion)
	conn := &nex.Connection{Settings: s, PID: 1}
	appel := func(m uint32, corps []byte) *nex.RMCMessage {
		return &nex.RMCMessage{Settings: s, Mode: nex.RMCRequest, Protocol: 0x73, Method: m, CallID: 1, Body: corps}
	}
	smm2PostPlayResult(conn, appel(96, vec("012e00000093e896030000000002000000ffffffff00000d00000093e89603000000005801a900000100000500000001010000")))
	smm2PostPlayResult(conn, appel(96, vec("012e000000df8c82030000000001000000f996000001000d000000000000000000000000000000000100000500000001010000")))

	if l := notes.marques(0x396e893); len(l) != 1 || l[0] != (marqueMort{X: 344, Y: 169}) {
		t.Fatalf("marques sur 0x396e893 : %+v", l)
	}
	if l := notes.marques(0x3828cdf); len(l) != 0 {
		t.Fatalf("un niveau reussi a laisse une marque : %+v", l)
	}

	r := smm2GetDeathPositions(conn, appel(103, vec("93e8960300000000")))
	// Un element, puis la forme de Nintendo : 00 0d000000 <data_id> <x> <y> <zone>.
	attendu := append(vec("01000000"), vec("000d00000093e89603000000005801a90000")...)
	if !bytes.Equal(r.Body, attendu) {
		t.Fatalf("103 :\n obtenu  %x\n attendu %x", r.Body, attendu)
	}
}

// TestNotesDepuisLa104 : le J'aime (c454) et le Bouh (c496) de Nintendo comptent ; changer
// d'avis remplace ; une note « aucune » n'efface rien.
func TestNotesDepuisLa104(t *testing.T) {
	defer isolerNotes(t)()
	s := nex.NewSwitchSettings(accessKey, nexVersion)
	appel := func(pid uint64, corps []byte) {
		c := &nex.Connection{Settings: s, PID: pid}
		r := smm2PostRatingInfo(c, &nex.RMCMessage{Settings: s, Mode: nex.RMCRequest, Protocol: 0x73, Method: 104, CallID: 1, Body: corps})
		if r.IsError || len(r.Body) != 0 {
			t.Fatalf("104 : %+v, attendu un succes vide", r)
		}
	}
	appel(1, vec("000b00000093e8960300000000010001")) // J'aime
	appel(2, vec("000b00000093e8960300000000020001")) // Bouh (meme niveau, autre joueur)
	if a, b, _ := notes.compte(0x396e893); a != 1 || b != 1 {
		t.Fatalf("coeurs=%d bouh=%d", a, b)
	}
	appel(2, vec("000b00000093e8960300000000010001")) // le second change d'avis
	appel(1, vec("000b00000093e8960300000000000101")) // « aucune » apres un J'aime
	if a, b, _ := notes.compte(0x396e893); a != 2 || b != 0 {
		t.Fatalf("apres changement : coeurs=%d bouh=%d", a, b)
	}
	if notes.restantes(2) != quotaNotes-2 {
		t.Fatalf("quota du joueur 2 : %d", notes.restantes(2))
	}
}

// TestCourseInfoNotesJamaisDiviseurNul : la table des notes de CourseInfo porte les
// coeurs et les bouh, et sa cle 2 n'est JAMAIS nulle ni inferieure aux notes. A zero, le
// jeu plantait a l'entree de Course World (2026-10-02, apres la 84) ; vide, il
// desactivait les boutons.
func TestCourseInfoNotesJamaisDiviseurNul(t *testing.T) {
	defer isolerNotes(t)()
	s := nex.NewSwitchSettings(accessKey, nexVersion)
	table := func(dataID uint64) map[uint8]uint32 {
		out := nex.NewStreamOut(s)
		ecrireCourseInfo(out, &courseMeta{DataID: dataID, Name: "n"}, 0x1ff)
		in := nex.NewStreamIn(out.Bytes(), s)
		_ = in.U8()
		p := in.Substream()
		p.U64()
		_ = p.String()
		p.PID()
		_ = p.String()
		_ = p.String()
		p.U8()
		p.U8()
		p.DateTime()
		p.U8()
		p.U8()
		p.U8()
		p.U8()
		p.U32()
		p.U16()
		p.U16()
		p.QBuffer()
		for n := p.U32(); n > 0; n-- { // statistiques
			p.U8()
			p.U32()
		}
		m := map[uint8]uint32{}
		for n := p.U32(); n > 0; n-- {
			k := p.U8()
			m[k] = p.U32()
		}
		if p.Err() != nil {
			t.Fatal(p.Err())
		}
		return m
	}
	if m := table(76); m[2] < 1 || len(m) != 3 {
		t.Fatalf("niveau sans note ni partie : %v, attendu trois cles et un diviseur >= 1", m)
	}
	notes.noter(77, 1, noteAime)
	notes.noter(77, 2, noteAime)
	notes.noter(77, 3, noteBouh)
	if m := table(77); m[0] != 2 || m[1] != 1 || m[2] < 3 {
		t.Fatalf("niveau note : %v, attendu 2 coeurs, 1 bouh, diviseur >= 3", m)
	}
}
