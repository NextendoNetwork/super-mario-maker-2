package main

// Le drapeau d'un joueur dans SMM2.
//
// MESURE le 2026-10-06 : sur une semaine, 52 joueurs dont on connait le vrai pays (autre que
// la France) se sont enregistres dans SMM2 comme « FR ». La console declare son pays a
// l'enregistrement (methode 47) et le tire de la fiche de compte Nintendo que sert nx-account,
// ou il est ecrit en dur « FR ». Tout le monde etait donc francais.
//
// Le pays existe deja ailleurs : le joueur le choisit sur le site (nextendo-account, champ
// Country, fait pour les drapeaux de MK8). On le lit par /internal/identity et on l'AFFICHE a
// la place de celui que la console a declare. Le profil stocke n'est pas modifie : si le
// joueur n'a rien choisi, on montre ce que sa console a dit, comme avant.
//
// L'affichage ne bloque jamais sur le service de comptes : on rend ce qu'on sait et on
// rafraichit en arriere-plan. Un changement fait sur le site apparait donc au plus tard
// paysTTL apres, au prochain affichage du profil.

import (
	"strings"
	"sync"
	"time"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

const paysTTL = 10 * time.Minute

type entreePays struct {
	code string
	at   time.Time
}

type cachePays struct {
	mu      sync.Mutex
	m       map[uint64]entreePays
	enCours map[uint64]bool
	// lire interroge le service de comptes ; injectable pour les tests.
	lire func(pid uint64)
}

var paysComptes = &cachePays{m: map[uint64]entreePays{}, enCours: map[uint64]bool{}}

// retenir memorise le pays lu pour ce compte (vide = le joueur n'en a pas choisi).
func (c *cachePays) retenir(pid uint64, code string) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if len(code) != 2 {
		code = ""
	}
	c.mu.Lock()
	c.m[pid] = entreePays{code: code, at: time.Now()}
	delete(c.enCours, pid)
	c.mu.Unlock()
}

// afficher rend le pays choisi sur le site, ou "" (l'appelant garde alors celui declare).
func (c *cachePays) afficher(pid uint64, _ string) string {
	if pid < premierPIDCompte {
		return ""
	}
	c.mu.Lock()
	e, connu := c.m[pid]
	perime := !connu || time.Since(e.at) > paysTTL
	lancer := perime && !c.enCours[pid] && c.lire != nil
	if lancer {
		c.enCours[pid] = true
	}
	c.mu.Unlock()
	if lancer {
		go func() {
			c.lire(pid)
			c.mu.Lock()
			delete(c.enCours, pid) // meme si la lecture a echoue : on reessaiera plus tard
			c.mu.Unlock()
		}()
	}
	return e.code
}

// installerPays branche le drapeau du site dans les profils SMM2.
func installerPays() {
	paysComptes.lire = func(pid uint64) { lireIdentiteCompte(pid) }
	nex.SMM2PaysFn = paysComptes.afficher
}
