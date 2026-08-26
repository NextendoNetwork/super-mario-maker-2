package main

import (
	"fmt"
	"math/big"
	"math/bits"
	"strings"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

// La recherche par code : « GetUserOrCourse » (methode 131).
//
// C'est ainsi que les joueurs se partagent des niveaux — on tape le code affiche a
// l'ecran et on tombe dessus. Documentee, et la requete est en plus MESUREE sur une
// vraie console, ce qui est confortable pour une fois :
//
//	00 14000000            en-tete de structure, 20 octets
//	0a00 "941000000\\0"     String : le code tape (9 caracteres)
//	ffe30000               Uint32 : userResultOption   (58367)
//	ff010000               Uint32 : courseResultOption (511)
//
// Les deux masques tombent exactement ou la documentation les annonce, ce qui confirme
// la lecture. Reponse : un UserInfo puis un CourseInfo.
//
// POURQUOI LE TRAJET INVERSE EST SUR. On pourrait craindre que notre encodeur ne soit
// pas celui de Nintendo, et que le code tape par le joueur ne corresponde donc a rien.
// Il n'en est rien, et pour une raison structurelle : le code affiche a l'ecran, c'est
// NOUS qui le fournissons — ecrireCourseInfo ecrit codeNiveau(dataID) dans la fiche. Le
// jeu ne le calcule pas, il le recopie. La correspondance est donc garantie par
// construction, quel que soit l'encodage retenu par Nintendo de leur cote.
//
// Un indice le confirme dans le journal : les codes cherches par de vrais joueurs
// (« 941000000 », « V31000000 ») se terminent tous par six zeros. Un code Nintendo
// authentique, reparti sur 30^9 valeurs, aurait des chiffres de poids fort non nuls.
// Ceux-la viennent de data_id petits — c'est-a-dire des notres.

const alphabetCode = "0123456789BCDFGHJKLMNPQRSTVWXY"

// dataIDDepuisCode fait l'inverse de codeNiveau. Les tirets et la casse sont tolerants :
// les joueurs recopient « B21-000-000 » avec ses tirets, et le refuser pour cela serait
// gratuit.
func dataIDDepuisCode(code string) (uint64, bool) {
	propre := strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(code))
	if len(propre) != 9 {
		return 0, false
	}
	var v uint64
	// codeNiveau ecrit le chiffre le moins significatif en premier : on remonte donc
	// depuis la fin.
	for i := len(propre) - 1; i >= 0; i-- {
		n := strings.IndexByte(alphabetCode, propre[i])
		if n < 0 {
			return 0, false
		}
		v = v*uint64(len(alphabetCode)) + uint64(n)
	}
	return v, true
}

// smm2GetUserOrCourse (131) : chercher un niveau ou un createur par son code.
func smm2GetUserOrCourse(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings

	in := nex.NewStreamIn(req.Body, s)
	p := in
	if s.StructHeader {
		_ = in.U8()
		p = in.Substream()
	}
	code := p.String()
	optUser := p.U32()
	optCourse := p.U32()
	if err := p.Err(); err != nil {
		fmt.Printf("[SMM2 Code] get_user_or_course(131) : parametre illisible (%v)\n", err)
		return nex.NewRMCError(s, 0x73, req.CallID, 0x00690002)
	}

	dataID, ok := resoudreCode(code)
	if !ok {
		fmt.Printf("[SMM2 Code] get_user_or_course(131) code=%q illisible\n", code)
		return nex.NewRMCError(s, 0x73, req.CallID, 0x80690004) // DataStore::NotFound
	}

	m := courses.get(dataID)
	if m == nil || !m.Ready || estFichierRattache(m.Name) {
		// La grande majorite des codes cherches sont de VRAIS codes Nintendo, tapes par
		// des joueurs qui les ont vus ailleurs : ils n'existent pas chez nous et le bon
		// comportement est de le dire, pas d'inventer un niveau.
		fmt.Printf("[SMM2 Code] get_user_or_course(131) code=%q -> data_id=%d introuvable\n", code, dataID)
		return nex.NewRMCError(s, 0x73, req.CallID, 0x80690004)
	}

	profil, trouve := nex.SMM2ProfilDe(m.OwnerPID)
	if !trouve {
		// Le niveau existe mais son auteur n'a plus de profil : on refuse plutot que de
		// rendre un UserInfo vide, que le client lit comme une fiche valide.
		fmt.Printf("[SMM2 Code] get_user_or_course(131) data_id=%d : profil %d absent\n", dataID, m.OwnerPID)
		return nex.NewRMCError(s, 0x73, req.CallID, 0x80690004)
	}

	// « USER OR COURSE » : le OU compte. Le premier essai remplissait les DEUX fiches,
	// et la console a affiche le CREATEUR, pas le niveau. Elle n'a pas refuse la reponse
	// — si le CourseInfo avait ete mal forme, tout l'appel aurait echoue et l'ecran
	// n'aurait rien montre du tout. Elle a donc lu les deux fiches et CHOISI celle de
	// l'utilisateur : quand les deux sont valides, l'utilisateur gagne.
	//
	// Nos codes designent toujours un niveau — ils encodent un data_id et rien d'autre,
	// contrairement a ceux de Nintendo qui portent un bit de type. On rend donc un
	// UserInfo VIDE, dont le PID a zero dit « ce code n'est pas celui d'un createur ».
	//
	// Le mode reste commutable sans redeploiement, le temps de confirmer :
	//   echo 1 > /opt/smm2/smm2_131.mode   -> les deux fiches (comportement precedent)
	mode := formeEssai(131, 0)
	out := nex.NewStreamOut(s)
	if mode == 1 {
		nex.EcrireUserInfo(out, profil)
	} else {
		nex.EcrireUserInfo(out, nex.SMM2Profil{})
	}
	ecrireCourseInfo(out, m, optCourse)

	quoi := "UserInfo vide + le niveau"
	if mode == 1 {
		quoi = "les deux fiches"
	}
	fmt.Printf("[SMM2 Code] get_user_or_course(131) code=%q -> data_id=%d %q de pid=%d (%s, optUser=0x%x optCourse=0x%x)\n",
		code, dataID, m.Name, m.OwnerPID, quoi, optUser, optCourse)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}

