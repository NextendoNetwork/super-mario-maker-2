package main

import (
	"testing"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

func TestOfficialMakerFlag(t *testing.T) {
	defer isolerNotes(t)()
	s := nex.NewSwitchSettings(accessKey, nexVersion)

	officiels.ajouter(9990001)
	nex.SMM2EstOfficielFn = officiels.estOfficiel

	if !officiels.estOfficiel(9990001) {
		t.Fatalf("PID 9990001 devrait etre officiel")
	}
	if officiels.estOfficiel(12345) {
		t.Fatalf("PID 12345 ne devrait pas etre officiel")
	}

	// Serialiser UserInfo pour le joueur officiel
	outOff := nex.NewStreamOut(s)
	nex.EcrireUserInfo(outOff, nex.SMM2Profil{PID: 9990001, Nom: "Nintendo", Pays: "JP"})

	in := nex.NewStreamIn(outOff.Bytes(), s)
	_ = in.U8() // version 3
	sub := in.Substream()
	_ = sub.PID()
	_ = sub.String() // code
	_ = sub.String() // nom
	_ = sub.U8()     // unk1 version
	_ = sub.Substream()
	_ = sub.QBuffer() // Mii
	_ = sub.String()  // pays
	_ = sub.U8()      // region
	_ = sub.DateTime()// last_active
	_ = sub.Bool()    // unk3
	_ = sub.Bool()    // unk4
	_ = sub.Bool()    // unk5
	// 5 maps + badges + 2 maps
	for m := 0; m < 5; m++ {
		count := sub.U32()
		for i := uint32(0); i < count; i++ {
			sub.U8()
			sub.U32()
		}
	}
	_ = sub.U32() // badges list count
	for m := 0; m < 2; m++ {
		count := sub.U32()
		for i := uint32(0); i < count; i++ {
			sub.U8()
			sub.U32()
		}
	}

	// Revision 3 fields:
	isEmployee := sub.Bool()
	if !isEmployee {
		t.Fatalf("IsNintendoEmployee devrait valoir true pour PID 9990001")
	}

	// Serialiser UserInfo pour un joueur normal
	outNorm := nex.NewStreamOut(s)
	nex.EcrireUserInfo(outNorm, nex.SMM2Profil{PID: 12345, Nom: "Joueur", Pays: "US"})

	inNorm := nex.NewStreamIn(outNorm.Bytes(), s)
	_ = inNorm.U8()
	subNorm := inNorm.Substream()
	_ = subNorm.PID()
	_ = subNorm.String()
	_ = subNorm.String()
	_ = subNorm.U8()
	_ = subNorm.Substream()
	_ = subNorm.QBuffer()
	_ = subNorm.String()
	_ = subNorm.U8()
	_ = subNorm.DateTime()
	_ = subNorm.Bool()
	_ = subNorm.Bool()
	_ = subNorm.Bool()
	for m := 0; m < 5; m++ {
		count := subNorm.U32()
		for i := uint32(0); i < count; i++ {
			subNorm.U8()
			subNorm.U32()
		}
	}
	_ = subNorm.U32()
	for m := 0; m < 2; m++ {
		count := subNorm.U32()
		for i := uint32(0); i < count; i++ {
			subNorm.U8()
			subNorm.U32()
		}
	}
	isEmployeeNorm := subNorm.Bool()
	if isEmployeeNorm {
		t.Fatalf("IsNintendoEmployee devrait valoir false pour PID normal 12345")
	}
}
