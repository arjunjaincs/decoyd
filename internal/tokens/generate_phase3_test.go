package tokens_test

import (
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"

	"github.com/arjunjaincs/decoyd/internal/tokens"
)

func TestGenerateNpmRC(t *testing.T) {
	tok, err := tokens.Generate(tokens.TypeNpmRC)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tok.Value, "npm_") {
		t.Errorf("npmrc value missing npm_ prefix: %q", tok.Value[:min(len(tok.Value), 60)])
	}
	if tok.Filename != ".npmrc" {
		t.Errorf("Filename = %q; want .npmrc", tok.Filename)
	}
}

func TestGeneratePypiRC(t *testing.T) {
	tok, err := tokens.Generate(tokens.TypePypiRC)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tok.Value, "pypi-") {
		t.Errorf("pypirc value missing pypi- prefix: %q", tok.Value[:min(len(tok.Value), 60)])
	}
	if tok.Filename != ".pypirc" {
		t.Errorf("Filename = %q; want .pypirc", tok.Filename)
	}
}

func TestGenerateDockerConfig(t *testing.T) {
	tok, err := tokens.Generate(tokens.TypeDockerConfig)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(tok.Value), &cfg); err != nil {
		t.Errorf("docker config not valid JSON: %v", err)
	}
	if _, ok := cfg["auths"]; !ok {
		t.Error("docker config missing 'auths' key")
	}
	if tok.Filename != "config.json" {
		t.Errorf("Filename = %q; want config.json", tok.Filename)
	}
}

func TestGenerateGitCredentials(t *testing.T) {
	tok, err := tokens.Generate(tokens.TypeGitCredentials)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tok.Value, "https://user:") {
		t.Errorf("git-credentials missing https://user: prefix: %q", tok.Value[:min(len(tok.Value), 60)])
	}
	if !strings.Contains(tok.Value, "github.com") {
		t.Errorf("git-credentials missing github.com: %q", tok.Value[:min(len(tok.Value), 80)])
	}
}

func TestGenerateNetrc(t *testing.T) {
	tok, err := tokens.Generate(tokens.TypeNetrc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tok.Value, "machine ") {
		t.Errorf("netrc missing 'machine' field: %q", tok.Value[:min(len(tok.Value), 60)])
	}
	if tok.Filename != ".netrc" {
		t.Errorf("Filename = %q; want .netrc", tok.Filename)
	}
}

func TestGenerateRSAKey(t *testing.T) {
	tok, err := tokens.Generate(tokens.TypeRSAKey)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode([]byte(tok.Value))
	if block == nil {
		t.Fatal("rsa key: no PEM block found")
	}
	if block.Type != "RSA PRIVATE KEY" {
		t.Errorf("PEM type = %q; want RSA PRIVATE KEY", block.Type)
	}
	if tok.Filename != "id_rsa" {
		t.Errorf("Filename = %q; want id_rsa", tok.Filename)
	}
}

func TestGenerateGCPServiceAccount(t *testing.T) {
	tok, err := tokens.Generate(tokens.TypeGCPServiceAccount)
	if err != nil {
		t.Fatal(err)
	}
	var sa map[string]any
	if err := json.Unmarshal([]byte(tok.Value), &sa); err != nil {
		t.Fatalf("GCP SA not valid JSON: %v", err)
	}
	if sa["type"] != "service_account" {
		t.Errorf("type = %v; want service_account", sa["type"])
	}
	keyID, _ := sa["private_key_id"].(string)
	if len(keyID) != 40 {
		t.Errorf("private_key_id len = %d; want 40", len(keyID))
	}
	clientID, _ := sa["client_id"].(string)
	if len(clientID) != 21 {
		t.Errorf("client_id len = %d; want 21", len(clientID))
	}
	privateKey, _ := sa["private_key"].(string)
	block, _ := pem.Decode([]byte(privateKey))
	if block == nil || block.Type != "PRIVATE KEY" {
		t.Errorf("private_key not a valid PKCS#8 PEM")
	}
}

func TestGenerateTerraformCreds(t *testing.T) {
	tok, err := tokens.Generate(tokens.TypeTerraformCreds)
	if err != nil {
		t.Fatal(err)
	}
	var creds map[string]any
	if err := json.Unmarshal([]byte(tok.Value), &creds); err != nil {
		t.Fatalf("terraform creds not valid JSON: %v", err)
	}
	if _, ok := creds["credentials"]; !ok {
		t.Error("terraform creds missing 'credentials' key")
	}
	if tok.Filename != "credentials.tfrc.json" {
		t.Errorf("Filename = %q; want credentials.tfrc.json", tok.Filename)
	}
}

func TestGenerateVaultToken(t *testing.T) {
	tok, err := tokens.Generate(tokens.TypeVaultToken)
	if err != nil {
		t.Fatal(err)
	}
	val := strings.TrimSpace(tok.Value)
	if !strings.HasPrefix(val, "s.") {
		t.Errorf("vault token value = %q; want s.xxx prefix", val[:min(len(val), 30)])
	}
	if len(val) != 26 { // "s." + 24 chars
		t.Errorf("vault token len = %d; want 26 (s.+24)", len(val))
	}
}

func TestGenerateMCPConfig(t *testing.T) {
	tok, err := tokens.Generate(tokens.TypeMCPConfig)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(tok.Value), &cfg); err != nil {
		t.Fatalf("mcp config not valid JSON: %v", err)
	}
	servers, ok := cfg["mcpServers"].(map[string]any)
	if !ok {
		t.Fatal("mcp config missing mcpServers")
	}
	github, ok := servers["github"].(map[string]any)
	if !ok {
		t.Fatal("mcp config missing github server")
	}
	env, ok := github["env"].(map[string]any)
	if !ok {
		t.Fatal("mcp config github server missing env")
	}
	pat, _ := env["GITHUB_PERSONAL_ACCESS_TOKEN"].(string)
	if !strings.HasPrefix(pat, "ghp_") {
		t.Errorf("MCP config PAT = %q; want ghp_ prefix", pat[:min(len(pat), 20)])
	}
}

// min helper (Go 1.21+ has builtin but kept explicit for compat)
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
