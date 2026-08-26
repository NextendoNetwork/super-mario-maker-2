package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

// Sonde de forme : comment repondre a une methode dont la structure n'est pas publiee.
//
// LE PROBLEME. Une partie du protocole 0x73 n'a aucune documentation — pour le bloc
// Mario sans fin, seule la 108 est decrite ; 109 a 116 ne sont que des noms dans un
// tableau. Deviner leurs champs ne marche pas : une forme fausse ne produit pas une
// erreur parlante, elle produit « service non disponible », exactement comme une forme
// absente. On ne peut donc pas raisonner, seulement mesurer.
//
// CE QUI A MARCHE. Pour la 115, trois formes ont ete essayees et la troisieme est
// passee : une liste vide DANS une structure. Ce n'etait pas une intuition — l'article
// de TGR (Phrack 72) disait que la methode rend une liste, et l'experience avait deja
// elimine la liste nue. Le renseignement a donne le contenu, la mesure a donne
// l'enveloppe.
//
// POURQUOI CE FICHIER. Chaque essai coute un aller-retour avec une vraie console. Figer
// une supposition dans le binaire obligeait a recompiler et redeployer a chaque fois.
// Ici la forme se choisit dans un fichier, relu a chaque appel : on passe au candidat
// suivant avec un `echo`, sans redemarrer, et le journal dit lequel a servi.
//
//	echo 3 > /opt/smm2/smm2_109.forme
//
// LES CANDIDATS NE SONT PAS DES INVENTIONS DE CHAMPS. Ce sont les enveloppes que ce
// protocole emploie deja ailleurs, avec des valeurs a zero. On cherche la FORME, pas le
// contenu : une fois l'enveloppe trouvee, le contenu se remplira au fur et a mesure.

// formeEssai lit le candidat choisi pour une methode. Absent ou illisible : la valeur
// par defaut fournie par l'appelant.
func formeEssai(methode uint32, defaut int) int {
	b, err := os.ReadFile(fmt.Sprintf("/data/smm2_%d.forme", methode))
	if err != nil {
		return defaut
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return defaut
	}
	return n
}

// candidat rend le corps de reponse d'une forme numerotee, et son nom pour le journal.
//
// La forme 0 est le corps vide ; la 1, une liste vide encapsulee — celle qui a resolu la
// 115, donc le premier candidat a essayer partout ailleurs.
func candidat(s *nex.Settings, forme int) (corps []byte, nom string) {
	env := func(f func(*nex.StreamOut)) []byte {
		o := nex.NewStreamOut(s)
		f(o)
		return frameStruct(s, 0, o.Bytes())
	}
	nu := func(f func(*nex.StreamOut)) []byte {
		o := nex.NewStreamOut(s)
		f(o)
		return o.Bytes()
	}

	switch forme {
	case 1:
		return env(func(o *nex.StreamOut) { o.U32(0) }), "struct{ liste vide }"
	case 2:
		return env(func(o *nex.StreamOut) { o.U32(0); o.U32(0) }), "struct{ deux listes vides }"
	case 3:
		return env(func(o *nex.StreamOut) { o.U64(0) }), "struct{ Uint64 }"
	case 4:
		return env(func(o *nex.StreamOut) { o.U8(0) }), "struct{ Uint8 }"
	case 5:
		return env(func(o *nex.StreamOut) { o.U32(0); o.Bool(false) }), "struct{ liste vide + bool }"
	case 6:
		return nu(func(o *nex.StreamOut) { o.U32(0) }), "liste vide nue"
	case 7:
		return env(func(o *nex.StreamOut) { o.U64(0); o.U32(0); o.U32(0) }), "struct{ Uint64, deux Uint32 }"
	case 8:
		return env(func(o *nex.StreamOut) {}), "struct vide"
	default:
		return nil, "corps vide"
	}
}

