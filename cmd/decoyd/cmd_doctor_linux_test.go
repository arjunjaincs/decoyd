//go:build linux

package main

import "testing"

func TestReleaseFromUtsname(t *testing.T) {
	var rel [65]int8
	for i, b := range []byte("6.18.58-test") {
		rel[i] = int8(b)
	}
	if got := releaseFromUtsname(rel); got != "6.18.58-test" {
		t.Fatalf("releaseFromUtsname = %q; want 6.18.58-test", got)
	}

	// Negative signed bytes must be skipped without wraparound.
	rel = [65]int8{'6', '.', -1, '1', 0}
	if got := releaseFromUtsname(rel); got != "6.1" {
		t.Fatalf("releaseFromUtsname signed skip = %q; want 6.1", got)
	}
}

func TestFSTypeFromMagic(t *testing.T) {
	cases := []struct {
		magic int64
		want  string
	}{
		{0xEF53, "ext4"},
		{0x01021994, "tmpfs"},
		{0x794C7630, "overlayfs"},
		{0x123456789, "0x123456789"},
	}
	for _, tc := range cases {
		if got := fsTypeFromMagic(tc.magic); got != tc.want {
			t.Errorf("fsTypeFromMagic(0x%x) = %q; want %q", tc.magic, got, tc.want)
		}
	}
}
