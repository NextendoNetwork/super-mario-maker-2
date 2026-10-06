package main

// Identite publique des joueurs : la console doit voir son NSA, pas notre PID interne.
//
// LE PROBLEME, mesure le 2026-10-05 avec deux consoles. Dans une salle d'amis, l'hote
// apparaissait INVISIBLE sur les deux ecrans et ne pouvait pas fixer les regles. La console
// embarque dans l'ApplicationData de la session l'identifiant de son compte (le NSA, 64 bits)
// et le compare au proprietaire et a son propre PID de connexion. Chez Nintendo ces nombres
// sont LE MEME (capture : PID d'Auth = OwnerPID = PIDSource de la 3001 = l'identifiant de
// l'ApplicationData = PID=... des StationURL). Chez nous l'Auth donnait 1800xxxxxx.
//
// Presenter le NSA seulement dans OwnerPID/HostPID a ete essaye (06030fa) et a casse la
// creation : la console compare le proprietaire a son PID de CONNEXION. Il faut donc que
// TOUT ce qui sort vers la console parle le NSA, a commencer par la reponse d'Auth.
//
// LA REGLE : a l'interieur du serveur rien ne change. Connexions, sessions, catalogue,
// profils, notes, records restent sur le PID interne. La traduction se fait a la frontiere,
// dans nextendo-nex (StreamOut.PID / StreamIn.PID, StationURL, Param2 des notifications),
// par les deux fonctions de ce fichier branchees dans Settings.
//
// D'OU VIENT LA CORRESPONDANCE. Jamais devinee (AGENTS.md) :
//   - a l'Auth, la console se nomme par son NSA et le service de comptes rend le PID ;
//   - NSA -> PID : /api/nsa (resolveNSAtoPID, deja utilise par pidJoueur) ;
//   - PID -> NSA : /internal/identity?pid= (baasUserID), la meme valeur que /api/nsa compare.
// La table est gardee sur disque : un createur hors ligne doit sortir sous la meme identite.
//
// SORTIE NON BLOQUANTE. Une liste de cent niveaux ne doit pas attendre cent appels HTTP :
// un PID dont on ne connait pas encore le NSA sort tel quel (le comportement d'avant) et la
// recherche part en arriere-plan. Le joueur LUI-MEME est toujours connu, depuis son Auth.
//
// Interrupteur sans redeploiement : creer <storageDir>/smm2_identite.off puis redemarrer.

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

// premierPIDCompte : nos PID de compte commencent ici et tiennent dans 32 bits. En dessous,
// ce sont des PID de service (le serveur securise, 2) : on n'y touche pas.
const premierPIDCompte = 1_800_000_000

type tableIdentites struct {
	mu      sync.Mutex
	NSA     map[uint64]uint64 `json:"nsa"` // PID interne -> NSA
	pid     map[uint64]uint64 // NSA -> PID interne (reconstruit au chargement)
	chemin  string
	sale    bool
	enCours map[uint64]bool
	// chercher rend le NSA d'un PID aupres du service de comptes. Injectable pour les tests.
	chercher func(pid uint64) (uint64, bool)
}

var identites = &tableIdentites{
	NSA:     map[uint64]uint64{},
	pid:     map[uint64]uint64{},
	enCours: map[uint64]bool{},
}

// identitesRecherches plafonne les appels /internal/identity simultanes.
var identitesRecherches = make(chan struct{}, 4)

func (t *tableIdentites) charger(dir string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.chemin = filepath.Join(dir, "smm2_identites.json")
	if b, err := os.ReadFile(t.chemin); err == nil {
		var c struct {
			NSA map[uint64]uint64 `json:"nsa"`
		}
		if json.Unmarshal(b, &c) == nil && c.NSA != nil {
			for p, n := range c.NSA {
				t.lierLocked(p, n)
			}
		}
	}
	fmt.Printf("[SMM2 Identite] %d correspondance(s) PID <-> NSA chargee(s)\n", len(t.NSA))
}

