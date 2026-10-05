package main

// Les NOTES (J'aime / Bouh) et les MARQUES DE MORT d'un niveau.
//
// MESURE CHEZ NINTENDO le 2026-10-02 (capturas-smm2) :
//
//   - 104 PostRatingInfo : { data_id, Uint8 note, Uint8, Bool } — note 1 = J'aime,
//     2 = Bouh, 0 = aucune (envoyee apres une reussite en mode sans fin). Reponse vide.
//   - 61 CanPostRatingAndComment : VRAI, 0, { 0: notes restantes, 1: 1 }, VRAI, 0,
//     { 0: commentaires restants, 1: 2 }. Les compteurs baissent de un a chaque note ou
//     commentaire du joueur (99999995, 99999994, 99999993…). Nous rendions FAUX et deux
//     tables VIDES : sans quota, les boutons s'affichaient mais ne repondaient pas.
//   - CourseInfo, table des notes (option 0x2) : cle 0 = coeurs (documente), 1 = bouh,
//     2 = joue sans noter (deduit : elle augmente sur une reussite sans 104 notee).
//     Nous l'envoyions vide.
//   - 96 PostPlayResult porte, apres le temps, une structure de 13 octets
//     { data_id, Uint16 x, Uint16 y, Uint8 sous-zone } : la marque de mort de l'essai.
//     A zero (data_id compris) quand le niveau est reussi. C'est exactement l'element que
//     rend la 103, qui en donne jusqu'a 200. Nous ne la lisions pas et la 103 rendait
//     toujours une liste vide — en lisant d'ailleurs son parametre de travers.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

const (
	noteAucune = 0
	noteAime   = 1
	noteBouh   = 2

	maxMarquesMort   = 200      // la 103 de Nintendo en rendait 200 au plus
	quotaNotes       = 99999999 // Nintendo : 99999995 apres quelques notes
	quotaCommParJour = 100      // Nintendo : 98, puis 96 apres deux commentaires
)

type marqueMort struct {
	X        uint16 `json:"x"`
	Y        uint16 `json:"y"`
	SousZone uint8  `json:"z,omitempty"`
}

type magasinNotes struct {
	mu sync.RWMutex
	// Notes : niveau -> joueur -> note (0, 1, 2).
	Notes map[uint64]map[uint64]uint8 `json:"notes"`
	// Donnees : joueur -> nombre de notes donnees, pour le quota de la 61.
	Donnees map[uint64]uint32 `json:"donnees"`
	// Morts : niveau -> les marques, les plus recentes a la fin.
	Morts map[uint64][]marqueMort `json:"morts"`
	// Progres : niveau -> joueur -> 2 joue, 3 reussi. Pour que la fiche d'un niveau dise
	// a CE joueur ou il en est, comme chez Nintendo (premier des quatre octets).
	Progres map[uint64]map[uint64]uint8 `json:"progres,omitempty"`
	chemin  string
}

var notes = &magasinNotes{Notes: map[uint64]map[uint64]uint8{}, Donnees: map[uint64]uint32{}, Morts: map[uint64][]marqueMort{}}

func (m *magasinNotes) charger(dir string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.chemin = filepath.Join(dir, "smm2_notes.json")
	if b, err := os.ReadFile(m.chemin); err == nil {
		var c magasinNotes
		if json.Unmarshal(b, &c) == nil {
			if c.Notes != nil {
				m.Notes = c.Notes
			}
			if c.Donnees != nil {
				m.Donnees = c.Donnees
			}
			if c.Morts != nil {
				m.Morts = c.Morts
			}
			if c.Progres != nil {
				m.Progres = c.Progres
			}
		}
	}
	fmt.Printf("[SMM2 Notes] notes sur %d niveau(x), marques de mort sur %d\n", len(m.Notes), len(m.Morts))
}

