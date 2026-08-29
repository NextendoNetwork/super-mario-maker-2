package main

// Les reglages entiers que SMM2 demande par Utility.GetIntegerSettings (0x6E, methode 7).
//
// CE QU'ILS BLOQUAIENT. Nous rendions une carte VIDE — `corps=00 00 00 00` dans le journal.
// L'ecran des super mondes affichait « service non disponible » et le jeu tournait en rond :
// il redemandait son profil et sa carte du monde en boucle sans jamais atteindre la
// recherche (methode 162). C'est ici qu'un serveur declare ce qui est disponible ; une carte
// vide, c'est « rien n'est disponible ».
//
// LA MEME FAMILLE DE PANNE QUE PAC-MAN 99, qui refusait d'apparier tant que GetIntegerSettings
// ne rendait pas au moins vingt-deux entrees. Le client lit des POSITIONS, pas des cles : une
// entree manquante au milieu decale tout ce qui suit.
//
// D'OU VIENNENT CES VALEURS. Ce sont celles que rend un serveur SMM2 qui fonctionne. Ce ne
// sont pas des choix de notre part et ce ne sont pas des inventions : ce sont des CONSTANTES
// DU PROTOCOLE, au meme titre qu'un numero de methode ou une largeur de champ. Elles
// ressemblent a des plafonds et a des delais — 30 pour des comptes, 300 pour des secondes,
// 4000 pour une taille — mais nous ne savons pas encore ce que chaque position commande, et
// il vaut mieux l'ecrire que de le laisser croire.
//
// A REMPLACER par une mesure a nous le jour ou on capturera le vrai service.
var smm2ReglagesEntiers = map[int]int32{
	0: 1, 1: 1, 2: 1, 3: 1, 4: 1, 5: 1, 6: 1,
	7: 30, 8: 30, 9: 30,
	10: 200, 11: 2, 12: 3000, 13: 4, 14: 1, 15: 6, 16: 2,
	17: 300, 18: 200, 19: 150, 20: 100, 21: 300,
	22: 6,
	23: 300, 24: 300, 25: 300, 26: 300,
	27: 100, 28: 100, 29: 20, 30: 10,
	31: 4000, 32: 10, 33: 180, 34: 10, 35: 250, 36: 5297, 37: 1,
}
