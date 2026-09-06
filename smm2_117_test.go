package main

import (
	"bytes"
	"testing"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

// TestEncode117FormeMesuree fixe la reponse du 117 SUR LES OCTETS.
//
// La forme vient de l'analyseur du client, a 0x5e79b0 dans main.bin une fois les
// relocations appliquees : un bool, puis deux structures encadrees de corps
// { uint32 ; bool }. Douze formes avaient ete essayees avant, toutes des suites
// d'uint32 ; aucune ne pouvait passer. Affirmer ici sur le cable est ce qui empeche
// d'y retomber, parce qu'une structure « qui a l'air juste » sur le papier est
// exactement ce qui a coute les douze essais.
func TestEncode117FormeMesuree(t *testing.T) {
	s := nex.NewSwitchSettings(accessKey, nexVersion)

	cas := []struct {
		nom    string
		classe bool
		notes  [2]note117
		veut   []byte
	}{
		{
			// Les valeurs que le client se donne a lui-meme quand le champ manque.
			nom:    "defaut du client",
			classe: false,
			notes:  [2]note117{{0xFFFFFFFF, false}, {0xFFFFFFFF, false}},
			veut: []byte{
				0x00,
				0x00, 0x05, 0x00, 0x00, 0x00, 0xFF, 0xFF, 0xFF, 0xFF, 0x00,
				0x00, 0x05, 0x00, 0x00, 0x00, 0xFF, 0xFF, 0xFF, 0xFF, 0x00,
			},
		},
		{
			nom:    "valeurs posees",
			classe: true,
			notes:  [2]note117{{1500, true}, {350, true}},
			veut: []byte{
				0x01,
				0x00, 0x05, 0x00, 0x00, 0x00, 0xDC, 0x05, 0x00, 0x00, 0x01,
				0x00, 0x05, 0x00, 0x00, 0x00, 0x5E, 0x01, 0x00, 0x00, 0x01,
			},
		},
	}

	for _, c := range cas {
		got := encode117(s, c.classe, c.notes)
		if !bytes.Equal(got, c.veut) {
			t.Errorf("%s:\n  obtenu %x\n  attendu %x", c.nom, got, c.veut)
		}
	}
}

// TestEncode117NEstPasUneSuiteDUint32 est l'epreuve a l'envers : elle echoue si
// quelqu'un revient a l'ancienne forme.
//
// L'ancienne reponse etait trois uint32, encadres ou nus, soit 17 ou 12 octets et
// commencant par l'octet de poids faible de 1500 (0xDC). La mesure dit 21 octets et un
// bool en tete. Sans cette epreuve, le retour en arriere se ferait en silence.
func TestEncode117NEstPasUneSuiteDUint32(t *testing.T) {
	s := nex.NewSwitchSettings(accessKey, nexVersion)
	got := encode117(s, false, [2]note117{{1500, false}, {350, false}})

	if len(got) != 21 {
		t.Fatalf("la reponse mesuree fait 21 octets, obtenu %d (%x)", len(got), got)
	}
	if got[0] != 0x00 && got[0] != 0x01 {
		t.Errorf("le champ 0 est un bool : attendu 0 ou 1, obtenu %#x", got[0])
	}
	// Le premier octet ne doit PAS etre le poids faible d'un uint32 de valeur.
	if got[0] == 0xDC {
		t.Error("on ecrit encore une suite d'uint32 : c'est la forme refutee")
	}
}

// TestEncode119 fixe la reponse de la 119 : DEUX structures et AUCUN bool devant.
//
// L'ecart avec la 117 tient a un octet. Sans cette epreuve, quelqu'un qui factorise les
// deux methodes « parce qu'elles se ressemblent » casserait le versus sans rien voir.
func TestEncode119(t *testing.T) {
	s := nex.NewSwitchSettings(accessKey, nexVersion)
	got := encode119(s)
	veut := []byte{
		0x00, 0x05, 0x00, 0x00, 0x00, 0xFF, 0xFF, 0xFF, 0xFF, 0x00,
		0x00, 0x05, 0x00, 0x00, 0x00, 0xFF, 0xFF, 0xFF, 0xFF, 0x00,
	}
	if !bytes.Equal(got, veut) {
		t.Errorf("obtenu %x\nattendu %x", got, veut)
	}
	if len(got) != 20 {
		t.Errorf("la 119 fait 20 octets, obtenu %d", len(got))
	}
	// La 117 en fait 21 : si les deux coincident, c'est qu'on a ajoute le bool de tete.
	if len(got) == len(encode117(s, false, [2]note117{{0xFFFFFFFF, false}, {0xFFFFFFFF, false}})) {
		t.Error("la 119 ne doit PAS porter le bool de tete de la 117")
	}
}
