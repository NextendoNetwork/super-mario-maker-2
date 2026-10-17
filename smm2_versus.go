package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// statsVersusJoueur : historique et statistiques du mode multijoueur Versus.
type statsVersusJoueur struct {
	Rating           uint32 `json:"rating"`            // Note Versus (defaut 1000)
	Parties          uint32 `json:"parties"`           // Matches joues
	Victoires        uint32 `json:"victoires"`         // Matches gagnes (1re place)
	Defaites         uint32 `json:"defaites"`          // Matches perdus
	Streak           uint32 `json:"streak"`            // Serie de victoires en cours
	MaxStreak        uint32 `json:"max_streak"`        // Meilleure serie de victoires
	MaxRating        uint32 `json:"max_rating"`        // Meilleure note atteinte
	GlickoRate       uint32 `json:"glicko_rate"`       // Taux Glicko-2
	GlickoDeviation  uint32 `json:"glicko_deviation"`  // Deviation Glicko-2
	GlickoVolatility uint32 `json:"glicko_volatility"` // Volatilite Glicko-2
	Kills            uint32 `json:"kills"`             // Joueurs elimines
	Deaths           uint32 `json:"deaths"`            // Morts subies
	PartiesCoop      uint32 `json:"parties_coop"`      // Parties coop
	VictoiresCoop    uint32 `json:"victoires_coop"`    // Reussites coop
	StreakCoop       uint32 `json:"streak_coop"`       // Serie coop
	DernierePartie   int64  `json:"derniere_partie"`   // Instant Unix
}

type magasinVersus struct {
	mu        sync.RWMutex
	parJoueur map[uint64]*statsVersusJoueur
	chemin    string
}

var versus = &magasinVersus{parJoueur: map[uint64]*statsVersusJoueur{}}

func (m *magasinVersus) charger(dir string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.chemin = filepath.Join(dir, "smm2_versus.json")
	if b, err := os.ReadFile(m.chemin); err == nil {
		var c map[uint64]*statsVersusJoueur
		if json.Unmarshal(b, &c) == nil && c != nil {
			m.parJoueur = c
		}
	}
	fmt.Printf("[SMM2 Versus] %d joueur(s) charges dans l'historique versus\n", len(m.parJoueur))
}

func (m *magasinVersus) ecrireLocked() {
	if m.chemin == "" {
		return
	}
	b, err := json.Marshal(m.parJoueur)
	if err != nil {
		return
	}
	tmp := m.chemin + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		_ = os.Rename(tmp, m.chemin)
	}
}

// statsDe rend les statistiques du joueur ou les valeurs d'ouverture par defaut.
func (m *magasinVersus) statsDe(pid uint64) statsVersusJoueur {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if st, ok := m.parJoueur[pid]; ok && st != nil {
		return *st
	}
	return statsVersusJoueur{Rating: 1000, MaxRating: 1000, GlickoRate: 1500, GlickoDeviation: 350}
}

// ratingDe rend la note pour le classement Versus (Method 52).
func (m *magasinVersus) ratingDe(pid uint64) uint32 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if st, ok := m.parJoueur[pid]; ok && st != nil && st.Parties > 0 {
		return st.Rating
	}
	return 0
}

// rangTierDe calcule le tier pour multiplayer_stats (1 = D, 2 = C, 3 = B, 4 = A, 5 = S, 6 = S+).
// Seuils extraits du code machine ARM64 a 0x1b81d24-0x1b81f10 dans main :
//   < 1000      -> D  (index 0, tier 1)
//   1000 - 1999 -> C  (index 1, tier 2)
//   2000 - 2999 -> B  (index 2, tier 3)
//   3000 - 3999 -> A  (index 3, tier 4)
//   4000 - 4999 -> S  (index 4, tier 5)
//   >= 5000     -> S+ (index 5, tier 6)
func rangTierDe(rating uint32) uint32 {
	switch {
	case rating >= 5000:
		return 6 // S+
	case rating >= 4000:
		return 5 // S
	case rating >= 3000:
		return 4 // A
	case rating >= 2000:
		return 3 // B
	case rating >= 1000:
		return 2 // C
	default:
		return 1 // D
	}
}

