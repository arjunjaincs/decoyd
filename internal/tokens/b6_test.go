package tokens_test

import (
	"strings"
	"testing"

	"github.com/arjunjaincs/decoyd/internal/tokens"
)

// ── GitHub PAT checksum (B6) ─────────────────────────────────────────────────

// TestGitHubPAT_ChecksumTestVector verifies the CRC32-IEEE base62 checksum
// against the published test vector.
// Test vector: entropy "zQWBuTSOoRi4A9spHcVY5ncnsDkxkJ" → checksum "0mLq17"
// Source: https://github.blog/security/application-security/behind-githubs-new-authentication-token-formats/
// (checksum split and dictionary from third-party reference implementation)
func TestGitHubPAT_ChecksumTestVector(t *testing.T) {
	const (
		wantEntropy  = "zQWBuTSOoRi4A9spHcVY5ncnsDkxkJ"
		wantChecksum = "0mLq17"
	)
	if got := tokens.GHPChecksumForTest(wantEntropy); got != wantChecksum {
		t.Errorf("GHPChecksum(%q) = %q; want %q", wantEntropy, got, wantChecksum)
	}
}

// TestGitHubPAT_Format verifies length and checksum of generated PATs.
// Format: ghp_ + 30 entropy chars + 6 checksum chars = 40 total.
func TestGitHubPAT_Format(t *testing.T) {
	for i := 0; i < 50; i++ {
		tok, err := tokens.Generate(tokens.TypeGitHubPAT)
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		val := tok.Value
		if !strings.HasPrefix(val, "ghp_") {
			t.Errorf("[%d] want ghp_ prefix, got %q", i, val)
			continue
		}
		body := val[4:]
		if len(body) != 36 {
			t.Errorf("[%d] body len=%d; want 36 (30 entropy + 6 checksum)", i, len(body))
			continue
		}
		entropy, cs := body[:30], body[30:]
		if want := tokens.GHPChecksumForTest(entropy); cs != want {
			t.Errorf("[%d] checksum=%q want=%q (entropy=%q)", i, cs, want, entropy)
		}
	}
}

// ── SSH key comment (B6) ─────────────────────────────────────────────────────

// TestSSHKey_NoIdentifierInComment verifies no identifying comment in pubkey line.
// Rule 12: no static identifiers in generated token values.
func TestSSHKey_NoIdentifierInComment(t *testing.T) {
	bannedInComment := []string{"decoy", "canary", "decoyd", "fake", "example"}
	for i := 0; i < 20; i++ {
		tok, err := tokens.Generate(tokens.TypeSSHKey)
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		parts := strings.SplitN(tok.Value, "---PUBLIC KEY---\n", 2)
		if len(parts) != 2 {
			t.Fatalf("[%d] malformed SSH token: no sentinel", i)
		}
		pubLine := strings.TrimSpace(parts[1])
		// authorized_keys format: type base64data [comment]
		fields := strings.Fields(pubLine)
		if len(fields) > 2 {
			comment := strings.Join(fields[2:], " ")
			lo := strings.ToLower(comment)
			for _, banned := range bannedInComment {
				if strings.Contains(lo, banned) {
					t.Errorf("[%d] SSH pubkey comment %q contains banned phrase %q", i, comment, banned)
				}
			}
		}
	}
}

// ── Kubeconfig plausible names (B6) ──────────────────────────────────────────

// TestKubeconfig_PlausibleNames verifies plausible cluster/user names rather
// than random 8-char gibberish strings.
func TestKubeconfig_PlausibleNames(t *testing.T) {
	plausiblePrefixes := []string{
		"prod-eks-", "prod-gke-", "prod-aks-",
		"staging-eks-", "staging-gke-", "staging-aks-",
		"dev-eks-", "docker-desktop", "minikube", "kind-local", "k3s-homelab",
	}
	plausibleUsers := []string{
		"admin", "deploy-bot", "ci-runner", "cluster-admin",
		"devops", "platform-team", "sre-oncall",
	}

	for i := 0; i < 30; i++ {
		tok, err := tokens.Generate(tokens.TypeKubeconfig)
		if err != nil {
			t.Fatalf("[%d] Generate: %v", i, err)
		}
		yaml := tok.Value

		// Find the first "name:" line (cluster name in clusters block).
		clusterName := ""
		for _, line := range strings.Split(yaml, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "name: ") && clusterName == "" {
				clusterName = strings.TrimPrefix(trimmed, "name: ")
			}
		}
		if clusterName == "" {
			t.Errorf("[%d] could not extract cluster name", i)
			continue
		}
		found := false
		for _, pfx := range plausiblePrefixes {
			if strings.HasPrefix(clusterName, pfx) || clusterName == pfx {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("[%d] cluster name %q is not plausible", i, clusterName)
		}

		// Find user name from users section.
		userName := ""
		inUsers := false
		for _, line := range strings.Split(yaml, "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "users:" {
				inUsers = true
				continue
			}
			if inUsers && strings.HasPrefix(trimmed, "- name: ") {
				userName = strings.TrimPrefix(trimmed, "- name: ")
				break
			}
		}
		if userName == "" {
			t.Errorf("[%d] could not extract user name", i)
			continue
		}
		foundUser := false
		for _, u := range plausibleUsers {
			if userName == u {
				foundUser = true
				break
			}
		}
		if !foundUser {
			t.Errorf("[%d] user name %q is not plausible", i, userName)
		}
	}
}

// ── safeRandStr ban coverage (B6/B0-tokens) ──────────────────────────────────

// TestSafeRandStr_NoBannedPhrases complements TestNoStaticIdentifiers with higher
// iteration counts to verify safeRandStr rejection-sampling works correctly.
func TestSafeRandStr_NoBannedPhrases(t *testing.T) {
	banned := []string{"decoy", "canary", "decoyd", "fake", "example"}
	allTypes := []string{
		tokens.TypeAWSCredentials,
		tokens.TypeSSHKey,
		tokens.TypeEnvSecrets,
		tokens.TypeGitHubPAT,
		tokens.TypeSlackToken,
		tokens.TypeKubeconfig,
		tokens.TypeDBDump,
		tokens.TypeDNSCanary,
	}
	for _, tt := range allTypes {
		for i := 0; i < 100; i++ {
			tok, err := tokens.Generate(tt)
			if err != nil {
				t.Fatalf("Generate(%q) #%d: %v", tt, i, err)
			}
			lo := strings.ToLower(tok.Value)
			for _, p := range banned {
				if strings.Contains(lo, p) {
					t.Errorf("type=%s token[%d] contains %q: %.100q", tt, i, p, tok.Value)
				}
			}
		}
	}
}
