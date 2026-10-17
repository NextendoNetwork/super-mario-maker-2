package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// magasinOfficiels gere les createurs officiels (Nintendo / Ninji / Admin).
// Ces createurs portent le drapeau IsNintendoEmployee dans UserInfo (revision 3),
// ce qui affiche le badge officiel rouge sur leur Mii et leurs niveaux.
type magasinOfficiels struct {
	mu     sync.RWMutex
	PIDs   map[uint64]bool `json:"pids"`
	chemin string
}

var officiels = &magasinOfficiels{PIDs: map[uint64]bool{}}

func (m *magasinOfficiels) charger(dir string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.chemin = filepath.Join(dir, "smm2_officiel.json")
	if b, err := os.ReadFile(m.chemin); err == nil {
		var c struct {
			PIDs []uint64 `json:"pids"`
		}
		if json.Unmarshal(b, &c) == nil && len(c.PIDs) > 0 {
			for _, pid := range c.PIDs {
				m.PIDs[pid] = true
			}
		}
	}
	// Charger aussi depuis la variable d'environnement OFFICIAL_MAKERS (ex: "100,101")
	if env := os.Getenv("OFFICIAL_MAKERS"); env != "" {
		for _, part := range strings.Split(env, ",") {
			if pid, err := strconv.ParseUint(strings.TrimSpace(part), 10, 64); err == nil && pid > 0 {
				m.PIDs[pid] = true
			}
		}
	}
	fmt.Printf("[SMM2 Officiel] %d createur(s) officiel(s) charge(s)\n", len(m.PIDs))
}

func (m *magasinOfficiels) ecrireLocked() {
	if m.chemin == "" {
		return
	}
	var list []uint64
	for pid := range m.PIDs {
		list = append(list, pid)
	}
	out := struct {
		PIDs []uint64 `json:"pids"`
	}{PIDs: list}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return
	}
	tmp := m.chemin + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		_ = os.Rename(tmp, m.chemin)
	}
}

func (m *magasinOfficiels) estOfficiel(pid uint64) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.PIDs[pid]
}

func (m *magasinOfficiels) ajouter(pid uint64) {
	if pid == 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.PIDs[pid] = true
	m.ecrireLocked()
}

func (m *magasinOfficiels) retirer(pid uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.PIDs, pid)
	m.ecrireLocked()
}

func (m *magasinOfficiels) liste() []uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []uint64
	for pid := range m.PIDs {
		out = append(out, pid)
	}
	return out
}
