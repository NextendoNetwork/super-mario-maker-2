package main

import "testing"

func TestVersusSelectionUsesCoursesWithCompletePlayStats(t *testing.T) {
	oldCourses, oldResults := courses, resultats
	oldChoices, oldMatchmaking := choixParties, MatchmakingSMM2
	defer func() {
		courses, resultats = oldCourses, oldResults
		choixParties, MatchmakingSMM2 = oldChoices, oldMatchmaking
	}()

	courses = &courseStore{byID: map[uint64]*courseMeta{
		1: {DataID: 1, Name: "unplayed", Ready: true},
		2: {DataID: 2, Name: "played without a clear", Ready: true},
		3: {DataID: 3, Name: "completed", Ready: true},
	}}
	resultats = &magasinResultats{parNiv: map[uint64]*resultatNiveau{
		2: {Parties: 1, Tentatives: 2},
		3: {Parties: 2, Tentatives: 3, Reussites: 1},
	}}
	choixParties = map[uint32]choixPartie{}
	MatchmakingSMM2 = nil

	got := niveauxChoisisPourVersus(42, 1)
	if len(got) != 1 || got[0].DataID != 3 {
		t.Fatalf("versus chose an untested course: %+v", got)
	}
	// The co-op pool retains all public courses.
	choixParties = map[uint32]choixPartie{}
	if got := niveauxChoisisPourPartie(42, 3); len(got) != 3 {
		t.Fatalf("co-op pool changed: %d courses", len(got))
	}
	resultats.parNiv = map[uint64]*resultatNiveau{}
	choixParties = map[uint32]choixPartie{}
	if got := niveauxChoisisPourVersus(42, 1); len(got) != 0 {
		t.Fatalf("versus fell back to untested courses: %+v", got)
	}
}
