package main

import (
	"bytes"
	"encoding/hex"
	"testing"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

// TestSuperMondeRejoueNintendo rejoue l'entree dans un super monde jouee chez NINTENDO le
// 2026-10-02 (capturas-smm2, connexion k08) et exige les MEMES octets : progression vide
// (163), debut du monde (165), deux avancees (166), puis le retour (163) — l'ecran ou
// « Continuer » apparait chez Nintendo et pas chez nous.
//
// Les octets ne contiennent que l'identifiant du monde et des valeurs de jeu. Cet
// identifiant commence par le PID du createur en hexadecimal — un autre joueur — :
// remplace par 1000000000000001, meme longueur, partout ou il apparait.
func TestSuperMondeRejoueNintendo(t *testing.T) {
	s := nex.NewSwitchSettings(accessKey, nexVersion)
	conn := &nex.Connection{Settings: s, PID: 1}

	ancien := superMondes
	defer func() { superMondes = ancien }()
	superMondes = &magasinSuperMondes{ // chemin vide : rien sur disque
		parPID:  map[uint64]*superMonde{},
		parID:   map[string]*superMonde{},
		progres: map[string]*progressionMonde{},
	}

	handlers := map[uint32]func(*nex.Connection, *nex.RMCMessage) *nex.RMCMessage{
		163: smm2GetWorldMapProgress,
		165: smm2InitializeWorldMapProgress,
		166: smm2UpdateWorldMapProgress,
	}
	appels := []struct {
		methode, appel   uint32
		requete, reponse string
	}{
		{163, 543, "00280000002600313030303030303030303030303030315f323032303035313231383237303038323737363900", "3200000073011f020000a380000000230000000100000000000000000000000000000000000000000000000000000000000000000000"},
		{165, 544, "002c0000002600313030303030303030303030303030315f32303230303531323138323730303832373736390000000000", "0a000000730120020000a5800000"},
		{166, 554, "00480000002600313030303030303030303030303030315f3230323030353132313832373030383237373639007e55590000000000000000000000000001000300010000000000030000000000", "0a00000073012a020000a6800000"},
		{166, 564, "00480000002600313030303030303030303030303030315f3230323030353132313832373030383237373639007e555900000000000100000000000000010003000100dc370000030000000000", "0a000000730134020000a6800000"},
		{163, 566, "00280000002600313030303030303030303030303030315f323032303035313231383237303038323737363900", "57000000730136020000a380000000480000002600313030303030303030303030303030315f323032303035313231383237303038323737363900020100dc3700000003007e555900000000000100000000000000000003000000"},
	}
	for _, a := range appels {
		corps, _ := hex.DecodeString(a.requete)
		attendu, _ := hex.DecodeString(a.reponse)
		req := &nex.RMCMessage{Settings: s, Mode: nex.RMCRequest, Protocol: 0x73, Method: a.methode, CallID: a.appel, Body: corps}
		obtenu := handlers[a.methode](conn, req).Encode()
		if !bytes.Equal(obtenu, attendu) {
			t.Fatalf("%d c%d differe de Nintendo :\n obtenu  %x\n attendu %x", a.methode, a.appel, obtenu, attendu)
		}
	}
}