func (m *magasinNotes) ecrireLocked() {
	if m.chemin == "" {
		return
	}
	b, err := json.Marshal(m)
	if err != nil {
		return
	}
	if os.WriteFile(m.chemin+".tmp", b, 0o644) == nil {
		os.Rename(m.chemin+".tmp", m.chemin)
	}
}

// noter enregistre la note d'un joueur. Changer d'avis remplace l'ancienne note ; une note
// « aucune » n'efface pas un J'aime deja donne.
func (m *magasinNotes) noter(dataID, pid uint64, note uint8) {
	m.mu.Lock()
	defer m.mu.Unlock()
	parPID := m.Notes[dataID]
	if parPID == nil {
		parPID = map[uint64]uint8{}
		m.Notes[dataID] = parPID
	}
	ancienne, existe := parPID[pid]
	if note == noteAucune && existe {
		return
	}
	if note != noteAucune && ancienne != note {
		m.Donnees[pid]++
	}
	parPID[pid] = note
	m.ecrireLocked()
}

// compte rend les trois cles de la table des notes de CourseInfo.
// avancer retient l'etat d'un joueur sur un niveau ; il ne recule jamais (un niveau
// reussi le reste meme si l'on y meurt ensuite).
func (m *magasinNotes) avancer(dataID, pid uint64, etat uint8) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Progres == nil {
		m.Progres = map[uint64]map[uint64]uint8{}
	}
	parPID := m.Progres[dataID]
	if parPID == nil {
		parPID = map[uint64]uint8{}
		m.Progres[dataID] = parPID
	}
	if etat > parPID[pid] {
		parPID[pid] = etat
		m.ecrireLocked()
	}
}

// etatJoueur rend les deux premiers des quatre octets de CourseInfo pour CE joueur
// (mesure chez Nintendo, 2026-10-05) : son etat — 1 jamais joue, 2 joue, 3 reussi — et
// sa note — 1 aucune, 2 joue sans noter, 3 J'aime, 4 Bouh.
func (m *magasinNotes) etatJoueur(dataID, pid uint64) (uint8, uint8) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	etat := m.Progres[dataID][pid]
	n, note := m.Notes[dataID][pid]
	if note && etat == 0 {
		// Notes anterieures au suivi : une note « aucune » n'arrive qu'a la reussite
		// (104 avec l'indicateur de reussite), une vraie note prouve au moins une partie.
		etat = 2
		if n == noteAucune {
			etat = 3
		}
	}
	if etat == 0 {
		return 1, 1
	}
	switch {
	case note && n == noteAime:
		return etat, 3
	case note && n == noteBouh:
		return etat, 4
	}
	return etat, 2
}

func (m *magasinNotes) compte(dataID uint64) (aime, bouh, sans uint32) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, n := range m.Notes[dataID] {
		switch n {
		case noteAime:
			aime++
		case noteBouh:
			bouh++
		default:
			sans++
		}
	}
	return
}

func (m *magasinNotes) restantes(pid uint64) uint32 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if d := m.Donnees[pid]; d < quotaNotes {
		return quotaNotes - d
	}
	return 0
}

func (m *magasinNotes) mourir(dataID uint64, x, y uint16, zone uint8) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := append(m.Morts[dataID], marqueMort{X: x, Y: y, SousZone: zone})
	if len(l) > maxMarquesMort {
		l = l[len(l)-maxMarquesMort:]
	}
	m.Morts[dataID] = l
	m.ecrireLocked()
}

func (m *magasinNotes) marques(dataID uint64) []marqueMort {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]marqueMort(nil), m.Morts[dataID]...)
}

// commentairesDuJour : combien de commentaires ce joueur a laisses depuis 24 heures.
func commentairesDuJour(pid uint64) uint32 {
	depuis := time.Now().Add(-24 * time.Hour).Unix()
	var n uint32
	commentaires.mu.RLock()
	for _, l := range commentaires.parNiv {
		for _, c := range l {
			if c.PID == pid && c.Quand >= depuis {
				n++
			}
		}
	}
	commentaires.mu.RUnlock()
	return n
}