// --- Melange des codes de niveau ---------------------------------------------------
//
// LE PROBLEME. codeNiveau ecrivait le data_id en base 30 tel quel, chiffre de poids
// faible en tete. Nos identifiants etant petits (1000, 1016…), les six chiffres de poids
// fort restaient a zero et tous les codes ressemblaient a « B31-000-000 ». Deux
// consequences : ils se voient comme faux, et surtout ils sont ENUMERABLES — on essaie
// B31, C31, D31 et on parcourt le catalogue entier sans passer par la recherche.
//
// LA SOLUTION. Multiplier par une constante premiere avec 30^9 avant d'ecrire les
// chiffres. C'est une bijection : chaque data_id donne un code distinct, et l'operation
// s'inverse exactement. Le code occupe alors tout l'espace des 19 683 milliards de
// valeurs, comme ceux de Nintendo.
//
// LA COMPATIBILITE, QUI EST LE VRAI SUJET. Des codes de l'ancienne forme circulent deja :
// l'annonce est publiee. Changer l'encodage les tuerait. Le decodeur essaie donc les
// DEUX : d'abord la nouvelle forme, et si elle ne designe aucun niveau connu, l'ancienne.
// Rien de ce qui a ete partage ne cesse de fonctionner, et les codes emis desormais ont
// la bonne tete. Cela ne coute qu'une recherche de plus dans le cas rare.

const moduleCode uint64 = 19683000000000 // 30^9

// melangeCode : impair, non divisible par 3 ni par 5, donc premier avec 30^9. Il est
// GRAND a dessein : un petit multiplicateur laisserait les chiffres de poids fort a zero
// pour de petits identifiants, ce qui ne corrigerait rien.
const melangeCode uint64 = 7777777777777

// melangeCodeInv : l'inverse modulaire, calcule une fois au demarrage.
var melangeCodeInv uint64

func init() {
	m := new(big.Int).SetUint64(moduleCode)
	k := new(big.Int).SetUint64(melangeCode)
	inv := new(big.Int).ModInverse(k, m)
	if inv == nil {
		// Impossible avec une constante premiere avec le module — mais si quelqu'un
		// change melangeCode sans verifier, mieux vaut le savoir au demarrage que de
		// distribuer des codes irreversibles.
		panic("smm2: melangeCode n'est pas premier avec 30^9")
	}
	melangeCodeInv = inv.Uint64()
}

// mulMod multiplie modulo moduleCode en 128 bits.
//
// Un produit de deux valeurs proches de 10^13 depasse largement un uint64 ; le faire
// naivement donnerait des codes qui ne se decodent pas, et on ne s'en apercevrait qu'en
// production, sur un code partage qui ne mene nulle part.
func mulMod(a, b uint64) uint64 {
	hi, lo := bits.Mul64(a, b)
	_, r := bits.Div64(hi%moduleCode, lo, moduleCode)
	return r
}

// codeMelange rend le code affiche pour un data_id.
func codeMelange(dataID uint64) string {
	v := mulMod(dataID%moduleCode, melangeCode)
	code := make([]byte, 9)
	for i := range code {
		code[i] = alphabetCode[v%30]
		v /= 30
	}
	return string(code)
}

// dataIDDepuisCodeMelange fait le trajet inverse.
func dataIDDepuisCodeMelange(code string) (uint64, bool) {
	v, ok := valeurDepuisCode(code)
	if !ok {
		return 0, false
	}
	return mulMod(v, melangeCodeInv), true
}

// valeurDepuisCode lit les neuf caracteres en un nombre, sans demelanger.
func valeurDepuisCode(code string) (uint64, bool) {
	propre := strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(code))
	if len(propre) != 9 {
		return 0, false
	}
	var v uint64
	for i := len(propre) - 1; i >= 0; i-- {
		n := strings.IndexByte(alphabetCode, propre[i])
		if n < 0 {
			return 0, false
		}
		v = v*30 + uint64(n)
	}
	return v, true
}

// resoudreCode rend le data_id designe par un code, ancienne ou nouvelle forme.
//
// La nouvelle d'abord. On ne retient une reponse que si elle designe un niveau qui
// EXISTE : sans cette verification, la premiere forme essayee rendrait toujours un
// nombre et l'ancienne ne serait jamais consultee.
func resoudreCode(code string) (uint64, bool) {
	if id, ok := dataIDDepuisCodeMelange(code); ok && courses.get(id) != nil {
		return id, true
	}
	if id, ok := dataIDDepuisCode(code); ok && courses.get(id) != nil {
		return id, true
	}
	// Aucun des deux ne designe un niveau connu : on rend la lecture nouvelle forme,
	// pour que l'appelant reponde « introuvable » avec un identifiant coherent.
	return dataIDDepuisCodeMelange(code)
}
