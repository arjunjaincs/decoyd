package tokens_test

import (
	"regexp"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
	"gopkg.in/yaml.v3"

	"github.com/arjunjaincs/decoyd/internal/tokens"
)

// ── NewID ────────────────────────────────────────────────────────────────────

// TestNewID_Format verifies the returned ID is exactly 16 lowercase hex chars.
func TestNewID_Format(t *testing.T) {
	id, err := tokens.NewID()
	if err != nil {
		t.Fatalf("NewID() error: %v", err)
	}
	if len(id) != 16 {
		t.Errorf("NewID() len = %d; want 16", len(id))
	}
	matched, _ := regexp.MatchString(`^[0-9a-f]{16}$`, id)
	if !matched {
		t.Errorf("NewID() = %q; want 16 lowercase hex chars", id)
	}
}

// TestNewID_Collision generates 1 000 IDs and asserts no duplicates.
func TestNewID_Collision(t *testing.T) {
	const n = 1000
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		id, err := tokens.NewID()
		if err != nil {
			t.Fatalf("NewID() #%d error: %v", i, err)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate ID %q generated at iteration %d", id, i)
		}
		seen[id] = struct{}{}
	}
}

// TestNewID_Concurrent checks NewID is safe to call from multiple goroutines.
func TestNewID_Concurrent(t *testing.T) {
	const goroutines = 20
	const perGoroutine = 50

	var mu sync.Mutex
	seen := make(map[string]struct{}, goroutines*perGoroutine)
	var wg sync.WaitGroup

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				id, err := tokens.NewID()
				if err != nil {
					t.Errorf("NewID() goroutine error: %v", err)
					return
				}
				mu.Lock()
				seen[id] = struct{}{}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if len(seen) != goroutines*perGoroutine {
		t.Errorf("collision detected: got %d unique IDs, want %d", len(seen), goroutines*perGoroutine)
	}
}

// ── AWS Credentials ──────────────────────────────────────────────────────────

func TestGenerateAWSCredentials_Format(t *testing.T) {
	tok, err := tokens.GenerateAWSCredentials()
	if err != nil {
		t.Fatalf("GenerateAWSCredentials() error: %v", err)
	}
	if tok.Type != tokens.TypeAWSCredentials {
		t.Errorf("Type = %q; want %q", tok.Type, tokens.TypeAWSCredentials)
	}

	// Access key ID must match AKIA[A-Z0-9]{16}
	re := regexp.MustCompile(`aws_access_key_id = (AKIA[A-Z2-7]{16})`)
	m := re.FindStringSubmatch(tok.Value)
	if m == nil {
		t.Errorf("aws_access_key_id not found or wrong format in:\n%s", tok.Value)
	}

	// Secret must be present and 40 chars.
	reSecret := regexp.MustCompile(`aws_secret_access_key = (.{40})`)
	ms := reSecret.FindStringSubmatch(tok.Value)
	if ms == nil {
		t.Errorf("aws_secret_access_key not found or wrong length in:\n%s", tok.Value)
	}

	if tok.Filename != "credentials" {
		t.Errorf("Filename = %q; want %q", tok.Filename, "credentials")
	}
}

// ── SSH Private Key ──────────────────────────────────────────────────────────

func TestGenerateSSHKey_ParsesOK(t *testing.T) {
	tok, err := tokens.GenerateSSHKey()
	if err != nil {
		t.Fatalf("GenerateSSHKey() error: %v", err)
	}
	if tok.Type != tokens.TypeSSHKey {
		t.Errorf("Type = %q; want %q", tok.Type, tokens.TypeSSHKey)
	}

	// Split Value at the sentinel.
	parts := strings.SplitN(tok.Value, "---PUBLIC KEY---\n", 2)
	if len(parts) != 2 {
		t.Fatalf("SSH Value missing sentinel separator")
	}
	privPEM := []byte(parts[0])

	// ParseRawPrivateKey must succeed.
	signer, err := ssh.ParseRawPrivateKey(privPEM)
	if err != nil {
		t.Fatalf("ssh.ParseRawPrivateKey() error: %v", err)
	}
	if signer == nil {
		t.Error("parsed signer is nil")
	}

	// Public key line must start with ssh-ed25519.
	pubLine := strings.TrimSpace(parts[1])
	if !strings.HasPrefix(pubLine, "ssh-ed25519 ") {
		t.Errorf("public key line = %q; want prefix ssh-ed25519", pubLine)
	}

	if tok.Filename != "id_ed25519" {
		t.Errorf("Filename = %q; want id_ed25519", tok.Filename)
	}
}

// ── .env Secrets ─────────────────────────────────────────────────────────────

func TestGenerateEnvSecrets_Format(t *testing.T) {
	tok, err := tokens.GenerateEnvSecrets()
	if err != nil {
		t.Fatalf("GenerateEnvSecrets() error: %v", err)
	}

	required := []string{
		"DATABASE_URL=",
		"STRIPE_SECRET_KEY=sk_live_",
		"JWT_SECRET=",
	}
	for _, sub := range required {
		if !strings.Contains(tok.Value, sub) {
			t.Errorf("missing %q in .env output", sub)
		}
	}
	if tok.Filename != ".env" {
		t.Errorf("Filename = %q; want .env", tok.Filename)
	}
}