// encode61 : la reponse mesuree de CanPostRatingAndComment.
func encode61(s *nex.Settings, dataID, pid uint64) []byte {
	comm := uint32(0)
	if d := commentairesDuJour(pid); d < quotaCommParJour {
		comm = quotaCommParJour - d
	}
	c := nex.NewStreamOut(s)
	c.U64(dataID)
	c.Bool(true)
	c.U32(0)
	c.U32(2)
	c.U8(0)
	c.U32(notes.restantes(pid))
	c.U8(1)
	c.U32(1)
	c.Bool(true)
	c.U32(0)
	c.U32(2)
	c.U8(0)
	c.U32(comm)
	c.U8(1)
	c.U32(2)
	return frameStruct(s, 0, c.Bytes())
}

// smm2PostRatingInfo (104) : le joueur note le niveau.
func smm2PostRatingInfo(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	in := nex.NewStreamIn(req.Body, s)
	_ = in.U8()
	p := in.Substream()
	dataID := p.U64()
	note := p.U8()
	b := p.U8()
	c := p.Bool()
	if err := p.Err(); err != nil || note > noteBouh {
		fmt.Printf("[SMM2 Notes] post_rating_info(104) pid=%d : parametre illisible (%v) brut=%x\n", conn.PID, err, req.Body)
		return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, nil)
	}
	notes.noter(dataID, conn.PID, note)
	// Le premier Uint8 apres la note vaut 1 quand la 104 suit une reussite (mesure : la
	// 104 automatique de fin de niveau porte 0, 1, vrai).
	if b == 1 {
		notes.avancer(dataID, conn.PID, 3)
	} else {
		notes.avancer(dataID, conn.PID, 2)
	}
	aime, bouh, _ := notes.compte(dataID)
	fmt.Printf("[SMM2 Notes] post_rating_info(104) pid=%d niveau=%d note=%d (%d, %v) -> %d coeur(s), %d bouh\n",
		conn.PID, dataID, note, b, c, aime, bouh)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, nil)
}

// smm2GetDeathPositions (103) : les marques de mort du niveau.
//
// Requete : un Uint64 NU (mesure, huit octets). Reponse : List<DeathPositionInfo>, chaque
// element encadre { data_id, x, y, sous-zone } — treize octets.
func smm2GetDeathPositions(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	dataID := nex.NewStreamIn(req.Body, s).U64()
	l := notes.marques(dataID)
	out := nex.NewStreamOut(s)
	out.U32(uint32(len(l)))
	for _, m := range l {
		f := nex.NewStreamOut(s)
		f.U64(dataID)
		f.U16(m.X)
		f.U16(m.Y)
		f.U8(m.SousZone)
		out.Write(frameStruct(s, 0, f.Bytes()))
	}
	fmt.Printf("[SMM2 Courses] get_death_positions(103) data_id=%d -> %d\n", dataID, len(l))
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}

// lireMarqueMort lit, dans le corps d'une 96, la structure qui suit { data_id,
// tentatives, temps, termine } : la marque de mort. Rend ok=false si elle est absente ou
// nulle (niveau reussi).
func lireMarqueMort(s *nex.Settings, corps []byte) (dataID uint64, m marqueMort, ok bool) {
	in := nex.NewStreamIn(corps, s)
	_ = in.U8()
	p := in.Substream()
	_ = p.U64()
	_ = p.U32()
	_ = p.U32()
	_ = p.Bool()
	_ = p.U8()
	q := p.Substream()
	dataID = q.U64()
	m.X = q.U16()
	m.Y = q.U16()
	m.SousZone = q.U8()
	if p.Err() != nil || q.Err() != nil || dataID == 0 {
		return 0, marqueMort{}, false
	}
	return dataID, m, true
}
