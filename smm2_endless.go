package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// L'etat des parties de Mario sans fin.
//
// TGR prevenait dans Phrack 72 que ce mode n'est pas une methode a repondre mais un ETAT
// a tenir : le jeu initialise une partie, puis redemande son etat et verifie qu'elle
// existe. Tant qu'on repondait « aucune partie » apres avoir accepte l'initialisation,
// il concluait que rien ne s'etait passe et abandonnait.
//
// Les regles viennent d'ocw-server (db/dyna/endless_type_normal.go et endless.go) :
//
//   - vies initiales : 5, 10, 15, 30 selon la difficulte
//   - InitializeEndless met mode = 2 (actif), vies = vies initiales, reussites = 0
//   - dans GetEndlessModeStatus : si mode == 2 on rend les vies COURANTES, sinon les
//     vies INITIALES — jamais zero, ce qui etait notre erreur
//
// Ce dernier point est ce qui bloquait : nous envoyions quatre difficultes a zero vie.

var viesInitialesEndless = [4]uint8{5, 10, 15, 30}

type partieEndless struct {
	Mode        uint8  `json:"mode"` // 2 = partie en cours
	Vies        uint8  `json:"vies"`
	Reussites   uint32 `json:"reussites"`
	Pieces      uint8  `json:"pieces"`
	PointsScore uint32 `json:"points"`
}

type magasinEndless struct {
	mu     sync.RWMutex
	parPID map[uint64]*[4]partieEndless
	chemin string
}

var endless = &magasinEndless{parPID: map[uint64]*[4]partieEndless{}}

func (m *magasinEndless) charger(dir string) {
	m.chemin = filepath.Join(dir, "smm2_endless.json")
	b, err := os.ReadFile(m.chemin)
	if err != nil {
		return
	}
	var charge map[uint64]*[4]partieEndless
	if err := json.Unmarshal(b, &charge); err != nil {
		fmt.Printf("[SMM2 Endless] %s illisible (%v) — conserve tel quel\n", m.chemin, err)
		return
	}
	m.parPID = charge
	fmt.Printf("[SMM2 Endless] %d joueur(s) avec une partie enregistree\n", len(charge))
}

func (m *magasinEndless) ecrireLocked() {
	if m.chemin == "" {
		return
	}
	b, err := json.Marshal(m.parPID)
	if err != nil {
		return
	}
	tmp := m.chemin + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		os.Rename(tmp, m.chemin)
	}
}

// etatDe rend les quatre difficultes d'un joueur, en creant l'entree au besoin.
func (m *magasinEndless) etatDe(pid uint64) [4]partieEndless {
	m.mu.RLock()
	p, ok := m.parPID[pid]
	m.mu.RUnlock()
	if ok {
		return *p
	}
	return [4]partieEndless{}
}

// demarrer initialise une partie, comme InitializeEndless dans ocw-server.
func (m *magasinEndless) demarrer(pid uint64, difficulte uint8) {
	if difficulte > 3 {
		return
	}
	m.mu.Lock()
	p, ok := m.parPID[pid]
	if !ok {
		p = &[4]partieEndless{}
		m.parPID[pid] = p
	}
	p[difficulte] = partieEndless{
		Mode:      2, // actif
		Vies:      viesInitialesEndless[difficulte],
		Reussites: 0,
	}
	m.ecrireLocked()
	m.mu.Unlock()
}
