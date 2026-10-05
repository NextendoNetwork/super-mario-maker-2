package main

import (
	"testing"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

// TestJoueursDuNiveau : 53 rend ceux qui ont joue, 54 ceux qui ont reussi (y compris le
// premier finisseur et le detenteur du record), 55 ceux qui ont aime ; et la reponse est
// une simple List<UserInfo> limitee au nombre demande.
func TestJoueursDuNiveau(t *testing.T) {
	defer isolerNotes(t)()
	r, p := resultats, profilDe
	defer func() { resultats, profilDe = r, p }()
	resultats = &magasinResultats{parNiv: map[uint64]*resultatNiveau{
		10: {PremierPID: 5},
	}, parJoueur: map[uint64]*statsJoueur{}}
	profilDe = func(pid uint64) (nex.SMM2Profil, bool) { return nex.SMM2Profil{PID: pid, Nom: "j"}, true }

	notes.avancer(10, 1, 2)        // a joue, mort
	notes.avancer(10, 2, 3)        // a reussi
	notes.noter(10, 3, noteAime)   // a aime
	notes.noter(10, 4, noteAucune) // a reussi (note de fin de niveau)
	notes.noter(10, 6, noteBouh)   // a hue
	egal := func(a []uint64, b ...uint64) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}
	if l := joueursDuNiveau(10, listeJoue); !egal(l, 1, 2, 3, 4, 5, 6) {
		t.Fatalf("53 : %v", l)
	}
	if l := joueursDuNiveau(10, listeReussi); !egal(l, 2, 4, 5) {
		t.Fatalf("54 : %v", l)
	}
	if l := joueursDuNiveau(10, listeAime); !egal(l, 3) {
		t.Fatalf("55 : %v", l)
	}

	s := nex.NewSwitchSettings(accessKey, nexVersion)
	pp := nex.NewStreamOut(s)
	pp.U64(10)
	pp.U32(0)
	pp.U32(2) // nombre : 2
	rep := smm2SearchUsersPlayedCourse(&nex.Connection{Settings: s, PID: 1},
		&nex.RMCMessage{Settings: s, Mode: nex.RMCRequest, Protocol: 0x73, Method: 53, CallID: 1, Body: frameStruct(s, 0, pp.Bytes())})
	if n := nex.NewStreamIn(rep.Body, s).U32(); n != 2 {
		t.Fatalf("53 avec nombre 2 : %d joueur(s)", n)
	}
}

// TestTenuesToutesDebloquees : la 63 rend une List<MiiClothes> qui couvre au moins toutes
// les tenues vues chez Nintendo (categories 0 a 3, numeros jusqu'a 64), dans la forme
// mesuree : chaque element encadre { Uint16, Uint16, Bool }, cinq octets.
func TestTenuesToutesDebloquees(t *testing.T) {
	s := nex.NewSwitchSettings(accessKey, nexVersion)
	rep := smm2GetMiiClothes(&nex.Connection{Settings: s, PID: 1},
		&nex.RMCMessage{Settings: s, Mode: nex.RMCRequest, Protocol: 0x73, Method: 63, CallID: 1})
	in := nex.NewStreamIn(rep.Body, s)
	n := in.U32()
	vues := map[[2]uint16]bool{}
	for i := uint32(0); i < n; i++ {
		_ = in.U8()
		e := in.Substream()
		cat, num := e.U16(), e.U16()
		_ = e.Bool()
		if e.Remaining() != 0 {
			t.Fatalf("element %d : %d octet(s) de reste", i, e.Remaining())
		}
		vues[[2]uint16{cat, num}] = true
	}
	// Quelques tenues de la liste mesuree chez Nintendo, dont la plus haute de chaque
	// categorie.
	for _, x := range [][2]uint16{{0, 37}, {1, 64}, {2, 10}, {3, 39}, {0, 0}} {
		if !vues[x] {
			t.Fatalf("tenue %v absente", x)
		}
	}
	if in.Remaining() != 0 || in.Err() != nil {
		t.Fatal("reponse mal formee")
	}
}
