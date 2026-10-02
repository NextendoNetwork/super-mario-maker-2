package main

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

// isolerCommentaires remplace les magasins globaux par des magasins vides sur disque
// temporaire, et rend la fonction qui les restaure.
func isolerCommentaires(t *testing.T) func() {
	dir := t.TempDir()
	c, m, sd, su := courses, commentaires, storageDir, storageURL
	courses = &courseStore{byID: map[uint64]*courseMeta{}, nextID: 1000, catalog: filepath.Join(dir, "catalog.json")}
	commentaires = &magasinCommentaires{parNiv: map[uint64][]commentaire{}}
	storageDir, storageURL = dir, "https://exemple"
	return func() { courses, commentaires, storageDir, storageURL = c, m, sd, su }
}

// TestCommentaireTexteCommeNintendo : un commentaire texte, serialise par nous, donne les
// MEMES octets que la 95 de Nintendo du 2026-10-02 (capturas-smm2 c529, « Awesome! »).
//
// Trois substitutions dans le vecteur, toutes deux faites a la main et dites ici : le PID
// de l'auteur — un autre joueur — remplace par 0x1000000000000001, aux deux endroits ou
// il apparait (unk5 et l'identifiant) ; et les six chiffres de microsecondes de
// l'identifiant, que nous ne conservons pas, remplaces par le rang du commentaire (0).
//
// Et UN octet non reproduit, par choix : unk3, que Nintendo met a 1 ici et a 0 ou 1 sur
// les tampons. Son sens n'est pas connu ; 0 est la valeur avec laquelle on a vu nos
// commentaires texte s'afficher. Le test le remplace explicitement, apres avoir verifie
// que la capture dit bien 1.
func TestCommentaireTexteCommeNintendo(t *testing.T) {
	defer isolerCommentaires(t)()
	s := nex.NewSwitchSettings(accessKey, nexVersion)
	quand := time.Date(2025, 6, 15, 22, 5, 38, 0, time.UTC).Unix()
	commentaires.parNiv[0x3483e10] = []commentaire{{
		DataID: 0x3483e10, PID: 0x1000000000000001, Texte: "Awesome! ", Quand: quand, X: 2750, Y: 47,
	}}
	req := &nex.RMCMessage{Settings: s, Mode: nex.RMCRequest, Protocol: 0x73, Method: 95, CallID: 529,
		Body: []byte{0x10, 0x3e, 0x48, 0x03, 0, 0, 0, 0}}
	attendu, _ := hex.DecodeString("8c0000007301110200005f800000010000000079000000103e4803000000002e0032303235303631353232303533383030303030305f313030303030303030303030303030315f333438336531300001010100000000000010be0a2f0000000000000066619fa51f00000000000a00417765736f6d65212000000f000000010000000000000000000000010000000000")
	const posUnk3 = 14 + 4 + 5 + 8 + 2 + 46 // en-tete RMC, liste, structure, unk1, unk2
	if attendu[posUnk3] != 1 {
		t.Fatalf("la capture a change : unk3 = %d", attendu[posUnk3])
	}
	attendu[posUnk3] = 0
	obtenu := smm2SearchComments(&nex.Connection{Settings: s, PID: 7}, req).Encode()
	if !bytes.Equal(obtenu, attendu) {
		t.Fatalf("95 differe de Nintendo :\n obtenu  %x\n attendu %x", obtenu, attendu)
	}
}

