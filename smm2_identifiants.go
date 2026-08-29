package main

// Traduction des identifiants de joueur envoyes par la console.
//
// LE PROBLEME, mesure le 2026-08-28. Au moment de publier un super monde, SMM2 demande
// « les niveaux publies par moi » — mais il ne designe pas le joueur par son PID NEX. Il
// envoie l'identifiant NSA de son compte Nintendo :
//
//	[Auth]         NSA 11389664994428258486 -> account pid=1800001206
//	search_posted_by(74) createurs=[11389664994428258486] -> 0 niveau(x)
//
// Nous cherchions ce nombre dans le catalogue comme s'il s'agissait d'un PID, ne trouvions
// rien, et repondions « ce createur n'a aucun niveau » a propos du joueur qui posait la
// question. Le jeu en tirait la seule conclusion possible : « des niveaux de ce super monde
// ont ete supprimes de Course World ». Rien n'etait supprime, et rien n'etait mal encode —
// nous repondions correctement a une question que nous avions mal lue.
//
// COMBIEN DE TEMPS CELA A COUTE, pour que la lecon reste. J'ai d'abord accuse la
// serialisation, puis la pagination, puis les champs inconnus de la fiche, puis les niveaux
// d'origine Nintendo du joueur. Tout cela se raisonnait tres bien et rien ne se mesurait.
// Ce qui a tranche, c'est d'avoir JOURNALISE LA VALEUR RECUE puis de l'avoir cherchee
// ailleurs dans nos propres journaux — ou elle figurait, en toutes lettres, a cote du PID.

import "math"

// pidJoueur rend le PID NEX correspondant a un identifiant venu du client.
//
// LE DISCRIMINANT est la taille. Nos PID sont attribues a partir de 1 800 000 000 et tiennent
// tous dans trente-deux bits ; un identifiant NSA en occupe soixante-quatre et depasse
// toujours cette borne. Un nombre qui ne peut pas etre un de nos PID est donc traite comme un
// NSA — et s'il ne se resout pas, il est rendu tel quel plutot que remplace par zero, qui
// designerait un autre joueur.
//
// On ne traduit JAMAIS dans l'autre sens et on ne devine jamais : la resolution passe par le
// service de comptes, seule autorite sur qui possede quel NSA. Sans cela, un client pourrait
// se faire passer pour n'importe qui en envoyant l'identifiant d'un autre.
func pidJoueur(id uint64) uint64 {
	if id <= math.MaxUint32 {
		return id // deja un PID NEX
	}
	if pid, st := resolveNSAtoPID(id); st == nsaOK {
		return pid
	}
	// Non resolu : on rend l'identifiant inchange. La recherche ne trouvera rien, ce qui
	// est la reponse juste pour un joueur qu'on ne connait pas.
	return id
}

// pidsJoueurs traduit une liste, sans la reordonner : l'appelant rend un resultat par
// identifiant DEMANDE, dans l'ordre, et melanger desynchroniserait la lecture du client.
func pidsJoueurs(ids []uint64) []uint64 {
	out := make([]uint64, len(ids))
	for i, id := range ids {
		out[i] = pidJoueur(id)
	}
	return out
}