// ── GitHub PAT ───────────────────────────────────────────────────────────────

func TestGenerateGitHubPAT_Format(t *testing.T) {
	tok, err := tokens.GenerateGitHubPAT()
	if err != nil {
		t.Fatalf("GenerateGitHubPAT() error: %v", err)
	}
	re := regexp.MustCompile(`^ghp_[A-Za-z0-9]{36}$`)
	if !re.MatchString(tok.Value) {
		t.Errorf("GitHub PAT = %q; want ghp_ + 36 alphanumeric", tok.Value)
	}
}

// ── Slack Token ───────────────────────────────────────────────────────────────

func TestGenerateSlackToken_Format(t *testing.T) {
	tok, err := tokens.GenerateSlackToken()
	if err != nil {
		t.Fatalf("GenerateSlackToken() error: %v", err)
	}
	re := regexp.MustCompile(`^xoxb-[0-9]{10}-[0-9]{11}-[A-Za-z0-9]{24}$`)
	if !re.MatchString(tok.Value) {
		t.Errorf("Slack token = %q; want xoxb-<10>-<11>-<24>", tok.Value)
	}
}

// ── Kubeconfig ────────────────────────────────────────────────────────────────

func TestGenerateKubeconfig_ValidYAML(t *testing.T) {
	tok, err := tokens.GenerateKubeconfig()
	if err != nil {
		t.Fatalf("GenerateKubeconfig() error: %v", err)
	}

	// Parse as generic YAML map.
	var doc map[string]interface{}
	if err := yaml.Unmarshal([]byte(tok.Value), &doc); err != nil {
		t.Fatalf("kubeconfig is not valid YAML: %v", err)
	}

	// Check required top-level keys.
	for _, key := range []string{"apiVersion", "kind", "clusters", "contexts", "users"} {
		if _, ok := doc[key]; !ok {
			t.Errorf("kubeconfig missing top-level key %q", key)
		}
	}
	if doc["apiVersion"] != "v1" {
		t.Errorf("apiVersion = %v; want v1", doc["apiVersion"])
	}
	if doc["kind"] != "Config" {
		t.Errorf("kind = %v; want Config", doc["kind"])
	}
}

// ── DB Dump ───────────────────────────────────────────────────────────────────

func TestGenerateDBDump_Format(t *testing.T) {
	tok, err := tokens.GenerateDBDump()
	if err != nil {
		t.Fatalf("GenerateDBDump() error: %v", err)
	}
	required := []string{
		"PostgreSQL database dump",
		"CREATE TABLE public.users",
		"INSERT INTO public.users",
		"password_hash",
	}
	for _, sub := range required {
		if !strings.Contains(tok.Value, sub) {
			t.Errorf("DB dump missing expected content %q", sub)
		}
	}
	if tok.Filename != "backup.sql" {
		t.Errorf("Filename = %q; want backup.sql", tok.Filename)
	}
}

// ── DNS Canary ────────────────────────────────────────────────────────────────

func TestGenerateDNSCanary_LabelFormat(t *testing.T) {
	tok, err := tokens.GenerateDNSCanary()
	if err != nil {
		t.Fatalf("GenerateDNSCanary() error: %v", err)
	}

	// Extract the label from "label=<value>".
	re := regexp.MustCompile(`label=([a-z0-9]{16})`)
	m := re.FindStringSubmatch(tok.Value)
	if m == nil {
		t.Errorf("DNS canary label not found or wrong format in:\n%s", tok.Value)
	}

	if tok.Type != tokens.TypeDNSCanary {
		t.Errorf("Type = %q; want %q", tok.Type, tokens.TypeDNSCanary)
	}
}

// TestGenerateDNSCanary_LabelUniqueness generates 1 000 DNS canary tokens and
// checks that all labels are distinct.
func TestGenerateDNSCanary_LabelUniqueness(t *testing.T) {
	const n = 1000
	re := regexp.MustCompile(`label=([a-z0-9]{16})`)
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		tok, err := tokens.GenerateDNSCanary()
		if err != nil {
			t.Fatalf("GenerateDNSCanary() #%d error: %v", i, err)
		}
		m := re.FindStringSubmatch(tok.Value)
		if m == nil {
			t.Fatalf("label not found in token %d", i)
		}
		label := m[1]
		if _, dup := seen[label]; dup {
			t.Fatalf("duplicate label %q at iteration %d", label, i)
		}
		seen[label] = struct{}{}
	}
}

// ── Fingerprint denylist ─────────────────────────────────────────────────────

