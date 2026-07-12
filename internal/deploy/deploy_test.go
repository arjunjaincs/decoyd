package deploy_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/arjunjaincs/decoyd/internal/deploy"
	"github.com/arjunjaincs/decoyd/internal/tokens"
)

// makeToken returns a minimal token for testing.
func makeTestToken(tokenType string) tokens.Token {
	return tokens.Token{
		ID:        "deadbeefdeadbeef",
		Type:      tokenType,
		Value:     "test-value-contents\n",
		Filename:  "testfile.txt",
		CreatedAt: time.Now().UTC(),
	}
}

// ── DeployToFile — basic write ────────────────────────────────────────────────

func TestDeployToFile_WritesFile(t *testing.T) {
	dir := t.TempDir()
	tok := makeTestToken(tokens.TypeEnvSecrets)

	res, err := deploy.DeployToFile(tok, dir, deploy.Options{})
	if err != nil {
		t.Fatalf("DeployToFile() error: %v", err)
	}
	if res.DeployedTo == "" {
		t.Error("DeployResult.DeployedTo is empty")
	}

	data, err := os.ReadFile(res.DeployedTo)
	if err != nil {
		t.Fatalf("cannot read deployed file: %v", err)
	}
	if string(data) != tok.Value {
		t.Errorf("content = %q; want %q", string(data), tok.Value)
	}
}

func TestDeployToFile_CreatesTargetDirectory(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "nested", "subdir")

	tok := makeTestToken(tokens.TypeGitHubPAT)
	_, err := deploy.DeployToFile(tok, dir, deploy.Options{})
	if err != nil {
		t.Fatalf("DeployToFile() should create nested dir: %v", err)
	}

	dest := filepath.Join(dir, tok.Filename)
	if _, err := os.Stat(dest); err != nil {
		t.Errorf("expected file at %s: %v", dest, err)
	}
}

// ── DeployToFile — overwrite guard ───────────────────────────────────────────

func TestDeployToFile_RefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	tok := makeTestToken(tokens.TypeGitHubPAT)

	// Write once.
	if _, err := deploy.DeployToFile(tok, dir, deploy.Options{}); err != nil {
		t.Fatalf("first deploy: %v", err)
	}

	// Second write must return ErrAlreadyExists.
	_, err := deploy.DeployToFile(tok, dir, deploy.Options{})
	if !errors.Is(err, deploy.ErrAlreadyExists) {
		t.Errorf("second deploy = %v; want ErrAlreadyExists", err)
	}
}

// ── DeployToFile — dry-run ────────────────────────────────────────────────────

func TestDeployToFile_DryRun_NothingWritten(t *testing.T) {
	dir := t.TempDir()
	tok := makeTestToken(tokens.TypeKubeconfig)

	res, err := deploy.DeployToFile(tok, dir, deploy.Options{DryRun: true})
	if err != nil {
		t.Fatalf("DryRun deploy error: %v", err)
	}
	if !res.DryRun {
		t.Error("DryRun result should have DryRun=true")
	}
	if !res.WouldCreate {
		t.Error("DryRun result should have WouldCreate=true")
	}

	// No file should have been created.
	dest := filepath.Join(dir, tok.Filename)
	if _, err := os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("DryRun must not create file; got stat err = %v", err)
	}
}

func TestDeployToFile_DryRun_AlsoChecksOverwrite(t *testing.T) {
	dir := t.TempDir()
	tok := makeTestToken(tokens.TypeSlackToken)

	// Write first for real.
	if _, err := deploy.DeployToFile(tok, dir, deploy.Options{}); err != nil {
		t.Fatalf("first deploy: %v", err)
	}

	// DryRun on an existing file still returns ErrAlreadyExists.
	_, err := deploy.DeployToFile(tok, dir, deploy.Options{DryRun: true})
	if !errors.Is(err, deploy.ErrAlreadyExists) {
		t.Errorf("DryRun on existing file = %v; want ErrAlreadyExists", err)
	}
}

// ── Permission bits (Linux only) ─────────────────────────────────────────────

func TestDeployToFile_PermissionsSecret(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits not enforced on Windows")
	}
	dir := t.TempDir()
	tok := makeTestToken(tokens.TypeAWSCredentials)
	tok.Filename = "credentials"

	res, err := deploy.DeployToFile(tok, dir, deploy.Options{})
	if err != nil {
		t.Fatalf("deploy error: %v", err)
	}

	info, err := os.Stat(res.DeployedTo)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	got := info.Mode().Perm()
	want := os.FileMode(0o600)
	if got != want {
		t.Errorf("perm = %04o; want %04o", got, want)
	}
}

func TestDeployToFile_PermissionsPublic(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits not enforced on Windows")
	}
	dir := t.TempDir()
	tok := makeTestToken(tokens.TypeDBDump)
	tok.Filename = "backup.sql"

	res, err := deploy.DeployToFile(tok, dir, deploy.Options{})
	if err != nil {
		t.Fatalf("deploy error: %v", err)
	}

	info, err := os.Stat(res.DeployedTo)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	got := info.Mode().Perm()
	want := os.FileMode(0o644)
	if got != want {
		t.Errorf("perm = %04o; want %04o", got, want)
	}
}

// ── PermForType ───────────────────────────────────────────────────────────────

