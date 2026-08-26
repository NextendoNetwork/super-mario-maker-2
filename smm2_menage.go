package main

import (
	"fmt"
	"os"
	"strings"
)

// Menage des rediffusions : effacer celles que plus personne ne peut voir.
//
// CE QU'ON A MESURE. Chaque partie televerse une rediffusion (methode 132, type 12), et
// on n'en garde la reference que pour UNE : celle du record. Les autres restent sur le
// disque sans que rien ne les designe. Avec trois niveaux et une vingtaine de parties,
// il y avait deja 38 fichiers orphelins pour 3 utiles — 93 % de gaspillage, et qui
// grandit a chaque partie, indefiniment.
//
// CE QU'ON N'EFFACE PAS, ET C'EST L'ESSENTIEL. Les objets de type 1, 2, 3, 5 et 6 sont
// des accessoires PERMANENTS du niveau — miniatures, donnees de verification. Il y en a
// exactement un de chaque par niveau et leur nombre ne croit pas. Seul le type 12
// s'accumule. La regle est donc etroite a dessein : type 12 ET non reference. Effacer
// des donnees de joueur est irreversible, et une regle large qui se trompe une fois
// detruit un niveau publie.

// typeRelation rend le type d'un objet rattache (« rel-<parent>-<type> »), ou -1.
func typeRelation(nom string) int {
	if !strings.HasPrefix(nom, "rel-") {
		return -1
	}
	i := strings.LastIndexByte(nom, '-')
	if i < 0 {
		return -1
	}
	t := 0
	for _, c := range nom[i+1:] {
		if c < '0' || c > '9' {
			return -1
		}
		t = t*10 + int(c-'0')
	}
	return t
}

// effacerRediffusion supprime une rediffusion devenue inutile.
//
// Rien n'est efface si l'objet n'est pas une rediffusion, ou s'il est encore le record
// d'un niveau — on verifie plutot que de faire confiance a l'appelant.
func effacerRediffusion(dataID uint64) {
	if dataID == 0 {
		return
	}
	m := courses.get(dataID)
	if m == nil {
		return
	}
	if typeRelation(m.Name) != 12 {
		fmt.Printf("[SMM2 Menage] refus d'effacer %d (%q) : ce n'est pas une rediffusion\n", dataID, m.Name)
		return
	}
	if resultats.estRediffusionDeRecord(dataID) {
		return
	}
	if err := os.Remove(blobPath(dataID)); err != nil && !os.IsNotExist(err) {
		fmt.Printf("[SMM2 Menage] %d : %v\n", dataID, err)
		return
	}
	courses.oublier(dataID)
}

// menageInitial efface les rediffusions orphelines accumulees avant ce nettoyage.
//
// Une seule passe au demarrage. On dit combien et combien d'octets : un menage muet est
// indistinguable d'une perte de donnees quand quelqu'un s'en apercoit plus tard.
func menageInitial() {
	var candidats []uint64
	courses.mu.RLock()
	for id, m := range courses.byID {
		if typeRelation(m.Name) == 12 {
			candidats = append(candidats, id)
		}
	}
	courses.mu.RUnlock()

	var n int
	var octets int64
	for _, id := range candidats {
		if resultats.estRediffusionDeRecord(id) {
			continue
		}
		if fi, err := os.Stat(blobPath(id)); err == nil {
			octets += fi.Size()
		}
		if err := os.Remove(blobPath(id)); err != nil && !os.IsNotExist(err) {
			continue
		}
		courses.oublier(id)
		n++
	}
	if n > 0 {
		fmt.Printf("[SMM2 Menage] %d rediffusion(s) orpheline(s) effacee(s), %d Ko liberes\n", n, octets/1024)
	}
}
