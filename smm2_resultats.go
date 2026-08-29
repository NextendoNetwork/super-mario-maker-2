package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

// Resultats de partie : qui a joue un niveau, qui l'a termine, et en combien de temps.
//
// PostPlayResult (96) n'est PAS documente sur le wiki PretendoNetwork — seul son nom y
// figure. La forme lue ici vient donc d'une console reelle, mesuree le 2026-08-24 :
//
//	01 32000000                 en-tete de structure, 50 octets
//	e803000000000000            Uint64  data_id du niveau (1000)
//	01000000                    Uint32  nombre de tentatives
//	601a0000                    Uint32  temps en millisecondes (6752 = 6,75 s)
//	01                          Bool    niveau termine
//	…                           champs encore inconnus, laisses tels quels
//	0500 "1008"                 String  data_id de la rediffusion televersee juste avant
//
// On ne pretend pas comprendre les champs du milieu. On lit ce qu'on sait identifier,
// on ignore franchement le reste, et on l'ecrit dans le journal pour pouvoir y revenir.

type resultatNiveau struct {
	Parties      uint32 `json:"parties"`      // Play stats cle 0
	Tentatives   uint32 `json:"tentatives"`   // Play stats cle 1
	Reussites    uint32 `json:"reussites"`    // Play stats cle 3
	PremierPID   uint64 `json:"premier_pid"`  // premier joueur a terminer
	RecordPID    uint64 `json:"record_pid"`   // detenteur du record du monde
	RecordMs     uint32 `json:"record_ms"`    // record du monde
	TempsAuteurM uint32 `json:"temps_auteur"` // temps de l'auteur
	Rediffusion  string `json:"rediffusion"`  // data_id de la rediffusion du record
}

// statsJoueur : l'activite cumulee d'un joueur, pour son profil en jeu.
//
// On la tient a part des resultats par niveau parce qu'on ne peut PAS la recalculer a
// partir d'eux : resultatNiveau ne retient que le premier finisseur et le detenteur du
// record, pas la liste de qui a joue. Un joueur qui termine un niveau sans battre le
// record n'y laisse aucune trace, et ses parties disparaitraient du compte.
type statsJoueur struct {
	Parties    uint32 `json:"parties"`
	Reussites  uint32 `json:"reussites"`
	Tentatives uint32 `json:"tentatives"`

	// Premieres reussites et records sont tenus A JOUR, pas recalcules.
	//
	// La premiere version les COMPTAIT sur tout le catalogue a chaque consultation de
	// profil. Avec trente niveaux c'est invisible ; GetUsers est la methode la plus
	// appelee du serveur (457 fois dans un seul journal), donc avec dix mille niveaux
	// c'etait dix mille tours de boucle par consultation, et autant pour le compte des
	// publications. On tient donc des compteurs.
	//
	// Le piege qui m'avait fait choisir le comptage : un record CHANGE DE MAIN. Un
	// compteur qui ne sait qu'augmenter le laisserait a l'ancien detenteur pour
	// toujours. La reponse n'est pas de tout recompter, c'est de decrementer l'ancien
	// au moment precis ou il perd son record — moment qu'on controle, puisqu'il n'y a
	// qu'un seul endroit ou un record change.
	PremieresReussites uint32 `json:"premieres_reussites"`
	Records            uint32 `json:"records"`
}

type magasinResultats struct {
	mu        sync.RWMutex
	parNiv    map[uint64]*resultatNiveau
	parJoueur map[uint64]*statsJoueur
	chemin    string
	modifie   bool
}

// fichierResultats : ce qu'on ecrit sur le disque. Enveloppe nommee plutot que la carte
// nue d'avant, pour pouvoir ajouter des sections sans casser la relecture.
type fichierResultats struct {
	Niveaux map[uint64]*resultatNiveau `json:"niveaux"`
	Joueurs map[uint64]*statsJoueur    `json:"joueurs"`
}

var resultats = &magasinResultats{
	parNiv:    map[uint64]*resultatNiveau{},
	parJoueur: map[uint64]*statsJoueur{},
}

func (m *magasinResultats) charger(dir string) {
	m.chemin = filepath.Join(dir, "smm2_resultats.json")
	b, err := os.ReadFile(m.chemin)
	if err != nil {
		return
	}
	// Un fichier illisible est PRESERVE, jamais ecrase : c'est le seul exemplaire des
	// records des joueurs. Mieux vaut demarrer sans eux et pouvoir les recuperer a la
	// main que les effacer proprement.
	var f fichierResultats
	if err := json.Unmarshal(b, &f); err == nil && f.Niveaux != nil {
		m.parNiv = f.Niveaux
		if f.Joueurs != nil {
			m.parJoueur = f.Joueurs
		}
		fmt.Printf("[SMM2 Resultats] %d niveau(x), %d joueur(s)\n", len(m.parNiv), len(m.parJoueur))
		return
	}
	// Ancien format : la carte des niveaux, nue. On la relit plutot que de jeter les
	// records deja gagnes par les joueurs — c'est leur seul exemplaire.
	var ancien map[uint64]*resultatNiveau
	if err := json.Unmarshal(b, &ancien); err != nil {
		fmt.Printf("[SMM2 Resultats] %s illisible (%v) — conserve tel quel, demarrage a vide\n", m.chemin, err)
		return
	}
	m.parNiv = ancien
	fmt.Printf("[SMM2 Resultats] %d niveau(x) (ancien format, converti)\n", len(ancien))
}

