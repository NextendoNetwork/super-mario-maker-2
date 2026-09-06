package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Moderation des niveaux publies.
//
// POURQUOI UNE MISE EN QUARANTAINE ET PAS UNE SUPPRESSION.
//
// Un niveau signale doit disparaitre tout de suite, mais la moderation se trompe : un nom
// mal compris, un signalement de mauvaise foi, un clic a cote. Effacer le fichier rend
// l'erreur DEFINITIVE, et il n'y a aucune sauvegarde par niveau — le seul recours serait
// une restauration complete de /opt/smm2, qui ecraserait aussi tout ce qui a ete publie
// depuis. On DEPLACE donc, on n'efface pas.
//
// L'effet en jeu est le meme qu'une suppression : tous les chemins de lecture — recherche,
// Course World, fiche de niveau, profil de l'auteur — passent par le catalogue, et un
// niveau retire n'y est plus. Rien d'autre n'a besoin d'etre modifie, et c'est justement
// ce qui rend l'operation sure : un seul point de passage.
//
// CE QU'ON NE TOUCHE PAS. Les resultats (parties, records) et les commentaires restent en
// place. C'est deliberé : la quarantaine est reversible, donc la detruire les rendrait
// irreversible par un autre chemin. Un niveau restaure retrouve son historique intact.

// dossierQuarantaine : a COTE du dossier des objets, pas dedans.
//
// Le balayage des rediffusions orphelines parcourt le CATALOGUE et jamais le disque, donc
// des fichiers hors catalogue ne risquent rien — mais garder les deux separes evite d'avoir
// a re-verifier ce raisonnement chaque fois que quelqu'un touchera au menage.
func dossierQuarantaine() string {
	return filepath.Join(filepath.Dir(storageDir), "quarantaine")
}

