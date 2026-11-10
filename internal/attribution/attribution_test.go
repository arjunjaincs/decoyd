package attribution_test

import (
	"encoding/json"
	"testing"

	"github.com/arjunjaincs/decoyd/internal/attribution"
)

func TestConfidence_String(t *testing.T) {
	cases := []struct {
		c    attribution.Confidence
		want string
	}{
		{attribution.ConfidenceLow, "low"},
		{attribution.ConfidenceMedium, "medium"},
		{attribution.ConfidenceHigh, "high"},
		{attribution.Confidence(99), "unknown"},
	}
	for _, tc := range cases {
		if got := tc.c.String(); got != tc.want {
			t.Errorf("Confidence(%d).String() = %q, want %q", tc.c, got, tc.want)
		}
	}
}

func TestPackageConfidence_String(t *testing.T) {
	cases := []struct {
		c    attribution.PackageConfidence
		want string
	}{
		{attribution.PkgConfidenceNone, "none"},
		{attribution.PkgConfidenceLow, "low"},
		{attribution.PkgConfidenceMedium, "medium"},
		{attribution.PkgConfidenceHigh, "high"},
	}
	for _, tc := range cases {
		if got := tc.c.String(); got != tc.want {
			t.Errorf("PackageConfidence(%d).String() = %q, want %q", tc.c, got, tc.want)
		}
	}
}

func TestAttribution_JSONRoundtrip(t *testing.T) {
	a := attribution.Attribution{
		Accessor: attribution.ProcessInfo{
			PID:       1234,
			PPID:      1000,
			Comm:      "node",
			Exe:       "/usr/bin/node",
			Cmdline:   "node install.js",
			Cwd:       "/home/user/app",
			UID:       1000,
			StartTicks: 12345678,
			ExeSHA256: "abc123",
		},
		Ancestors: attribution.AncestorChain{
			{PID: 1000, PPID: 999, Comm: "npm", Exe: "/usr/bin/npm"},
		},
		Confidence:        attribution.ConfidenceHigh,
		NpmPackage:        "evil-pkg",
		NpmVersion:        "1.0.0",
		NpmLifecycleEvent: "postinstall",
	}

	b, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got attribution.Attribution
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.Accessor.PID != a.Accessor.PID {
		t.Errorf("PID mismatch: got %d, want %d", got.Accessor.PID, a.Accessor.PID)
	}
	if got.Confidence != a.Confidence {
		t.Errorf("Confidence mismatch: got %d, want %d", got.Confidence, a.Confidence)
	}
	if got.NpmLifecycleEvent != a.NpmLifecycleEvent {
		t.Errorf("NpmLifecycleEvent mismatch: got %q, want %q", got.NpmLifecycleEvent, a.NpmLifecycleEvent)
	}
	if len(got.Ancestors) != 1 {
		t.Errorf("Ancestors len mismatch: got %d, want 1", len(got.Ancestors))
	}
}

func TestAttribution_OmitEmpty(t *testing.T) {
	// Low-confidence attribution with no npm fields should not marshal npm keys.
	a := attribution.Attribution{
		Accessor:   attribution.ProcessInfo{PID: 1, Comm: "cat"},
		Confidence: attribution.ConfidenceLow,
	}
	b, _ := json.Marshal(a)
	s := string(b)
	for _, key := range []string{"npm_package", "npm_version", "npm_lifecycle_event", "ancestors", "exe_deleted", "exe_sha256"} {
		if contains(s, `"`+key+`"`) {
			t.Errorf("JSON should omit %q for empty Attribution, but it appears in: %s", key, s)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsAt(s, sub))
}

func containsAt(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestPackageAttribution_HighConfidence(t *testing.T) {
	pa := attribution.PackageAttribution{
		Culprit:    "likely culprit: evil-pkg@1.0.0 (npm install hook, high confidence)",
		Name:       "evil-pkg",
		Version:    "1.0.0",
		Context:    "npm install hook",
		Confidence: attribution.PkgConfidenceHigh,
	}
	if pa.Confidence.String() != "high" {
		t.Errorf("expected high confidence string, got %q", pa.Confidence.String())
	}
	if pa.Name != "evil-pkg" {
		t.Errorf("unexpected name %q", pa.Name)
	}
}

func TestProcessInfo_ExeDeleted(t *testing.T) {
	pi := attribution.ProcessInfo{
		PID:        42,
		Exe:        "/usr/bin/python3",
		ExeDeleted: true,
	}
	b, _ := json.Marshal(pi)
	if !containsAt(string(b), `"exe_deleted":true`) {
		t.Errorf("ExeDeleted=true should appear in JSON, got: %s", b)
	}
}
