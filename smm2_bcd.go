package main

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
	"unicode/utf16"
)

// Lecture de l'en-tete d'un niveau : style, theme, minuterie, condition de victoire.
//
// POURQUOI. Ces champs etaient ecrits a ZERO dans CourseInfo, avec le commentaire « zero
// est une valeur valide, le jeu affichera les defauts ». C'etait faux, et le joueur l'a
// vu avant nous : le style 0 est SMB1, donc TOUS les niveaux s'annoncaient en Super
// Mario Bros. Le jeu charge le vrai style depuis les donnees du niveau, mais la fiche
// annonce autre chose — d'ou un niveau SM3DW presente avec le Mario d'origine. Le
// niveau 1000 est en realite du SMB3, et le 1016 du SM3DW.
//
// Zero n'etait donc pas « une valeur par defaut inoffensive » : c'etait une affirmation
// fausse. Il y a une difference entre ne pas savoir et pretendre savoir, et j'avais mis
// la deuxieme dans un commentaire en la faisant passer pour la premiere.
//
// L'ALGORITHME N'EST PAS DEVINE. Le corps du fichier est chiffre en AES-CBC ; seuls les
// seize premiers octets sont en clair. La table de derivation et le generateur
// pseudo-aleatoire de Nintendo (sead) viennent de mm2srv/smm2_parsing, le materiel
// publie par TGR. Et surtout, le format porte un CRC32 du texte clair : si notre
// dechiffrement etait faux, il ne coincideraient pas. On ne se contente pas d'un
// resultat vraisemblable, on le verifie.
//
// Le CMAC, lui, n'est PAS verifie : cela demanderait une dependance externe pour un gain
// nul, le CRC32 sur 376 Ko etablissant deja que le dechiffrement est correct. C'est un
// choix, pas un oubli.

var bcdTable = []uint32{
	0x7ab1c9d2, 0xca750936, 0x3003e59c, 0xf261014b, 0x2e25160a, 0xed614811,
	0xf1ac6240, 0xd59272cd, 0xf38549bf, 0x6cf5b327, 0xda4db82a, 0x820c435a,
	0xc95609ba, 0x19be08b0, 0x738e2b81, 0xed3c349a, 0x045275d1, 0xe0a73635,
	0x1debf4da, 0x9924b0de, 0x6a1fc367, 0x71970467, 0xfc55abeb, 0x368d7489,
	0x0cc97d1d, 0x17cc441e, 0x3528d152, 0xd0129b53, 0xe12a69e9, 0x13d1bdb7,
	0x32eaa9ed, 0x42f41d1b, 0xaea5f51f, 0x42c5d23c, 0x7cc742ed, 0x723ba5f9,
	0xde5b99e3, 0x2c0055a4, 0xc38807b4, 0x4c099b61, 0xc4e4568e, 0x8c29c901,
	0xe13b34ac, 0xe7c3f212, 0xb67ef941, 0x08038965, 0x8afd1e6a, 0x8e5341a3,
	0xa4c61107, 0xfbaf1418, 0x9b05ef64, 0x3c91734e, 0x82ec6646, 0xfb19f33e,
	0x3bde6fe2, 0x17a84cca, 0xccdf0ce9, 0x50e4135c, 0xff2658b2, 0x3780f156,
	0x7d8f5d68, 0x517cbed1, 0x1fcddf0d, 0x77a58c94,
}

const tailleBCD = 0x5C000

// hasardSead : le generateur de Nintendo. Quatre mots d'etat, pris dans le pied du
// fichier ; c'est lui qui rend la clef reproductible sans qu'elle soit stockee.
type hasardSead struct{ s [4]uint32 }

func (r *hasardSead) u32() uint32 {
	t := r.s[0]
	t ^= t << 11
	t ^= t >> 8
	t ^= r.s[3]
	t ^= r.s[3] >> 19
	r.s[0], r.s[1], r.s[2], r.s[3] = r.s[1], r.s[2], r.s[3], t
	return t
}

func (r *hasardSead) borne(max uint32) uint32 {
	return uint32((uint64(r.u32()) * uint64(max)) >> 32)
}

func clefBCD(r *hasardSead) []byte {
	clef := make([]byte, 0x10)
	for i := 0; i < 4; i++ {
		var v uint32
		for e := 0; e < 4; e++ {
			idx := r.borne(uint32(len(bcdTable)))
			dec := r.borne(4) * 8
			v = (v << 8) | ((bcdTable[idx] >> dec) & 0xFF)
		}
		binary.LittleEndian.PutUint32(clef[i*4:], v)
	}
	return clef
}