func (m *magasinResultats) lire(dataID uint64) resultatNiveau {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if r, ok := m.parNiv[dataID]; ok {
		return *r
	}
	return resultatNiveau{}
}

// enregistrer applique une partie et dit si quelque chose a change.
// enregistrer applique une partie et rend le data_id de la rediffusion devenue inutile,
// s'il y en a une : soit celle qu'on vient de recevoir (elle ne bat pas le record), soit
// celle de l'ancien detenteur (il vient de le perdre). Zero si rien n'est a jeter.
//
// C'est enregistrer qui le sait, parce que c'est le seul endroit ou un record change de
// main. Le menage se fait dehors, verrou relache.
func (m *magasinResultats) enregistrer(dataID, pid uint64, tentatives, ms uint32, termine bool, rediff string) (aJeter uint64) {
	m.mu.Lock()
	r, ok := m.parNiv[dataID]
	if !ok {
		r = &resultatNiveau{}
		m.parNiv[dataID] = r
	}
	r.Parties++
	r.Tentatives += tentatives

	j, ok := m.parJoueur[pid]
	if !ok {
		j = &statsJoueur{}
		m.parJoueur[pid] = j
	}
	j.Parties++
	j.Tentatives += tentatives

	if termine {
		j.Reussites++
		r.Reussites++
		if r.PremierPID == 0 {
			r.PremierPID = pid
			j.PremieresReussites++
		}
		// Un temps de zero n'est pas un record : c'est un champ qu'on n'a pas su lire.
		if ms > 0 && (r.RecordMs == 0 || ms < r.RecordMs) {
			// Le record change de main : on le retire a l'ancien avant de le donner.
			if r.RecordPID != 0 && r.RecordPID != pid {
				if ancien, ok := m.parJoueur[r.RecordPID]; ok && ancien.Records > 0 {
					ancien.Records--
				}
			}
			if r.RecordPID != pid {
				j.Records++
			}
			// L'ancienne rediffusion du record ne sera plus jamais montree.
			if r.Rediffusion != "" && r.Rediffusion != rediff {
				if v, err := strconv.ParseUint(r.Rediffusion, 10, 64); err == nil {
					aJeter = v
				}
			}
			r.RecordMs = ms
			r.RecordPID = pid
			r.Rediffusion = rediff
		} else if rediff != "" {
			// La partie n'a pas battu le record : sa rediffusion ne sert a personne.
			if v, err := strconv.ParseUint(rediff, 10, 64); err == nil {
				aJeter = v
			}
		}
	} else if rediff != "" {
		// Partie non terminee : sa rediffusion n'est referencee nulle part.
		if v, err := strconv.ParseUint(rediff, 10, 64); err == nil {
			aJeter = v
		}
	}
	m.modifie = true
	m.mu.Unlock()
	m.ecrire()
	return aJeter
}

func (m *magasinResultats) ecrire() {
	m.mu.RLock()
	if !m.modifie || m.chemin == "" {
		m.mu.RUnlock()
		return
	}
	b, err := json.Marshal(fichierResultats{Niveaux: m.parNiv, Joueurs: m.parJoueur})
	m.mu.RUnlock()
	if err != nil {
		fmt.Printf("[SMM2 Resultats] serialisation impossible: %v\n", err)
		return
	}
	// Ecriture atomique : fichier temporaire puis renommage. Une coupure au mauvais
	// moment laisse alors l'ancien fichier intact plutot qu'un JSON tronque.
	tmp := m.chemin + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		fmt.Printf("[SMM2 Resultats] ecriture impossible: %v\n", err)
		return
	}
	if err := os.Rename(tmp, m.chemin); err != nil {
		fmt.Printf("[SMM2 Resultats] renommage impossible: %v\n", err)
		return
	}
	m.mu.Lock()
	m.modifie = false
	m.mu.Unlock()
}

