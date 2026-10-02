package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	nex "github.com/NextendoNetwork/nextendo-nex"
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
//
// MESURE CHEZ NINTENDO le 2026-10-02 (capturas-smm2, HALLAZGOS-SMM2-2026-10-02.txt) :
//
//   - NORMAL COMMENCE A 5 VIES, pas 10 : la 108 annonce StartLives=5 et la premiere 110
//     apres l'initialisation rend 5 vies. Facile = 5, expert = 15, super expert = 30 sont
//     les valeurs que la 108 annonce ; seule la normale a ete jouee, donc mesuree.
//   - LES VIES NE SONT PAS PLAFONNEES aux vies initiales : en normale, 5 vies + 3 gagnees
//     au premier niveau = 8 chez Nintendo. Notre plafond les ramenait a 5.
//   - la 115 rend, par difficulte, LA LISTE DES NIVEAUX REUSSIS dans la partie en cours.
//     Nous rendions quatre listes vides : une partie a N reussites et zero niveau, que le
//     jeu ne reconnait pas au retour. C'est le « Continue » qui n'apparaissait pas.
//   - les deux DateTime de la 108 sont de vraies dates : debut de partie, puis derniere
//     suspension. Une partie jamais suspendue garde l'epoque.

var viesInitialesEndless = [4]uint8{5, 5, 15, 30}

// viesMaxEndless : la seule borne. Le jeu decide lui-meme des vies gagnees ; on se
// contente de ne pas deborder de ce qu'il sait afficher.
const viesMaxEndless = 99

// niveauEndless : un niveau reussi dans la partie en cours, tel que la 115 le rend.
// Inconnu8 et Inconnu9 sont les deux derniers octets de la 111, rendus tels quels — chez
// Nintendo ils reviennent a l'identique (04 03 envoyes, 04 03 rendus).
type niveauEndless struct {
	DataID   uint64 `json:"data_id"`
	Inconnu8 uint8  `json:"u8"`
	Inconnu9 uint8  `json:"u9"`
}

// horloge est remplacable dans les tests, pour rejouer les dates de la capture.
var horloge = time.Now

func dateTimeMaintenant() uint64 {
	t := horloge().UTC()
	return uint64(nex.MakeDateTime(t.Year(), int(t.Month()), t.Day(), t.Hour(), t.Minute(), t.Second()))
}

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
	// Record : le MEILLEUR nombre de niveaux enchaines dans cette difficulte, toutes
	// parties confondues. Il survit a la fin d'une partie — c'est ce qui le distingue de
	// Reussites, remis a zero a chaque nouvelle tentative — et c'est lui que le profil
	// affiche comme score du mode sans fin.
	Record uint32 `json:"record,omitempty"`
	// Debut et Suspendue : les deux DateTime de la 108. Zero = jamais, on rend l'epoque.
	Debut     uint64 `json:"debut,omitempty"`
	Suspendue uint64 `json:"suspendue,omitempty"`
	// Niveaux : les niveaux reussis dans CETTE partie, dans l'ordre, pour la 115.
	Niveaux []niveauEndless `json:"niveaux,omitempty"`
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
	// Le Record survit a une nouvelle partie, comme il survit a terminer : l'ecraser ici
	// effacait le meilleur score de la difficulte a chaque tentative.
	p[difficulte] = partieEndless{
		Mode:      2, // actif
		Vies:      viesInitialesEndless[difficulte],
		Reussites: 0,
		Record:    p[difficulte].Record,
		Debut:     dateTimeMaintenant(),
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

// reussirCours acte un niveau termine : une reussite de plus, les vies gagnees, et le
// niveau ajoute a la liste de la partie. Il n'y a PAS de plafond aux vies initiales : chez
// Nintendo, 5 vies + 3 gagnees donnent 8 en normale.
func (m *magasinEndless) reussirCours(pid uint64, difficulte uint8, cours uint64, viesGagnees, pieces uint8, points uint32, inconnu8, inconnu9 uint8) (uint8, uint32) {
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
	if int(e.Vies)+int(viesGagnees) > viesMaxEndless {
		e.Vies = viesMaxEndless
	} else {
		e.Vies += viesGagnees
	}
	e.Reussites++
	if e.Reussites > e.Record {
		e.Record = e.Reussites
	}
	e.Pieces = pieces
	e.PointsScore = points
	e.Niveaux = append(e.Niveaux, niveauEndless{DataID: cours, Inconnu8: inconnu8, Inconnu9: inconnu9})
	e.CoursActuel = 0 // le niveau est fini : le suivant ne sera pas une mort
	m.ecrireLocked()
	return e.Vies, e.Reussites
}

// suspendre date la pause. Rien d'autre ne change : suspendre n'est pas abandonner.
func (m *magasinEndless) suspendre(pid uint64, difficulte uint8) {
	if difficulte > 3 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.parPID[pid]
	if !ok {
		return
	}
	p[difficulte].Suspendue = dateTimeMaintenant()
	m.ecrireLocked()
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
	// Le RECORD survit a la remise a zero. L'effacer avec le reste rendait le mode sans
	// fin incapable de garder quoi que ce soit : chaque partie repartait de zero et le
	// profil n'avait jamais rien a montrer.
	record := e.Record
	if reussites > record {
		record = reussites
	}
	*e = partieEndless{Record: record}
	m.ecrireLocked()
	return vies, reussites
}

// recordsDe rend les quatre records du mode sans fin, pour le profil.
//
// Le record d'une difficulte est le meilleur enchainement jamais realise ; on prend aussi
// en compte la partie EN COURS, sinon un joueur qui bat son record et consulte son profil
// sans avoir termine verrait encore l'ancien.
func (m *magasinEndless) recordsDe(pid uint64) [4]uint32 {
	var out [4]uint32
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.parPID[pid]
	if !ok {
		return out
	}
	for d := 0; d < 4; d++ {
		out[d] = p[d].Record
		if p[d].Reussites > out[d] {
			out[d] = p[d].Reussites
		}
	}
	return out
}
