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

// TestCourseInfoNotesVides : la table des notes de CourseInfo reste VIDE, meme quand des
// notes existent. Remplie, elle faisait planter le jeu a l'entree de Course World
// (2026-10-02, apres la 84). On cherche l'octet de la table : stats (5 cles, 29 octets
// apres le compte) puis la table des notes, qui doit etre un compte nul.
func TestCourseInfoNotesVides(t *testing.T) {
	defer isolerNotes(t)()
	s := nex.NewSwitchSettings(accessKey, nexVersion)
	notes.noter(77, 1, noteAime)
	out := nex.NewStreamOut(s)
	ecrireCourseInfo(out, &courseMeta{DataID: 77, Name: "n"}, 0x1ff, 0)
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
	if n := p.U32(); n != 0 || p.Err() != nil {
		t.Fatalf("table des notes : %d entree(s) (err %v), attendu vide", n, p.Err())
	}
}

// TestCourseInfoNotesSeulementSiJoue : un niveau JOUE (parties, tentatives et reussites
// non nulles, comme toutes les fiches mesurees chez Nintendo) recoit la forme complete —
// statistiques 0 a 4 dans l'ordre, notes {coeurs, bouh, base}, table 0x40 a deux cles.
// Un niveau jamais termine garde la forme d'avant, notes VIDES : remplie pour lui, elle
// faisait planter Course World (2026-10-02).
func TestCourseInfoNotesSeulementSiJoue(t *testing.T) {
	defer isolerNotes(t)()
	r := resultats
	defer func() { resultats = r }()
	resultats = &magasinResultats{parNiv: map[uint64]*resultatNiveau{
		77: {Parties: 25, Tentatives: 70, Reussites: 15},
		78: {Parties: 3, Tentatives: 9, Reussites: 0},
	}, parJoueur: map[uint64]*statsJoueur{}}
	notes.noter(77, 1, noteAime)
	notes.noter(77, 2, noteBouh)
	notes.noter(78, 1, noteAime)
	s := nex.NewSwitchSettings(accessKey, nexVersion)
	tables := func(dataID uint64) [3][]uint8 {
		out := nex.NewStreamOut(s)
		ecrireCourseInfo(out, &courseMeta{DataID: dataID, Name: "n"}, 0x1ff, 0)
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
		for i := 0; i < 4; i++ {
			p.U8()
		}
		p.U32()
		p.U16()
		p.U16()
		p.QBuffer()
		var res [3][]uint8
		for k := 0; k < 3; k++ {
			for n := p.U32(); n > 0; n-- {
				res[k] = append(res[k], p.U8())
				p.U32()
			}
		}
		if p.Err() != nil {
			t.Fatal(p.Err())
		}
		return res
	}
	if got := tables(77); len(got[0]) != 5 || got[0][4] != 4 || len(got[1]) != 3 || len(got[2]) != 2 {
		t.Fatalf("niveau joue : cles %v, attendu stats 0-4, notes 0-2, 0x40 0-1", got)
	}
	if got := tables(78); len(got[1]) != 0 || len(got[2]) != 0 {
		t.Fatalf("niveau jamais termine : cles %v, attendu notes et 0x40 vides", got)
	}
}

// TestEtatJoueurDansLaFiche : les deux premiers des quatre octets disent a CE joueur ou
// il en est (1 jamais joue, 2 joue, 3 reussi) et ce qu'il a note (1 aucune, 2 joue sans
// noter, 3 J'aime, 4 Bouh) — les combinaisons mesurees chez Nintendo le 2026-10-05.
func TestEtatJoueurDansLaFiche(t *testing.T) {
	defer isolerNotes(t)()
	cas := []struct {
		nom        string
		faire      func()
		etat, note uint8
	}{
		{"jamais joue", func() {}, 1, 1},
		{"mort puis Bouh", func() { notes.avancer(1, 7, 2); notes.noter(1, 7, noteBouh) }, 2, 4},
		{"mort puis J'aime", func() { notes.avancer(2, 7, 2); notes.noter(2, 7, noteAime) }, 2, 3},
		{"reussi sans noter", func() { notes.avancer(3, 7, 3); notes.noter(3, 7, noteAucune) }, 3, 2},
		{"reussi puis mort", func() { notes.avancer(4, 7, 3); notes.avancer(4, 7, 2) }, 3, 2},
		{"note ancienne « aucune »", func() { notes.noter(5, 7, noteAucune) }, 3, 2},
	}
	for i, c := range cas {
		c.faire()
		if e, n := notes.etatJoueur(uint64(i), 7); e != c.etat || n != c.note {
			t.Errorf("%s : (%d, %d), attendu (%d, %d)", c.nom, e, n, c.etat, c.note)
		}
	}
	// Un autre joueur ne voit pas l'etat du premier.
	if e, n := notes.etatJoueur(1, 8); e != 1 || n != 1 {
		t.Errorf("autre joueur : (%d, %d), attendu (1, 1)", e, n)
	}
}