// multiplayerStats renseigne les 15 cles de la table multiplayer_stats d'UserInfo.
func (m *magasinVersus) multiplayerStats(pid uint64) map[uint8]uint32 {
	st := m.statsDe(pid)
	stats := make(map[uint8]uint32, 15)
	for i := uint8(0); i < 15; i++ {
		stats[i] = 0
	}
	if st.Parties == 0 && st.PartiesCoop == 0 {
		stats[0] = 0
		stats[1] = 1
		return stats
	}
	if st.Parties > 0 {
		stats[0] = st.Rating
		stats[1] = rangTierDe(st.Rating)
	} else {
		stats[0] = 0
		stats[1] = 1
	}
	stats[2] = st.Parties
	stats[3] = st.Victoires
	stats[4] = st.Streak
	stats[5] = st.MaxRating
	stats[6] = st.MaxStreak
	stats[7] = st.GlickoRate
	stats[8] = st.GlickoDeviation
	stats[9] = 0 // deconnexions
	stats[10] = st.PartiesCoop
	stats[11] = st.VictoiresCoop
	stats[12] = st.StreakCoop
	stats[13] = 0 // points coop
	stats[14] = st.StreakCoop
	return stats
}

// enregistrerFinMatch met a jour les stats du joueur suite a EndBattleMode (Methode 119).
func (m *magasinVersus) enregistrerFinMatch(pid uint64, rang uint8, kills, deaths, glickoRate, glickoDev, glickoVol uint32) (newRating, tier uint32) {
	m.mu.Lock()
	defer m.mu.Unlock()

	st, ok := m.parJoueur[pid]
	if !ok || st == nil {
		st = &statsVersusJoueur{Rating: 1000, MaxRating: 1000, GlickoRate: 1500, GlickoDeviation: 350}
		m.parJoueur[pid] = st
	}

	st.Parties++
	st.Kills += kills
	st.Deaths += deaths
	st.DernierePartie = nowUnix()

	if glickoRate > 0 {
		st.GlickoRate = glickoRate
	}
	if glickoDev > 0 {
		st.GlickoDeviation = glickoDev
	}
	if glickoVol > 0 {
		st.GlickoVolatility = glickoVol
	}

	if rang == 1 {
		// Victoire !
		st.Victoires++
		st.Streak++
		if st.Streak > st.MaxStreak {
			st.MaxStreak = st.Streak
		}
		delta := uint32(20 + st.Streak*2)
		if glickoRate > 0 && glickoRate > st.Rating {
			delta = (glickoRate - st.Rating) / 10
			if delta < 15 {
				delta = 15
			}
		}
		st.Rating += delta
	} else {
		// Defaite
		st.Defaites++
		st.Streak = 0
		var perte uint32 = 12
		if st.Rating > 1000+perte {
			st.Rating -= perte
		} else {
			st.Rating = 1000
		}
	}

	if st.Rating > st.MaxRating {
		st.MaxRating = st.Rating
	}

	m.ecrireLocked()
	return st.Rating, rangTierDe(st.Rating)
}

// enregistrerFinMatchCoop met a jour les compteurs du mode cooperatif (Multi Clear).
func (m *magasinVersus) enregistrerFinMatchCoop(pid uint64, reussi bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	st, ok := m.parJoueur[pid]
	if !ok || st == nil {
		st = &statsVersusJoueur{Rating: 1000, MaxRating: 1000, GlickoRate: 1500, GlickoDeviation: 350}
		m.parJoueur[pid] = st
	}

	st.PartiesCoop++
	st.DernierePartie = nowUnix()

	if reussi {
		st.VictoiresCoop++
		st.StreakCoop++
	} else {
		st.StreakCoop = 0
	}

	m.ecrireLocked()
}
