package main

import "testing"

func TestFreshPlayerMultiplayerStatsMatchCapturedNintendoValues(t *testing.T) {
	got := statsMultijoueurDe(1800000001)
	if len(got) != 15 {
		t.Fatalf("Nintendo returns 15 keys for a fresh player; got %d", len(got))
	}
	for key := uint8(0); key < 15; key++ {
		want := uint32(0)
		if key == 1 {
			want = 1
		}
		if got[key] != want {
			t.Errorf("key %d = %d, want %d", key, got[key], want)
		}
	}
}
