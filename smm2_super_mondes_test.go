package main

import (
	"encoding/hex"
	"testing"
	"time"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

// Les deux fiches des super mondes, encodees par l'IMPLEMENTATION DE REFERENCE avec les
// valeurs posees ci-dessous. Produites par ~/smm2-re/mundo-arnes, qui vit hors du depot.
//
// POURQUOI CE TEST EXISTE. Une fiche mal formee ne provoque aucune erreur lisible : le
// client lit un champ de travers, tout ce qui suit se decale, et il rejette la reponse
// entiere en silence. C'est exactement ce qui s'etait passe avec CourseTimeStats, ou deux
// PID pris pour des Uint32 decalaient la fiche de douze octets. Comparer les octets attrape
// cela en une seconde ; le raisonnement, lui, avait echoue huit fois de suite.
const (
	refWorldMapInfo = "009a0000001f0030303030303030302d303030302d303365382d303030302d30303030303000e8030000000000000500010203040500340000001c0068747470733a2f2f6578656d706c652f6f626a6563742f343234320002d204000002000000aabb0900343234322e62696e00030304de6339aa1f000000030000000b000000000000001600000000000000210000000000000000000000630000000101"
	refWorldMapProg = "004500000021003030303030303030303030303033653830303030303030303030306634323430000104118813000002030409030000000000007803000000000000020009082a000000"
)

func TestFicheSuperMondeOctetPourOctet(t *testing.T) {
	s := nex.NewSwitchSettings(accessKey, nexVersion)

	// La vignette est retrouvee par le NOM de l'objet rattache : « rel-<id du monde>-<type> ».
	// On en pose une, pour que la fiche porte une vraie adresse plutot que /object/0.
	const idMonde = "00000000-0000-03e8-0000-000000"
	precedent := storageURL
	storageURL = "https://exemple"
	defer func() { storageURL = precedent }()
	courses.rootCA = []byte{0xAA, 0xBB}
	courses.byID[4242] = &courseMeta{DataID: 4242, Name: "rel-" + idMonde + "-2", Size: 1234}
	defer delete(courses.byID, 4242)

	// Une date REELLE, pas des bits choisis a la main. Le premier essai avait pris
	// 0x69C0C0C0 pour un DateTime valable : desempaquete, il donne un jour et un mois qui
	// n'existent pas, `time.Date` les normalise, et le reempaquetage rend autre chose. Le
	// test echouait sur une faute du banc d'essai, pas du serveur — d'ou cette date-ci.
	instant := time.Date(2026, 8, 28, 22, 15, 30, 0, time.UTC)
	if got := uint64(nex.MakeDateTime(2026, 8, 28, 22, 15, 30)); got != 0x1FAA3963DE {
		t.Fatalf("l'empaquetage du DateTime a change : %#x\n", got)
	}

	sm := &superMonde{
		ID:          idMonde,
		OwnerPID:    1000,
		Plan:        []byte{1, 2, 3, 4, 5},
		Niveaux:     []uint64{11, 22, 33},
		Mondes:      3,
		TypePlanete: 4,
		Unk5:        99,
		MajLe:       instant.Unix(),
	}
	out := nex.NewStreamOut(s)
	ecrireWorldMapInfo(out, sm)
	comparer(t, "WorldMapInfo", refWorldMapInfo, out.Bytes())

	prog := &progressionMonde{
		Unk20: 1, Vies: 4, Pieces: 17, Points: 5000,
		Unk21: 2, Unk22: 3, Unk23: 4,
		CourseID: 777, Unk1: 888,
		Unk11: []byte{9, 8}, Unk10: 42,
	}
	out2 := nex.NewStreamOut(s)
	ecrireWorldMapProgressInfo(out2, "00000000000003e800000000000f4240", prog)
	comparer(t, "WorldMapProgressInfo", refWorldMapProg, out2.Bytes())
}

// comparer dit OU les octets divergent, pas seulement QU'ILS divergent : sur une fiche de
// cent cinquante octets, « ce n'est pas egal » ne se debogue pas.
func comparer(t *testing.T, nom, attenduHex string, obtenu []byte) {
	t.Helper()
	attendu, err := hex.DecodeString(attenduHex)
	if err != nil {
		t.Fatalf("%s : reference illisible : %v", nom, err)
	}
	if string(attendu) == string(obtenu) {
		return
	}
	n := len(attendu)
	if len(obtenu) < n {
		n = len(obtenu)
	}
	for i := 0; i < n; i++ {
		if attendu[i] != obtenu[i] {
			t.Errorf("%s : divergence a l'octet %d (0x%x) — reference %02x, nous %02x\n  reference %x\n  nous      %x",
				nom, i, i, attendu[i], obtenu[i], attendu, obtenu)
			return
		}
	}
	t.Errorf("%s : meme prefixe mais longueurs differentes — reference %d octets, nous %d\n  reference %x\n  nous      %x",
		nom, len(attendu), len(obtenu), attendu, obtenu)
}

// TestIdentifiantMondeLargeur : trente caracteres, ni plus ni moins.
//
// ocw-server distingue ses super mondes de ceux de Nintendo par la LONGUEUR de
// l'identifiant — au-dela de trente-cinq caracteres, c'est un identifiant Nintendo. La
// largeur porte donc du sens, et nos trente-deux hexadecimaux nus tombaient du mauvais
// cote. Un test parce qu'une contrainte invisible se reintroduit toute seule.
func TestIdentifiantMondeLargeur(t *testing.T) {
	for _, pid := range []uint64{0, 1, 1800001206, 1 << 62} {
		id := identifiantMondeDe(pid)
		if len(id) != 30 {
			t.Errorf("pid=%d : identifiant de %d caracteres (%q), attendu 30", pid, len(id), id)
		}
	}
	// Stable : le meme joueur doit retrouver son monde, et ses visiteurs leur progression.
	if identifiantMondeDe(1800001206) != identifiantMondeDe(1800001206) {
		t.Error("l'identifiant n'est pas stable pour un meme pid")
	}
	// Distinct : deux createurs ne peuvent pas partager un monde.
	if identifiantMondeDe(1800001206) == identifiantMondeDe(1800001207) {
		t.Error("deux pid differents donnent le meme identifiant")
	}
}