// niveauModere : une ligne de la liste de moderation.
type niveauModere struct {
	DataID      uint64   `json:"data_id"`
	Code        string   `json:"code"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	OwnerPID    uint64   `json:"owner_pid"`
	Auteur      string   `json:"auteur"`
	CreatedAt   int64    `json:"created_at"`
	Size        uint32   `json:"size"`
	Style       uint8    `json:"style"`
	Theme       uint8    `json:"theme"`
	Tags        []string `json:"tags,omitempty"`
	Ready       bool     `json:"ready"`
	Parties     uint32   `json:"parties"`
	Reussites   uint32   `json:"reussites"`
	Commentaires uint32  `json:"commentaires"`
}

// compteursModeration rend les compteurs de parties d'un niveau, ou deux zeros.
func (m *magasinResultats) compteursModeration(dataID uint64) (uint32, uint32) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if r := m.parNiv[dataID]; r != nil {
		return r.Parties, r.Reussites
	}
	return 0, 0
}

// listerNiveauxModeration rend les VRAIS niveaux du catalogue.
//
// Les entrees « rel-… » en sont exclues : ce sont les miniatures et les rediffusions
// rattachees a un niveau, pas des niveaux. Elles representent l'essentiel des entrees et
// les afficher noierait la liste sous des lignes sur lesquelles il n'y a rien a moderer.
func listerNiveauxModeration() []niveauModere {
	courses.mu.RLock()
	metas := make([]*courseMeta, 0, len(courses.byID))
	for _, m := range courses.byID {
		if typeRelation(m.Name) >= 0 {
			continue
		}
		cp := *m
		metas = append(metas, &cp)
	}
	courses.mu.RUnlock()

	out := make([]niveauModere, 0, len(metas))
	for _, m := range metas {
		parties, reussites := resultats.compteursModeration(m.DataID)
		out = append(out, niveauModere{
			DataID:       m.DataID,
			Code:         codeMelange(m.DataID),
			Name:         m.Name,
			Description:  m.Description,
			OwnerPID:     m.OwnerPID,
			Auteur:       pseudoOr(m.OwnerPID),
			CreatedAt:    m.CreatedAt,
			Size:         m.Size,
			Style:        m.Style,
			Theme:        m.Theme,
			Tags:         m.Tags,
			Ready:        m.Ready,
			Parties:      parties,
			Reussites:    reussites,
			Commentaires: commentaires.nombreDe(m.DataID),
		})
	}
	// Le plus recent d'abord : un signalement porte presque toujours sur une publication
	// recente, et sans tri l'ordre serait celui d'une carte Go, donc different a chaque appel.
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out
}

// ficheQuarantaine : ce qu'on garde pour pouvoir revenir en arriere.
//
// On y recopie les entrees de catalogue TELLES QUELLES. Restaurer, c'est les remettre dans
// le catalogue et rapatrier les fichiers : rien n'a besoin d'etre reconstruit ni redevine.
type ficheQuarantaine struct {
	DataID    uint64        `json:"data_id"`
	Code      string        `json:"code"`
	Nom       string        `json:"nom"`
	Auteur    string        `json:"auteur"`
	OwnerPID  uint64        `json:"owner_pid"`
	RetireLe  int64         `json:"retire_le"`
	Motif     string        `json:"motif"`
	Par       string        `json:"par"`
	Entrees   []*courseMeta `json:"entrees"`
}

// retirerNiveau met un niveau et ses objets rattaches en quarantaine.
//
// Tout se fait sous UN SEUL verrou du catalogue : sans ca, entre le moment ou on liste les
// objets a deplacer et celui ou on les retire, une partie en cours peut en ajouter un — et
// il resterait sur le disque en pointant vers un niveau qui n'existe plus.
func retirerNiveau(dataID uint64, motif, par string) (*ficheQuarantaine, error) {
	if dataID == 0 {
		return nil, fmt.Errorf("data_id manquant")
	}
	dossier := dossierQuarantaine()
	if err := os.MkdirAll(dossier, 0o755); err != nil {
		return nil, fmt.Errorf("quarantaine inaccessible : %w", err)
	}

	courses.mu.Lock()
	defer courses.mu.Unlock()

	principal := courses.byID[dataID]
	if principal == nil {
		return nil, fmt.Errorf("niveau %d introuvable", dataID)
	}
	if typeRelation(principal.Name) >= 0 {
		return nil, fmt.Errorf("%d est un objet rattache (%q), pas un niveau", dataID, principal.Name)
	}

	// Les objets rattaches sont des entrees de catalogue nommees « rel-<parent>-<type> ».
	prefixe := "rel-" + strconv.FormatUint(dataID, 10) + "-"
	aRetirer := []*courseMeta{principal}
	for _, m := range courses.byID {
		if m.DataID != dataID && strings.HasPrefix(m.Name, prefixe) {
			aRetirer = append(aRetirer, m)
		}
	}

	fiche := &ficheQuarantaine{
		DataID:   dataID,
		Code:     codeMelange(dataID),
		Nom:      principal.Name,
		Auteur:   pseudoOr(principal.OwnerPID),
		OwnerPID: principal.OwnerPID,
		RetireLe: time.Now().Unix(),
		Motif:    motif,
		Par:      par,
		Entrees:  aRetirer,
	}

	// La fiche s'ecrit AVANT de deplacer quoi que ce soit. Si l'ecriture echoue, rien n'a
	// bouge ; si elle reussit et qu'un deplacement echoue ensuite, on sait au moins quoi
	// chercher et ou. L'ordre inverse laisserait des fichiers sans mode d'emploi.
	chemin := filepath.Join(dossier, strconv.FormatUint(dataID, 10)+".json")
	b, err := json.MarshalIndent(fiche, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(chemin, b, 0o644); err != nil {
		return nil, fmt.Errorf("fiche de quarantaine : %w", err)
	}

	var deplaces int
	for _, m := range aRetirer {
		src := blobPath(m.DataID)
		dst := filepath.Join(dossier, strconv.FormatUint(m.DataID, 10)+".bin")
		if err := os.Rename(src, dst); err != nil {
			// Un objet dont le fichier manque deja n'est pas une raison d'abandonner : on
			// le signale et on continue, sinon un seul fichier absent bloquerait le retrait
			// d'un niveau signale.
			if !os.IsNotExist(err) {
				fmt.Printf("[SMM2 Moderation] %d : deplacement impossible : %v\n", m.DataID, err)
			}
			continue
		}
		deplaces++
	}
	for _, m := range aRetirer {
		delete(courses.byID, m.DataID)
	}
	courses.persistLocked()

	fmt.Printf("[SMM2 Moderation] RETIRE %d (%q de %s) : %d entrees, %d fichiers deplaces, motif=%q par=%q\n",
		dataID, principal.Name, fiche.Auteur, len(aRetirer), deplaces, motif, par)
	return fiche, nil
}

// restaurerNiveau remet en ligne un niveau mis en quarantaine.
func restaurerNiveau(dataID uint64) (*ficheQuarantaine, error) {
	dossier := dossierQuarantaine()
	chemin := filepath.Join(dossier, strconv.FormatUint(dataID, 10)+".json")
	b, err := os.ReadFile(chemin)
	if err != nil {
		return nil, fmt.Errorf("aucune fiche de quarantaine pour %d", dataID)
	}
	var fiche ficheQuarantaine
	if err := json.Unmarshal(b, &fiche); err != nil {
		return nil, fmt.Errorf("fiche illisible : %w", err)
	}

	courses.mu.Lock()
	defer courses.mu.Unlock()

	if courses.byID[dataID] != nil {
		return nil, fmt.Errorf("le niveau %d est deja au catalogue", dataID)
	}

	var rendus int
	for _, m := range fiche.Entrees {
		src := filepath.Join(dossier, strconv.FormatUint(m.DataID, 10)+".bin")
		if err := os.Rename(src, blobPath(m.DataID)); err != nil {
			if !os.IsNotExist(err) {
				fmt.Printf("[SMM2 Moderation] %d : retour impossible : %v\n", m.DataID, err)
				continue
			}
			// Fichier absent des deux cotes : on remet quand meme l'entree au catalogue si
			// elle en avait un a l'origine ? Non — une entree sans fichier donnerait un
			// niveau qui apparait dans les listes et plante a l'ouverture. On la saute.
			continue
		}
		courses.byID[m.DataID] = m
		rendus++
	}
	// nextID doit rester au-dessus de tout identifiant rendu, sinon le prochain
	// televersement reutiliserait celui d'un objet restaure et ecraserait son fichier.
	for _, m := range fiche.Entrees {
		if m.DataID >= courses.nextID {
			courses.nextID = m.DataID + 1
		}
	}
	courses.persistLocked()

	if courses.byID[dataID] == nil {
		return nil, fmt.Errorf("le fichier du niveau %d est introuvable : rien restaure", dataID)
	}
	_ = os.Remove(chemin)

	fmt.Printf("[SMM2 Moderation] RESTAURE %d (%q) : %d entrees rendues\n", dataID, fiche.Nom, rendus)
	return &fiche, nil
}

// listerQuarantaine rend les fiches des niveaux actuellement retires.
func listerQuarantaine() []ficheQuarantaine {
	dossier := dossierQuarantaine()
	entrees, err := os.ReadDir(dossier)
	if err != nil {
		return []ficheQuarantaine{}
	}
	out := []ficheQuarantaine{}
	for _, e := range entrees {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dossier, e.Name()))
		if err != nil {
			continue
		}
		var f ficheQuarantaine
		if json.Unmarshal(b, &f) == nil {
			// Les entrees completes ne servent qu'a la restauration : les renvoyer ferait
			// voyager le meta_binary hexadecimal de chaque objet dans la reponse.
			f.Entrees = nil
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RetireLe > out[j].RetireLe })
	return out
}