// repondreSonde repond a une methode non documentee selon la forme choisie, en le disant
// clairement dans le journal. Le marqueur [SONDE] signale que ce n'est PAS une
// implementation : c'est une question posee a la console.
func repondreSonde(conn *nex.Connection, req *nex.RMCMessage, nom string, defaut int) *nex.RMCMessage {
	s := conn.Settings
	forme, auto := formeSuivante(req.Method, defaut)
	corps, desc := candidat(s, forme)
	noterSonde(req.Method, forme)
	mode := ""
	if auto {
		mode = " [balayage auto]"
	}
	fmt.Printf("[SMM2 Sonde] %s(%d) pid=%d -> forme %d : %s (%do) [SONDE, non documente]%s\n",
		nom, req.Method, conn.PID, forme, desc, len(corps), mode)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, corps)
}

// --- Balayage automatique ------------------------------------------------------------
//
// Essayer les candidats un par un exige, a chaque fois, que quelqu'un rallume la console
// et appuie sur Demarrer. Le fichier de forme peut donc contenir « auto » : la sonde
// avance alors d'un candidat A CHAQUE APPEL. Il suffit d'appuyer sur Demarrer plusieurs
// fois de suite, et le balayage se fait tout seul.
//
// Et surtout, la sonde RETIENT la derniere forme servie. Quand une methode qui ne venait
// jamais arrive enfin — pour Mario sans fin, la 79 ou la 110 —, c'est que la forme
// precedente a ete acceptee, et le journal le dit en clair. Sans cela il faudrait
// recouper deux lignes a la main a chaque essai, et c'est exactement le genre de
// recoupement ou l'on se trompe.

var (
	sondeMu       sync.Mutex
	sondeCompteur = map[uint32]int{}
	sondeDerniere = map[uint32]int{}
)

const sondeFormeMax = 8

// formeSuivante rend le candidat a servir. En mode « auto », il avance a chaque appel.
func formeSuivante(methode uint32, defaut int) (int, bool) {
	b, err := os.ReadFile(fmt.Sprintf("/data/smm2_%d.forme", methode))
	texte := ""
	if err == nil {
		texte = strings.TrimSpace(string(b))
	}
	if texte != "auto" {
		if n, err := strconv.Atoi(texte); err == nil {
			return n, false
		}
		return defaut, false
	}
	sondeMu.Lock()
	defer sondeMu.Unlock()
	f := sondeCompteur[methode]
	sondeCompteur[methode] = (f + 1) % (sondeFormeMax + 1)
	return f, true
}

// noterSonde retient la forme servie pour une methode.
func noterSonde(methode uint32, forme int) {
	sondeMu.Lock()
	sondeDerniere[methode] = forme
	sondeMu.Unlock()
}

// SondeAcceptee signale qu'une methode attendue est enfin arrivee, donc que la forme
// servie juste avant a ete acceptee. A appeler depuis le handler de cette methode-la.
func SondeAcceptee(arrivee uint32, sonde uint32) {
	sondeMu.Lock()
	f, ok := sondeDerniere[sonde]
	sondeMu.Unlock()
	if ok {
		fmt.Printf("[SMM2 Sonde] *** 0x73.%d est arrive : la FORME %d de la methode %d a donc ete ACCEPTEE ***\n",
			arrivee, f, sonde)
	}
}

// drapeauFichier lit un interrupteur depuis un fichier : « 0 » ou « false » desactive,
// tout le reste active, absent = valeur par defaut.
//
// Le meme motif que /opt/nextendo-account/nx_diag.on cote nx-account : un interrupteur
// qui se manoeuvre sans redemarrer le service. Ici il compte parce que le changement
// touche le multijoueur de tous les joueurs connectes.
func drapeauFichier(chemin string, defaut bool) bool {
	b, err := os.ReadFile(chemin)
	if err != nil {
		return defaut
	}
	switch strings.TrimSpace(string(b)) {
	case "0", "false", "non", "no":
		return false
	case "":
		return defaut
	}
	return true
}
