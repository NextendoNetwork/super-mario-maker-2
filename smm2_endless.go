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
	// CoursActuel : le niveau en cours. Il sert a distinguer « le joueur passe au niveau
	// suivant » de « le joueur vient de MOURIR et recommence le meme » — c'est le seul
	// signal dont on dispose pour decompter une vie, le jeu ne dit pas « je suis mort ».
	CoursActuel uint64 `json:"cours_actuel"`
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

// demarrerCours enregistre le niveau lance. Rend les vies, les reussites, et s'il s'agit
// d'une mort — c'est-a-dire d'un relancement du MEME niveau.
func (m *magasinEndless) demarrerCours(pid uint64, difficulte uint8, cours uint64) (uint8, uint32, bool) {
	if difficulte > 3 {
		return 0, 0, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.parPID[pid]
	if !ok {
		p = &[4]partieEndless{}
		m.parPID[pid] = p
	}
	e := &p[difficulte]
	mort := e.CoursActuel != 0 && e.CoursActuel == cours
	if mort && e.Vies > 0 {
		e.Vies--
	}
	e.CoursActuel = cours
	m.ecrireLocked()
	return e.Vies, e.Reussites, mort
}

// reussirCours acte un niveau termine : une reussite de plus, et des vies gagnees dans la
// limite du maximum de la difficulte. Sans ce plafond, un joueur accumulerait des vies sans
// fin et le mode cesserait d'etre un mode « sans fin ».
func (m *magasinEndless) reussirCours(pid uint64, difficulte, viesGagnees, pieces uint8, points uint32) (uint8, uint32) {
	if difficulte > 3 {
		return 0, 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.parPID[pid]
	if !ok {
		p = &[4]partieEndless{}
		m.parPID[pid] = p
	}
	e := &p[difficulte]
	max := viesInitialesEndless[difficulte]
	if int(e.Vies)+int(viesGagnees) > int(max) {
		e.Vies = max
	} else {
		e.Vies += viesGagnees
	}
	e.Reussites++
	e.Pieces = pieces
	e.PointsScore = points
	e.CoursActuel = 0 // le niveau est fini : le suivant ne sera pas une mort
	m.ecrireLocked()
	return e.Vies, e.Reussites
}

// terminer clot la partie : le mode repasse a zero, mais les reussites sont RENDUES avant
// d'etre effacees — c'est le score de la partie, et le jeu l'affiche.
func (m *magasinEndless) terminer(pid uint64, difficulte uint8) (uint8, uint32) {
	if difficulte > 3 {
		return 0, 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.parPID[pid]
	if !ok {
		return 0, 0
	}
	e := &p[difficulte]
	vies, reussites := e.Vies, e.Reussites
	*e = partieEndless{}
	m.ecrireLocked()
	return vies, reussites
}
