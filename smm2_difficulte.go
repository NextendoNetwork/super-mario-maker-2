package main

// La difficulte d'un niveau.
//
// Elle ne figure PAS dans le fichier du niveau : c'est le serveur qui la calcule. La
// documentation PretendoNetwork donne l'enum (0 facile, 1 normal, 2 expert, 3 super
// expert, 4 « n'importe laquelle ») mais AUCUNE source publique ne donne les seuils
// employes par Nintendo — ni le wiki, ni l'article de TGR, ni tgrcode. J'ai cherche.
//
// LES SEUILS CI-DESSOUS SONT DONC LES NOTRES. Ce n'est pas une reconstitution de ce que
// fait Nintendo, et il ne faut pas les lire comme telle. Ils sont choisis pour que les
// quatre categories soient utilisables sur un serveur qui compte des dizaines de niveaux
// et non des millions. Ils sont regroupes ici, en un seul endroit, precisement pour
// qu'on puisse les corriger quand on aura des donnees.
//
// DEUX SOURCES, DANS CET ORDRE.
//
//  1. Le taux de reussite mesure chez nous, quand assez de gens y ont joue. C'est la
//     vraie mesure, celle qui reflete ce que vivent les joueurs.
//
//  2. Sinon, le nombre d'essais qu'il a fallu a l'AUTEUR pour terminer son propre
//     niveau, lu dans le fichier. Il doit le finir pour le publier, donc cette valeur
//     existe des la publication — avant que personne d'autre n'y ait touche.
//
// Le second point compte plus qu'il n'y parait. Sans lui, tout niveau frais serait
// annonce « facile » faute de statistiques, ce qui est une affirmation fausse — la meme
// faute que le style a zero qui presentait chaque niveau en SMB1. Un niveau que son
// propre auteur a mis quatre-vingts essais a finir n'est pas facile, et on le sait avant
// le premier joueur.

const (
	diffFacile      uint8 = 0
	diffNormal      uint8 = 1
	diffExpert      uint8 = 2
	diffSuperExpert uint8 = 3

	// En dessous de ce nombre de parties, le taux de reussite ne veut rien dire : une
	// seule reussite sur une seule partie donnerait 100 %.
	partiesMinimum = 8
)

// difficulteDepuisTaux : le taux de reussite en pourcentage.
func difficulteDepuisTaux(reussites, parties uint32) uint8 {
	taux := float64(reussites) / float64(parties) * 100
	switch {
	case taux >= 20:
		return diffFacile
	case taux >= 7:
		return diffNormal
	case taux >= 1.5:
		return diffExpert
	default:
		return diffSuperExpert
	}
}

// difficulteDepuisAuteur : combien d'essais il a fallu a l'auteur.
func difficulteDepuisAuteur(essais uint32) uint8 {
	switch {
	case essais <= 2:
		return diffFacile
	case essais <= 9:
		return diffNormal
	case essais <= 49:
		return diffExpert
	default:
		return diffSuperExpert
	}
}

// difficulteNiveau choisit la meilleure source disponible.
func difficulteNiveau(m *courseMeta) uint8 {
	r := resultats.lire(m.DataID)
	if r.Parties >= partiesMinimum {
		return difficulteDepuisTaux(r.Reussites, r.Parties)
	}
	if m.EnteteLue && m.EssaisAuteur > 0 {
		return difficulteDepuisAuteur(m.EssaisAuteur)
	}
	// Ni statistiques ni en-tete lisible : on ne sait pas. « Facile » est alors un pari,
	// et il est assume faute de valeur « inconnue » dans l'enum du protocole — la
	// cinquieme, « n'importe laquelle », est un critere de recherche, pas un etat.
	return diffFacile
}