// dechiffrerNiveau rend le texte clair du niveau, ou une erreur franche.
func dechiffrerNiveau(buf []byte) ([]byte, error) {
	if len(buf) != tailleBCD {
		return nil, fmt.Errorf("taille %d, attendu %d", len(buf), tailleBCD)
	}
	fin := 0x5BFD0
	r := &hasardSead{}
	for i := 0; i < 4; i++ {
		r.s[i] = binary.LittleEndian.Uint32(buf[fin+0x10+i*4:])
	}
	bloc, err := aes.NewCipher(clefBCD(r))
	if err != nil {
		return nil, err
	}
	clair := make([]byte, 0x5BFC0)
	cipher.NewCBCDecrypter(bloc, buf[fin:fin+0x10]).CryptBlocks(clair, buf[0x10:fin])

	if crc32.ChecksumIEEE(clair) != binary.LittleEndian.Uint32(buf[8:12]) {
		return nil, fmt.Errorf("CRC invalide : le dechiffrement a echoue")
	}
	return clair, nil
}

// enteteNiveau : les champs de l'en-tete qui alimentent CourseInfo.
type enteteNiveau struct {
	Style       uint8 // 0 SMB1, 1 SMB3, 2 SMW, 3 NSMBU, 4 SM3DW
	Theme       uint8 // 0-9, l'enum du protocole correspond directement
	Minuterie   uint16
	Condition   uint32
	CondCat     uint8
	CondAmpleur uint16
	Nom         string
	Description string

	// L'auteur doit terminer son niveau avant de pouvoir le publier, et le fichier
	// retient combien d'essais cela lui a pris. C'est la seule mesure de difficulte
	// disponible AU MOMENT de la publication, avant que quiconque d'autre y ait joue —
	// et c'est celle que Nintendo possede aussi.
	EssaisAuteur uint32
	TempsAuteur  uint32
}

// styleDepuisCode traduit les deux caracteres ASCII du fichier vers l'index du protocole.
func styleDepuisCode(code uint16) (uint8, bool) {
	switch code {
	case 12621: // "M1"
		return 0, true
	case 13133: // "M3"
		return 1, true
	case 22349: // "MW"
		return 2, true
	case 21847: // "WU"
		return 3, true
	case 22323: // "3W"
		return 4, true
	}
	return 0, false
}

func texteUTF16(b []byte) string {
	mots := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		m := binary.LittleEndian.Uint16(b[i:])
		if m == 0 {
			break
		}
		mots = append(mots, m)
	}
	return string(utf16.Decode(mots))
}

func lireEntete(clair []byte) (*enteteNiveau, error) {
	if len(clair) < 0x202 {
		return nil, fmt.Errorf("texte clair trop court (%d)", len(clair))
	}
	code := binary.LittleEndian.Uint16(clair[0xF1:])
	style, ok := styleDepuisCode(code)
	if !ok {
		// Un style inconnu signale une lecture fausse, pas un niveau exotique : les cinq
		// valeurs sont exhaustives. On refuse plutot que d'ecrire SMB1 par defaut, qui
		// est exactement le mensonge qu'on est en train de corriger.
		return nil, fmt.Errorf("style inconnu 0x%04x a l'offset 0xF1", code)
	}
	return &enteteNiveau{
		Style:        style,
		Theme:        clair[0x200],
		Minuterie:    binary.LittleEndian.Uint16(clair[4:]),
		Condition:    binary.LittleEndian.Uint32(clair[16:]),
		CondCat:      clair[15],
		CondAmpleur:  binary.LittleEndian.Uint16(clair[6:]),
		Nom:          texteUTF16(clair[0xF4 : 0xF4+0x42]),
		Description:  texteUTF16(clair[0x136 : 0x136+0xCA]),
		EssaisAuteur: binary.LittleEndian.Uint32(clair[28:]),
		TempsAuteur:  binary.LittleEndian.Uint32(clair[32:]),
	}, nil
}

// enteteDuBlob lit l'en-tete du niveau stocke sous ce data_id.
func enteteDuBlob(dataID uint64) (*enteteNiveau, error) {
	buf, err := os.ReadFile(blobPath(dataID))
	if err != nil {
		return nil, err
	}
	clair, err := dechiffrerNiveau(buf)
	if err != nil {
		return nil, err
	}
	return lireEntete(clair)
}
