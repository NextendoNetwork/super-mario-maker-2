package main

import (
	"bytes"
	"encoding/hex"
	"testing"
	"time"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

// TestEndlessRejoueNintendo rejoue, appel par appel, une partie de Mario sans fin jouee
// chez NINTENDO le 2026-10-02 (capturas-smm2, connexion k08) et exige les MEMES octets.
//
// La sequence : etat (108), niveaux (115), nouvelle partie en normale (109), lancement
// d'un niveau (110), reussite +3 vies (111), niveau suivant (110), mort sur ce niveau
// (110, une vie en moins), pause (113), puis etat et niveaux au retour (108, 115). Ce
// retour est l'ecran ou « Continue » n'apparaissait pas chez nous : la 115 rendait quatre
// listes vides et la 108 des dates nulles et des vies plafonnees a 5.
//
// Les octets ne contiennent que des identifiants de niveaux publics et des dates ; aucun
// identifiant de compte.
//
// UN SEUL OCTET N'EST PAS REPRODUIT, et c'est voulu : en super expert, partie close,
// Nintendo rend 7 vies dans la fiche et 8 dans l'etat de manche. Nous ne stockons qu'un
// nombre de vies ; inventer un second champ pour une difficulte qu'on n'a pas vue jouer
// serait deviner. Le test le remplace explicitement.
func TestEndlessRejoueNintendo(t *testing.T) {
	s := nex.NewSwitchSettings(accessKey, nexVersion)
	conn := &nex.Connection{Settings: s, PID: 1}

	ancien, ancienneHorloge := endless, horloge
	defer func() { endless, horloge = ancien, ancienneHorloge }()
	endless = &magasinEndless{parPID: map[uint64]*[4]partieEndless{}} // chemin vide : rien sur disque

	// L'etat que Nintendo avait AVANT la session, tel que le rend la premiere 108.
	niveauxFacile := []niveauEndless{
		{0x33a28d8, 4, 0}, {0x3461636, 0, 0}, {0x2e831e9, 3, 2}, {0x32b7b6f, 1, 5}, {0x31f1418, 0, 0},
		{0x278e81c, 4, 0}, {0x1807e96, 4, 9}, {0x1ebd0fc, 0, 1}, {0xb02de7, 2, 3}, {0x17217c1, 0, 0},
	}
	endless.parPID[1] = &[4]partieEndless{
		{Mode: 2, Pieces: 69, PointsScore: 232570, Debut: 0x1fa55954dd, Suspendue: 0x1faa8530d8, Vies: 5, Reussites: 10, Niveaux: niveauxFacile},
		{Mode: 1, Debut: 0x1fa5741158, Suspendue: 0x9c3f3f7efb},
		{Mode: 3, Debut: 0x1fa5717c83, Suspendue: 0x9c3f3f7efb},
		{Mode: 1, Debut: 0x1fa55ce58b, Suspendue: 0x9c3f3f7efb, Vies: 7},
	}

	// L'heure UTC de chaque appel qui date quelque chose.
	heures := map[uint32]time.Time{
		503: time.Date(2026, 10, 2, 20, 4, 8, 0, time.UTC),  // 109 : debut de partie
		532: time.Date(2026, 10, 2, 20, 6, 19, 0, time.UTC), // 113 : pause
	}

	handlers := map[uint32]func(*nex.Connection, *nex.RMCMessage) *nex.RMCMessage{
		108: smm2GetEndlessModeStatus,
		109: smm2InitEndlessMode,
		110: smm2StartEndlessModeCourse,
		111: smm2DominateEndlessModeCourse,
		113: smm2SuspendEndlessModeCourse,
		115: smm2GetEndlessModePlayInfo,
	}

	appels := []struct {
		methode, appel   uint32
		requete, reponse string
	}{
		{108, 500, "", "bb0000007301f40100006c80000000ac0000000400000000001800000002457a8c0300dd5459a51f000000d83085aa1f0000000505010018000000010000000000581174a51f000000fb7e3f3f9c0000000005020018000000030000000000837c71a51f000000fb7e3f3f9c000000000f0300180000000100000000008be55ca51f000000fb7e3f3f9c000000071e04000000000005000000050a000000010005000000000000000002000500000000000000000300050000000800000000"},
		{115, 501, "", "ef0000007301f50100007380000000e000000004000000000a000000000f000000d8283a030000000000010000000400000f000000361646030000000000020000000000000f000000e931e8020000000000030000000302000f0000006f7b2b030000000000040000000105000f00000018141f030000000000050000000000000f0000001ce878020000000000060000000400000f000000967e80010000000000070000000409000f000000fcd0eb010000000000080000000001000f000000e72db0000000000000090000000203000f000000c117720100000000000a0000000000010000000002000000000300000000"},
		{109, 503, "000100000001", "0a0000007301f70100006d800000"},
		{115, 504, "", "ef0000007301f80100007380000000e000000004000000000a000000000f000000d8283a030000000000010000000400000f000000361646030000000000020000000000000f000000e931e8020000000000030000000302000f0000006f7b2b030000000000040000000105000f00000018141f030000000000050000000000000f0000001ce878020000000000060000000400000f000000967e80010000000000070000000409000f000000fcd0eb010000000000080000000001000f000000e72db0000000000000090000000203000f000000c117720100000000000a0000000000010000000002000000000300000000"},
		{110, 512, "000900000001df8c820300000000", "140000007301000200006e80000000050000000500000000"},
		{111, 517, "001100000001df8c8203000000000305921c01000403", "140000007301050200006f80000000050000000801000000"},
		{110, 526, "000900000001103e480300000000", "1400000073010e0200006e80000000050000000801000000"},
		{110, 531, "000900000001103e480300000000", "140000007301130200006e80000000050000000701000000"},
		{113, 532, "000100000001", "0a00000073011402000071800000"},
		{108, 537, "", "bb0000007301190200006c80000000ac0000000400000000001800000002457a8c0300dd5459a51f000000d83085aa1f00000005050100180000000205921c0100084185aa1f000000934185aa1f0000000705020018000000030000000000837c71a51f000000fb7e3f3f9c000000000f0300180000000100000000008be55ca51f000000fb7e3f3f9c000000071e04000000000005000000050a000000010005000000070100000002000500000000000000000300050000000800000000"},
		{115, 538, "", "0301000073011a0200007380000000f400000004000000000a000000000f000000d8283a030000000000010000000400000f000000361646030000000000020000000000000f000000e931e8020000000000030000000302000f0000006f7b2b030000000000040000000105000f00000018141f030000000000050000000000000f0000001ce878020000000000060000000400000f000000967e80010000000000070000000409000f000000fcd0eb010000000000080000000001000f000000e72db0000000000000090000000203000f000000c117720100000000000a00000000000101000000000f000000df8c8203000000000001000000040302000000000300000000"},
	}

	for _, a := range appels {
		if h, ok := heures[a.appel]; ok {
			horloge = func() time.Time { return h }
		} else {
			horloge = func() time.Time {
				t.Fatalf("appel %d : date demandee sans heure capturee", a.appel)
				return time.Time{}
			}
		}
		corps, _ := hex.DecodeString(a.requete)
		attendu, _ := hex.DecodeString(a.reponse)
		if a.methode == 108 {
			// L'octet du super expert, voir plus haut : derniere structure, vies de manche.
			i := len(attendu) - 5
			if attendu[i] != 8 {
				t.Fatalf("108 c%d : la capture a change (vies super expert = %d)", a.appel, attendu[i])
			}
			attendu[i] = 7
		}
		req := &nex.RMCMessage{Settings: s, Mode: nex.RMCRequest, Protocol: 0x73, Method: a.methode, CallID: a.appel, Body: corps}
		obtenu := handlers[a.methode](conn, req).Encode()
		if !bytes.Equal(obtenu, attendu) {
			t.Fatalf("%d c%d differe de Nintendo :\n obtenu  %x\n attendu %x", a.methode, a.appel, obtenu, attendu)
		}
	}
}

// TestEndlessDemarrerGardeLeRecord : une nouvelle tentative ne doit pas effacer le
// meilleur score de la difficulte.
func TestEndlessDemarrerGardeLeRecord(t *testing.T) {
	ancien := endless
	defer func() { endless = ancien }()
	endless = &magasinEndless{parPID: map[uint64]*[4]partieEndless{}}

	endless.demarrer(7, 1)
	endless.reussirCours(7, 1, 42, 0, 0, 0, 0, 0)
	endless.terminer(7, 1)
	endless.demarrer(7, 1)
	if r := endless.recordsDe(7)[1]; r != 1 {
		t.Fatalf("record apres nouvelle partie = %d, attendu 1", r)
	}
	if n := len(endless.etatDe(7)[1].Niveaux); n != 0 {
		t.Fatalf("une nouvelle partie herite de %d niveau(x)", n)
	}
}