// TestCommentaireDessineRattacheLeDessin rejoue la sequence mesuree chez Nintendo
// (capturas-smm2 c446, c450, c453) : 88 prepare le DESSIN, une 132 televerse la miniature
// de signalement (un JPEG), puis 90 confirme avec la clef de la 88. Le commentaire doit
// designer l'objet de la 88 — pas le JPEG, que nous prenions et que le jeu chargeait
// sans fin.
func TestCommentaireDessineRattacheLeDessin(t *testing.T) {
	defer isolerCommentaires(t)()
	s := nex.NewSwitchSettings(accessKey, nexVersion)
	conn := &nex.Connection{Settings: s, PID: 1}

	// 88, parametre de Nintendo tel quel : niveau 0x396e893, dessin de 531 octets.
	p88, _ := hex.DecodeString("001600000093e89603000000000009018000000010e00000000001000c000000130200004000000000000000")
	r88 := smm2PreparePostObjectCommentPicture(conn, &nex.RMCMessage{Settings: s, Mode: nex.RMCRequest, Protocol: 0x73, Method: 88, CallID: 446, Body: p88})
	in := nex.NewStreamIn(r88.Body, s)
	_ = in.U8()
	clef := in.Substream().String()
	var dessin uint64
	if _, err := fmt.Sscanf(clef, "%d", &dessin); err != nil {
		t.Fatalf("88 : clef %q illisible", clef)
	}
	// Le televersement du dessin (des traits zlib), puis celui de la miniature (un JPEG),
	// alloue juste apres, comme le fait la 132.
	os.WriteFile(blobPath(dessin), append([]byte{0x78, 0xda}, make([]byte, 529)...), 0o644)
	jpeg := courses.alloc(1, "rel-0-6", 6, nil, nil, 450)
	os.WriteFile(blobPath(jpeg), append([]byte{0xff, 0xd8}, make([]byte, 448)...), 0o644)

	// 90 : le parametre de Nintendo, avec NOTRE clef a la place de la leur.
	o := nex.NewStreamOut(s)
	o.String(clef)
	o.String("report-thumbnail_exemple")
	o.Write(p88)
	r90 := smm2CompletePostObjectCommentPicture(conn, &nex.RMCMessage{Settings: s, Mode: nex.RMCRequest, Protocol: 0x73, Method: 90, CallID: 453,
		Body: frameStruct(s, 0, o.Bytes())})
	if r90.IsError || len(r90.Body) != 0 {
		t.Fatalf("90 : reponse %+v, attendu un succes vide comme chez Nintendo", r90)
	}

	l := commentaires.parNiv[0x396e893]
	if len(l) != 1 {
		t.Fatalf("%d commentaire(s) sur le niveau, attendu 1", len(l))
	}
	c := l[0]
	if c.Image != dessin || c.TailleImg != 531 {
		t.Fatalf("image=%d (%d octets), attendu le dessin %d (531) — le JPEG est %d", c.Image, c.TailleImg, dessin, jpeg)
	}
	champs, _ := lirePosteCommentaire(func() *nex.StreamIn { i := nex.NewStreamIn(p88, s); _ = i.U8(); return i.Substream() }())
	if c.X != champs.X || c.Y != champs.Y || c.X == 0 {
		t.Fatalf("position (%d,%d), attendu (%d,%d)", c.X, c.Y, champs.X, champs.Y)
	}
	out := nex.NewStreamOut(s)
	ecrireCommentInfo(out, c, 0)
	if !bytes.Contains(out.Bytes(), []byte(fmt.Sprintf("/object/%d\x00", dessin))) {
		t.Fatalf("la fiche ne designe pas le dessin %d : %x", dessin, out.Bytes())
	}
	if u3, u4 := typeCommentaire(c); u3 != 0 || u4 != 0 {
		t.Fatalf("type (%d,%d), attendu (0,0) pour un dessin", u3, u4)
	}

	// Un dessin enregistre AVANT la correction designe le JPEG ; il doit etre servi
	// comme le dessin qui le precede.
	ancien := commentaire{DataID: 0x396e893, PID: 1, Image: dessin + 1, TailleImg: 450}
	courses.byID[dessin+1] = courses.byID[jpeg]
	if id, taille := imageDuCommentaire(ancien); id != dessin || taille != 531 {
		t.Fatalf("ancien dessin : objet %d (%d octets), attendu %d (531)", id, taille, dessin)
	}
	_ = strings.TrimSpace
}