func TestPermForType_SecretTypes(t *testing.T) {
	secretTypes := []string{
		tokens.TypeAWSCredentials,
		tokens.TypeSSHKey,
		tokens.TypeGitHubPAT,
		tokens.TypeSlackToken,
		tokens.TypeEnvSecrets,
	}
	for _, tt := range secretTypes {
		t.Run(tt, func(t *testing.T) {
			got := deploy.PermForType(tt)
			if got != 0o600 {
				t.Errorf("PermForType(%q) = %04o; want 0600", tt, got)
			}
		})
	}
}

func TestPermForType_PublicTypes(t *testing.T) {
	publicTypes := []string{
		tokens.TypeKubeconfig,
		tokens.TypeDBDump,
		tokens.TypeDNSCanary,
	}
	for _, tt := range publicTypes {
		t.Run(tt, func(t *testing.T) {
			got := deploy.PermForType(tt)
			if got != 0o644 {
				t.Errorf("PermForType(%q) = %04o; want 0644", tt, got)
			}
		})
	}
}

// ── SanitizePath ─────────────────────────────────────────────────────────────

func TestSanitizePath_TildeExpansion(t *testing.T) {
	home, _ := os.UserHomeDir()

	cases := []struct {
		input string
		want  string
	}{
		{"~/Documents", filepath.Join(home, "Documents")},
		{"~", home},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got, err := deploy.SanitizePath(tc.input)
			if err != nil {
				t.Fatalf("SanitizePath(%q) error: %v", tc.input, err)
			}
			if got != tc.want {
				t.Errorf("got %q; want %q", got, tc.want)
			}
		})
	}
}

// ── SSH keypair deploy ───────────────────────────────────────────────────────

func TestDeployToFile_SSHKey_WritesBothFiles(t *testing.T) {
	dir := t.TempDir()
	tok, err := tokens.GenerateSSHKey()
	if err != nil {
		t.Fatalf("GenerateSSHKey() error: %v", err)
	}

	res, err := deploy.DeployToFile(tok, dir, deploy.Options{})
	if err != nil {
		t.Fatalf("DeployToFile(SSH) error: %v", err)
	}

	// Primary path must be id_ed25519.
	if !strings.HasSuffix(res.DeployedTo, "id_ed25519") {
		t.Errorf("DeployedTo = %q; want suffix id_ed25519", res.DeployedTo)
	}
	// ExtraFiles must contain id_ed25519.pub.
	if len(res.ExtraFiles) != 1 || !strings.HasSuffix(res.ExtraFiles[0], "id_ed25519.pub") {
		t.Errorf("ExtraFiles = %v; want one entry ending in id_ed25519.pub", res.ExtraFiles)
	}

	// Both files must exist on disk.
	for _, path := range append([]string{res.DeployedTo}, res.ExtraFiles...) {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected file %s: %v", path, err)
		}
	}

	// Private key must start with PEM header.
	privData, _ := os.ReadFile(res.DeployedTo)
	if !strings.HasPrefix(string(privData), "-----BEGIN OPENSSH PRIVATE KEY-----") {
		t.Errorf("private key content does not start with PEM header")
	}

	// Public key line must start with ssh-ed25519.
	pubData, _ := os.ReadFile(res.ExtraFiles[0])
	if !strings.HasPrefix(string(pubData), "ssh-ed25519 ") {
		t.Errorf("public key content = %q; want prefix ssh-ed25519", string(pubData))
	}
}

func TestDeployToFile_SSHKey_PrivPerms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits not enforced on Windows")
	}
	dir := t.TempDir()
	tok, _ := tokens.GenerateSSHKey()
	res, err := deploy.DeployToFile(tok, dir, deploy.Options{})
	if err != nil {
		t.Fatalf("deploy error: %v", err)
	}
	info, _ := os.Stat(res.DeployedTo)
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("id_ed25519 perm = %04o; want 0600", got)
	}
}

func TestDeployToFile_SSHKey_PubPerms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits not enforced on Windows")
	}
	dir := t.TempDir()
	tok, _ := tokens.GenerateSSHKey()
	res, err := deploy.DeployToFile(tok, dir, deploy.Options{})
	if err != nil {
		t.Fatalf("deploy error: %v", err)
	}
	info, _ := os.Stat(res.ExtraFiles[0])
	if got := info.Mode().Perm(); got != 0o644 {
		t.Errorf("id_ed25519.pub perm = %04o; want 0644", got)
	}
}

func TestDeployToFile_SSHKey_DryRun(t *testing.T) {
	dir := t.TempDir()
	tok, _ := tokens.GenerateSSHKey()
	res, err := deploy.DeployToFile(tok, dir, deploy.Options{DryRun: true})
	if err != nil {
		t.Fatalf("dry-run error: %v", err)
	}
	if !res.DryRun || !res.WouldCreate {
		t.Error("DryRun result flags not set")
	}
	// Neither file should exist.
	for _, p := range append([]string{res.DeployedTo}, res.ExtraFiles...) {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("DryRun must not create %s", p)
		}
	}
}

// ── EmptyTargetDir guard ──────────────────────────────────────────────────────

func TestDeployToFile_EmptyDir_Error(t *testing.T) {
	tok := makeTestToken(tokens.TypeDNSCanary)
	_, err := deploy.DeployToFile(tok, "", deploy.Options{})
	if err == nil {
		t.Error("DeployToFile with empty targetDir should return error")
	}
}