// lierLocked enregistre la paire dans les deux sens. Un NSA ne designe qu'un PID : si le
// service de comptes l'a reattribue, l'ancienne paire disparait.
func (t *tableIdentites) lierLocked(pid, nsa uint64) {
	if pid < premierPIDCompte || pid > math.MaxUint32 || nsa <= math.MaxUint32 {
		return
	}
	if ancien, ok := t.pid[nsa]; ok && ancien != pid {
		delete(t.NSA, ancien)
	}
	if ancien, ok := t.NSA[pid]; ok && ancien != nsa {
		delete(t.pid, ancien)
	}
	if t.NSA[pid] != nsa {
		t.NSA[pid] = nsa
		t.pid[nsa] = pid
		t.sale = true
	}
}

func (t *tableIdentites) lier(pid, nsa uint64) {
	t.mu.Lock()
	t.lierLocked(pid, nsa)
	t.mu.Unlock()
}

// sauver ecrit la table si elle a change. Appele par une boucle, jamais sous un autre verrou.
func (t *tableIdentites) sauver() {
	t.mu.Lock()
	if !t.sale || t.chemin == "" {
		t.mu.Unlock()
		return
	}
	b, err := json.Marshal(struct {
		NSA map[uint64]uint64 `json:"nsa"`
	}{t.NSA})
	t.sale = false
	chemin := t.chemin
	t.mu.Unlock()
	if err == nil && os.WriteFile(chemin+".tmp", b, 0o600) == nil {
		os.Rename(chemin+".tmp", chemin)
	}
}

// public rend l'identifiant que la console doit voir pour ce PID. Ne bloque jamais.
func (t *tableIdentites) public(pid uint64) uint64 {
	if pid < premierPIDCompte || pid > math.MaxUint32 {
		return pid // deja public, ou PID de service
	}
	t.mu.Lock()
	if nsa, ok := t.NSA[pid]; ok {
		t.mu.Unlock()
		return nsa
	}
	t.mu.Unlock()

	// Le joueur qui vient de s'authentifier : son NSA est le nom qu'il a donne a l'Auth.
	if nom, ok := nex.LoginNameForPID(pid); ok {
		if nsa, err := strconv.ParseUint(nom, 10, 64); err == nil && nsa > math.MaxUint32 {
			t.lier(pid, nsa)
			return nsa
		}
	}
	t.rechercherPlusTard(pid)
	return pid
}

// interne rend notre PID pour un identifiant venu de la console.
func (t *tableIdentites) interne(id uint64) uint64 {
	if id <= math.MaxUint32 {
		return id
	}
	t.mu.Lock()
	if p, ok := t.pid[id]; ok {
		t.mu.Unlock()
		return p
	}
	t.mu.Unlock()
	p := pidJoueur(id) // /api/nsa, avec son cache ; inchange si inconnu
	if p != id {
		t.lier(p, id)
	}
	return p
}

func (t *tableIdentites) rechercherPlusTard(pid uint64) {
	t.mu.Lock()
	if t.enCours[pid] || t.chercher == nil {
		t.mu.Unlock()
		return
	}
	t.enCours[pid] = true
	t.mu.Unlock()
	go func() {
		defer func() {
			t.mu.Lock()
			delete(t.enCours, pid)
			t.mu.Unlock()
		}()
		if nsa, ok := t.chercher(pid); ok {
			t.lier(pid, nsa)
		}
	}()
}

// chercherNSA interroge /internal/identity. La reponse porte aussi l'e-mail du compte :
// on n'en lit que baasUserID et on ne journalise rien d'autre.
func chercherNSA(pid uint64) (uint64, bool) {
	nsa, ok, _ := chercherNSAEtat(pid)
	return nsa, ok
}