// TestNoStaticIdentifiers generates 200 tokens of every type and fails if any
// token value or filename matches the denylist of static identifiers.
// Rule 12: NO static identifiers ("decoyd", "canary", "decoy" etc.) in generated files.
//
// High-entropy random values (16+ consecutive base64/alphanumeric chars) are stripped
// before checking the denylist — the check applies to structural/template text only,
// not to cryptographically random values that could accidentally spell a word.
// Use -count=200 to get enough samples to catch accidental template leakage.
func TestNoStaticIdentifiers(t *testing.T) {
	// highEntropyRE strips runs of 16+ base64/alphanumeric chars — these are random
	// values that cannot be banned from templates (Rule 12 applies to template text,
	// not to random entropy values). After stripping, the remaining structural text
	// (labels, comments, format strings, YAML keys) must not contain banned phrases.
	highEntropyRE := regexp.MustCompile(`[A-Za-z0-9+/=_\-]{16,}`)

	// exactPhrases must NOT appear in structural text (case-insensitive substring match).
	exactPhrases := []string{
		"decoy",
		"canary",
		"decoyd",
		"# Create a DNS",
		"# Label:",
		"Decoyd will",
		"dns_canary",
	}

	// wordPatterns must NOT appear as whole words in structural text.
	wordPatterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bfake\b`),
		regexp.MustCompile(`(?i)\bexample\b`),
	}

	allTypes := []string{
		tokens.TypeAWSCredentials,
		tokens.TypeSSHKey,
		tokens.TypeEnvSecrets,
		tokens.TypeGitHubPAT,
		tokens.TypeSlackToken,
		tokens.TypeKubeconfig,
		tokens.TypeDBDump,
		tokens.TypeDNSCanary,
		// Phase 3 types
		tokens.TypeNpmRC,
		tokens.TypePypiRC,
		tokens.TypeDockerConfig,
		tokens.TypeGitCredentials,
		tokens.TypeNetrc,
		// TypeRSAKey excluded: RSA-2048 keygen is too slow for 200x sampling
		// TypeGCPServiceAccount excluded: RSA-2048 keygen is too slow for 200x
		tokens.TypeTerraformCreds,
		tokens.TypeVaultToken,
		tokens.TypeMCPConfig,
	}

	const n = 200
	for _, tt := range allTypes {
		for i := 0; i < n; i++ {
			tok, err := tokens.Generate(tt)
			if err != nil {
				t.Fatalf("Generate(%q) #%d error: %v", tt, i, err)
			}
			// Strip high-entropy runs before denylist check.
			structural := highEntropyRE.ReplaceAllString(tok.Value, "")
			for _, phrase := range exactPhrases {
				lo := strings.ToLower(phrase)
				if strings.Contains(strings.ToLower(structural), lo) {
					t.Errorf("type=%s token[%d] structural text contains banned phrase %q (stripped excerpt: %.120q)",
						tt, i, phrase, structural)
				}
				if strings.Contains(strings.ToLower(tok.Filename), lo) {
					t.Errorf("type=%s token[%d] Filename=%q contains banned phrase %q",
						tt, i, tok.Filename, phrase)
				}
			}
			for _, re := range wordPatterns {
				if re.MatchString(structural) {
					t.Errorf("type=%s token[%d] structural text matches banned word pattern %q (stripped excerpt: %.120q)",
						tt, i, re.String(), structural)
				}
				if re.MatchString(tok.Filename) {
					t.Errorf("type=%s token[%d] Filename=%q matches banned word pattern %q",
						tt, i, tok.Filename, re.String())
				}
			}
		}
	}
}

// ── Generate dispatch ─────────────────────────────────────────────────────────

func TestGenerate_UnknownType(t *testing.T) {
	_, err := tokens.Generate("totally_unknown_type")
	if err == nil {
		t.Error("Generate(unknown) should return an error")
	}
}

func TestGenerate_AllTypes(t *testing.T) {
	allTypes := []string{
		tokens.TypeAWSCredentials,
		tokens.TypeSSHKey,
		tokens.TypeEnvSecrets,
		tokens.TypeGitHubPAT,
		tokens.TypeSlackToken,
		tokens.TypeKubeconfig,
		tokens.TypeDBDump,
		tokens.TypeDNSCanary,
		// Phase 3 types
		tokens.TypeNpmRC,
		tokens.TypePypiRC,
		tokens.TypeDockerConfig,
		tokens.TypeGitCredentials,
		tokens.TypeNetrc,
		// TypeRSAKey excluded: RSA-2048 keygen is too slow for 200x sampling
		// TypeGCPServiceAccount excluded: RSA-2048 keygen is too slow for 200x
		tokens.TypeTerraformCreds,
		tokens.TypeVaultToken,
		tokens.TypeMCPConfig,
	}
	for _, tt := range allTypes {
		t.Run(tt, func(t *testing.T) {
			tok, err := tokens.Generate(tt)
			if err != nil {
				t.Fatalf("Generate(%q) error: %v", tt, err)
			}
			if tok.ID == "" {
				t.Error("generated token has empty ID")
			}
			if tok.Type != tt {
				t.Errorf("Type = %q; want %q", tok.Type, tt)
			}
			if tok.Value == "" {
				t.Error("generated token has empty Value")
			}
			if tok.Filename == "" {
				t.Error("generated token has empty Filename")
			}
			if tok.CreatedAt.IsZero() {
				t.Error("generated token has zero CreatedAt")
			}
		})
	}
}
