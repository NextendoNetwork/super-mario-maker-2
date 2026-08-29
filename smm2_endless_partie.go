package main

// Le DEROULEMENT d'une partie sans fin : methodes 110 a 114.
//
// CE QU'ELLES DEBLOQUENT. Le mode sans fin figurait comme « bloque » depuis le debut, et
// huit tentatives n'en etaient pas venues a bout. Ce n'etait pas une seule panne mais une
// PILE : les reglages entiers vides arretaient le jeu avant la liste, la fiche de niveau
// tronquee l'arretait a la liste, et ces cinq methodes-ci l'arretent au lancement du
// niveau. Chaque correction ne rend visible que le mur suivant.
//
// La derniere se lisait ainsi dans le journal :
//
//	0x73.110 corps brut len=14: 00 09000000 00 750a000000000000
//	UNCAPTURED 0x73.110 -> empty-list fallback
//
// Soit : version 0, longueur 9, difficulte 0, niveau 2677. Nous repondions quatre octets a
// zero la ou le jeu attend une structure {vies, reussites}. D'ou « erreur de connexion ».
//
// Noms et formes releves dans l'implementation de reference ; le code est ecrit ici.

import (
	"fmt"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

// etatManche : la reponse commune aux methodes 110 et 111 — EndlessModeRunState.
func ecrireEtatManche(out *nex.StreamOut, vies uint8, reussites uint32) {
	s := out.Settings
	f := nex.NewStreamOut(s)
	f.U8(vies)
	f.U32(reussites)
	if s.StructHeader {
		out.U8(0)
		out.Buffer(f.Bytes())
		return
	}
	out.Write(f.Bytes())
}

// 110 StartEndlessModeCourse : le joueur lance un niveau de la reserve.
//
// C'EST ICI QU'ON COMPTE LES MORTS. Le jeu n'annonce jamais « je suis mort » : il relance
// simplement le MEME niveau. Recevoir deux fois de suite le meme identifiant est donc le
// seul signal disponible, et c'est celui que retient l'implementation de reference.
func smm2StartEndlessModeCourse(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	in := nex.NewStreamIn(req.Body, s)
	_ = in.U8()
	p := in.Substream()
	difficulte := p.U8()
	cours := p.U64()
	if err := p.Err(); err != nil {
		fmt.Printf("[SMM2 Endless] start(110) : parametre illisible (%v) brut=%x\n", err, req.Body)
		return nex.NewRMCError(s, 0x73, req.CallID, 0x00690002)
	}

	vies, reussites, mort := endless.demarrerCours(conn.PID, difficulte, cours)
	out := nex.NewStreamOut(s)
	ecrireEtatManche(out, vies, reussites)

	quoi := "nouveau niveau"
	if mort {
		quoi = "MEME niveau -> une vie en moins"
	}
	fmt.Printf("[SMM2 Endless] start(110) pid=%d difficulte=%d niveau=%d : %s -> %d vie(s), %d reussite(s)\n",
		conn.PID, difficulte, cours, quoi, vies, reussites)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}

// 111 DominateEndlessModeCourse : le niveau est termine.
func smm2DominateEndlessModeCourse(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	in := nex.NewStreamIn(req.Body, s)
	_ = in.U8()
	p := in.Substream()
	difficulte := p.U8()
	cours := p.U64()
	viesGagnees := p.U8()
	pieces := p.U8()
	points := p.U32()
	unk8 := p.U8()
	unk9 := p.U8()
	if err := p.Err(); err != nil {
		fmt.Printf("[SMM2 Endless] dominate(111) : parametre illisible (%v) brut=%x\n", err, req.Body)
		return nex.NewRMCError(s, 0x73, req.CallID, 0x00690002)
	}

	vies, reussites := endless.reussirCours(conn.PID, difficulte, viesGagnees, pieces, points)
	out := nex.NewStreamOut(s)
	ecrireEtatManche(out, vies, reussites)

	fmt.Printf("[SMM2 Endless] dominate(111) pid=%d difficulte=%d niveau=%d +%d vie(s) pieces=%d points=%d unk8=%d unk9=%d -> %d vie(s), %d reussite(s)\n",
		conn.PID, difficulte, cours, viesGagnees, pieces, points, unk8, unk9, vies, reussites)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}

// 112 PassEndlessModeCourse : le joueur PASSE le niveau sans le finir. Aucune reponse.
func smm2PassEndlessModeCourse(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	in := nex.NewStreamIn(req.Body, s)
	_ = in.U8()
	p := in.Substream()
	difficulte := p.U8()
	cours := p.U64()
	fmt.Printf("[SMM2 Endless] pass(112) pid=%d difficulte=%d niveau=%d\n", conn.PID, difficulte, cours)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, nil)
}

// 113 SuspendEndlessModeCourse : la partie est mise en pause. Aucune reponse.
//
// On NE remet PAS la partie a zero ici : suspendre n'est pas abandonner, et le joueur doit
// retrouver ses vies en revenant.
func smm2SuspendEndlessModeCourse(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	in := nex.NewStreamIn(req.Body, s)
	_ = in.U8()
	p := in.Substream()
	difficulte := p.U8()
	fmt.Printf("[SMM2 Endless] suspend(113) pid=%d difficulte=%d\n", conn.PID, difficulte)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, nil)
}

// 114 FinishEndlessModeCourse : la partie est TERMINEE — plus de vies. Aucune reponse.
func smm2FinishEndlessModeCourse(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	in := nex.NewStreamIn(req.Body, s)
	_ = in.U8()
	p := in.Substream()
	difficulte := p.U8()
	vies, reussites := endless.terminer(conn.PID, difficulte)
	fmt.Printf("[SMM2 Endless] finish(114) pid=%d difficulte=%d : partie close a %d reussite(s) (vies=%d)\n",
		conn.PID, difficulte, reussites, vies)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, nil)
}

// 104 PostRankingInfo : le jeu declare un resultat de classement pour un niveau.
//
// Onze octets : le data_id du niveau puis trois Uint8 dont le sens n'est pas documente. On
// les journalise sans les interpreter — les inventer serait pire que de les ignorer — et on
// repond un corps VIDE, qui est ce que la methode rend.
func smm2PostRankingInfo(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	in := nex.NewStreamIn(req.Body, s)
	_ = in.U8()
	p := in.Substream()
	cours := p.U64()
	a, b, c := p.U8(), p.U8(), p.U8()
	if err := p.Err(); err != nil {
		fmt.Printf("[SMM2 Courses] post_ranking_info(104) : parametre illisible (%v) brut=%x\n", err, req.Body)
		return nex.NewRMCError(s, 0x73, req.CallID, 0x00690002)
	}
	fmt.Printf("[SMM2 Courses] post_ranking_info(104) pid=%d niveau=%d unk=[%d %d %d]\n", conn.PID, cours, a, b, c)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, nil)
}