// chercherNSAEtat distingue « pas de reponse utile » de « plafond atteint » : seul le second
// merite qu'on reessaie.
func chercherNSAEtat(pid uint64) (nsa uint64, ok bool, sature bool) {
	baas, _, trouve, sature := lireIdentiteCompte(pid)
	if !trouve {
		return 0, false, sature
	}
	nsa, err := strconv.ParseUint(baas, 16, 64)
	if err != nil || nsa <= math.MaxUint32 {
		return 0, false, false
	}
	return nsa, true, false
}

// lireIdentiteCompte interroge /internal/identity. La reponse porte aussi l'e-mail et la
// liste d'amis du compte : on n'en lit que l'identifiant BAAS et le pays, et on ne
// journalise rien. Le pays est memorise au passage (voir smm2_pays.go).
func lireIdentiteCompte(pid uint64) (baas, pays string, trouve, sature bool) {
	select {
	case identitesRecherches <- struct{}{}:
		defer func() { <-identitesRecherches }()
	default:
		return "", "", false, true // sature : on reessaiera au prochain affichage
	}
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/internal/identity?pid=%d", accountBaseURL, pid), nil)
	if err != nil {
		return "", "", false, false
	}
	if internalKey != "" {
		req.Header.Set("X-Internal-Key", internalKey)
	}
	resp, err := gateClient.Do(req)
	if err != nil {
		return "", "", false, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", false, false
	}
	var out struct {
		Baas    string `json:"baasUserID"`
		Country string `json:"country"`
	}
	if json.NewDecoder(resp.Body).Decode(&out) != nil {
		return "", "", false, false
	}
	paysComptes.retenir(pid, out.Country)
	return out.Baas, out.Country, true, false
}

// installerIdentitePublique branche la traduction dans les reglages d'Auth et du serveur
// securise. Rend false si l'interrupteur est coupe.
func installerIdentitePublique(reglages ...*nex.Settings) bool {
	if _, err := os.Stat(filepath.Join(storageDir, "smm2_identite.off")); err == nil {
		fmt.Println("[SMM2 Identite] DESACTIVEE (smm2_identite.off) : la console voit le PID interne")
		return false
	}
	identites.charger(storageDir)
	identites.chercher = chercherNSA
	for _, s := range reglages {
		s.PIDPublic = identites.public
		s.PIDInterne = identites.interne
	}
	go func() {
		for range time.Tick(10 * time.Second) {
			identites.sauver()
		}
	}()
	return true
}

// prechargerIdentites demande en arriere-plan le NSA des createurs deja connus, pour que la
// premiere liste de niveaux sorte deja sous la bonne identite.
func prechargerIdentites(pids []uint64) {
	for _, p := range pids {
		identites.mu.Lock()
		_, connu := identites.NSA[p]
		identites.mu.Unlock()
		if connu || p < premierPIDCompte || p > math.MaxUint32 {
			continue
		}
		if nsa, ok := chercherNSABloquant(p); ok {
			identites.lier(p, nsa)
		}
	}
	identites.sauver()
	fmt.Printf("[SMM2 Identite] prechargement termine : %d correspondance(s)\n", len(identites.NSA))
}

// chercherNSABloquant attend une place au lieu d'abandonner : le prechargement n'est pas
// presse, il ne doit simplement pas saturer le service de comptes.
func chercherNSABloquant(pid uint64) (uint64, bool) {
	for i := 0; i < 50; i++ {
		nsa, ok, sature := chercherNSAEtat(pid)
		if !sature {
			return nsa, ok
		}
		time.Sleep(200 * time.Millisecond)
	}
	return 0, false
}

// proprietaires rend les createurs du catalogue, sans doublon.
func (c *courseStore) proprietaires() []uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	vus := map[uint64]bool{}
	var out []uint64
	for _, cm := range c.byID {
		if !vus[cm.OwnerPID] {
			vus[cm.OwnerPID] = true
			out = append(out, cm.OwnerPID)
		}
	}
	return out
}