// smm2PostPlayResult (96) : le jeu annonce le resultat d'une partie.
func smm2PostPlayResult(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings

	in := nex.NewStreamIn(req.Body, s)
	p := in
	if s.StructHeader {
		_ = in.U8()
		p = in.Substream()
	}

	dataID := p.U64()
	tentatives := p.U32()
	ms := p.U32()
	termine := p.Bool()

	// La rediffusion est la DERNIERE chaine du corps. Plutot que de compter des champs
	// dont on ignore le sens — ce qui nous a deja coute un decalage de douze octets sur
	// CourseTimeStats — on la retrouve par sa forme dans les octets bruts.
	rediff := derniereChaine(req.Body)

	// Le menage se fait APRES, verrou relache : effacer un fichier prend le verrou du
	// catalogue, et le faire pendant qu'on tient celui des resultats est la recette d'un
	// interblocage — deux verrous pris dans deux ordres differents selon le chemin.
	if aJeter := resultats.enregistrer(dataID, conn.PID, tentatives, ms, termine, rediff); aJeter != 0 {
		effacerRediffusion(aJeter)
	}
	r := resultats.lire(dataID)

	// LE CORPS BRUT, pour retrouver la marque de mort. Quand un joueur echoue, le jeu
	// affiche une croix a l'endroit exact ou il est tombe, et cette croix est censee etre
	// conservee. Aucune methode non traitee ne porte de coordonnees pendant une partie —
	// verifie sur une partie complete le 2026-08-29 — donc elles voyagent forcement dans un
	// message qu'on traite deja, et celui-ci en est le seul candidat serieux : il porte
	// justement des champs centraux qu'on saute sans les lire.
	//
	// On ne devine pas leur position : on regarde les octets de deux echecs au meme endroit
	// et de deux echecs a des endroits differents, et on cherche ce qui change.
	fmt.Printf("[SMM2 Resultats] post_play_result(96) corps brut len=%d: %x\n", len(req.Body), req.Body)

	fmt.Printf("[SMM2 Resultats] post_play_result(96) pid=%d data_id=%d tentatives=%d temps=%dms termine=%v rediff=%q -> parties=%d reussites=%d record=%dms\n",
		conn.PID, dataID, tentatives, ms, termine, rediff, r.Parties, r.Reussites, r.RecordMs)

	// La REPONSE, elle, reste une conjecture : aucune capture, aucune documentation.
	// On renvoie un succes sans corps, ce qui est la reponse la plus sobre possible et
	// n'affirme rien de faux. Si le jeu la refuse, le journal le dira.
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, nil)
}

// derniereChaine rend la derniere chaine NEX (Uint16 longueur + octets, termine par un
// zero) trouvee dans un corps brut, ou "" s'il n'y en a pas.
func derniereChaine(b []byte) string {
	trouve := ""
	for i := 0; i+2 <= len(b); i++ {
		n := int(b[i]) | int(b[i+1])<<8
		if n < 2 || i+2+n > len(b) {
			continue
		}
		bloc := b[i+2 : i+2+n]
		if bloc[n-1] != 0 {
			continue
		}
		txt := string(bloc[:n-1])
		imprimable := true
		for _, c := range txt {
			if c < 0x20 || c > 0x7e {
				imprimable = false
				break
			}
		}
		if imprimable && txt != "" {
			trouve = txt
		}
	}
	return trouve
}

// statsDe rend l'activite d'un joueur pour son profil en jeu.
//
// Les parties et reussites viennent du compteur tenu a chaque fin de partie ; les
// premieres reussites et les records se COMPTENT sur le catalogue, parce qu'ils
// changent de main : celui qui detient un record le perd quand un autre fait mieux, et
// un compteur incremente ne saurait pas le lui retirer.
func (m *magasinResultats) statsDe(pid uint64) nex.SMM2StatsJoueur {
	m.mu.RLock()
	defer m.mu.RUnlock()

	j, ok := m.parJoueur[pid]
	if !ok {
		return nex.SMM2StatsJoueur{}
	}
	return nex.SMM2StatsJoueur{
		Parties:            j.Parties,
		Reussites:          j.Reussites,
		Tentatives:         j.Tentatives,
		PremieresReussites: j.PremieresReussites,
		Records:            j.Records,
	}
}

// reconstruireCompteurs recalcule premieres reussites et records depuis les niveaux.
//
// UNE SEULE FOIS, au demarrage. C'est le prix a payer pour tenir des compteurs plutot
// que de compter a chaque requete : il faut bien les etablir au depart, et rattraper les
// fichiers ecrits avant que ces champs existent.
func (m *magasinResultats) reconstruireCompteurs() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.parJoueur {
		j.PremieresReussites, j.Records = 0, 0
	}
	joueur := func(pid uint64) *statsJoueur {
		if pid == 0 {
			return nil
		}
		j, ok := m.parJoueur[pid]
		if !ok {
			j = &statsJoueur{}
			m.parJoueur[pid] = j
		}
		return j
	}
	for _, r := range m.parNiv {
		if j := joueur(r.PremierPID); j != nil {
			j.PremieresReussites++
		}
		if j := joueur(r.RecordPID); j != nil {
			j.Records++
		}
	}
	m.modifie = true
}

// estRediffusionDeRecord dit si cet objet est la rediffusion du record d'un niveau.
//
// C'est le garde-fou du menage : tant qu'un niveau la designe, elle est visible par les
// joueurs et ne doit pas disparaitre.
func (m *magasinResultats) estRediffusionDeRecord(dataID uint64) bool {
	cible := strconv.FormatUint(dataID, 10)
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, r := range m.parNiv {
		if r.Rediffusion == cible {
			return true
		}
	}
	return false
}
