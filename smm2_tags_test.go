package main

import "testing"

// TestTagOuZero : les etiquettes rangees par la 69 (codes decimaux) doivent ressortir dans
// CourseInfo. Avant, tagOuZero rendait zero dans tous les cas et aucune etiquette ne
// s'affichait chez nous ; chez Nintendo, « SUPER RACE!!! » porte 3 et 14.
func TestTagOuZero(t *testing.T) {
	cas := []struct {
		tags []string
		n    int
		veut uint32
	}{
		{[]string{"3", "14"}, 0, 3},
		{[]string{"3", "14"}, 1, 14},
		{[]string{"15"}, 0, 15},
		{[]string{"16"}, 0, 0},       // hors de l'enumeration
		{[]string{"speedrun"}, 0, 0}, // pas un code
		{[]string{"3"}, 1, 0},        // absente
		{nil, 0, 0},
	}
	for _, c := range cas {
		if got := tagOuZero(c.tags, c.n); got != c.veut {
			t.Errorf("tagOuZero(%q, %d) = %d, attendu %d", c.tags, c.n, got, c.veut)
		}
	}
}
