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

// ── A6: deploy hardening tests ───────────────────────────────────────────────

// TestDeploy_SecretDirPerm verifies that secret token types create directories
// with 0o700 (not 0o750 or wider).
func TestDeploy_SecretDirPerm(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permission bits not enforced on Windows")
	}
	secretTypes := []string{
		tokens.TypeAWSCredentials,
		tokens.TypeSSHKey,
		tokens.TypeGitHubPAT,
		tokens.TypeSlackToken,
		tokens.TypeEnvSecrets,
	}
	for _, tt := range secretTypes {
		tt := tt
		t.Run(tt, func(t *testing.T) {
			base := t.TempDir()
			subDir := filepath.Join(base, "secret")
			var tok tokens.Token
			if tt == tokens.TypeSSHKey {
				tok = tokens.Token{
					ID:       "deadbeefdeadbeef",
					Type:     tt,
					Value:    "-----BEGIN OPENSSH PRIVATE KEY-----\ntest\n-----END OPENSSH PRIVATE KEY-----\n---PUBLIC KEY---\nssh-ed25519 AAAAC test\n",
					Filename: "id_ed25519",
				}
			} else {
				tok = makeTestToken(tt)
			}
			_, err := deploy.DeployToFile(tok, subDir, deploy.Options{})
			if err != nil {
				t.Fatalf("DeployToFile(%s): %v", tt, err)
			}
			fi, err := os.Stat(subDir)
			if err != nil {
				t.Fatalf("stat dir: %v", err)
			}
			got := fi.Mode().Perm()
			if got != 0o700 {
				t.Errorf("dir perm for %s = %04o; want 0700", tt, got)
			}
		})
	}
}

// TestDeploy_FilePerm verifies that the deployed file has the expected mode.
func TestDeploy_FilePerm(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file permission bits not enforced on Windows")
	}
	dir := t.TempDir()
	tok := makeTestToken(tokens.TypeAWSCredentials)
	res, err := deploy.DeployToFile(tok, dir, deploy.Options{})
	if err != nil {
		t.Fatalf("DeployToFile: %v", err)
	}
	fi, err := os.Stat(res.DeployedTo)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	got := fi.Mode().Perm()
	if got != 0o600 {
		t.Errorf("file perm = %04o; want 0600", got)
	}
}

// TestDeploy_SymlinkRefusal verifies that placing a symlink at the target path
// causes deploy to fail (O_NOFOLLOW on Linux) rather than following the link.
func TestDeploy_SymlinkRefusal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on Windows")
	}
	dir := t.TempDir()
	tok := makeTestToken(tokens.TypeAWSCredentials)

	// Pre-place a symlink at the destination path.
	dest := filepath.Join(dir, tok.Filename)
	target := filepath.Join(dir, "real_file")
	if err := os.WriteFile(target, []byte("real"), 0o600); err != nil {
		t.Fatalf("write real_file: %v", err)
	}
	if err := os.Symlink(target, dest); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	_, err := deploy.DeployToFile(tok, dir, deploy.Options{})
	if err == nil {
		t.Error("DeployToFile succeeded when symlink at dest; want error (symlink refused)")
	} else {
		t.Logf("correctly refused symlink: %v", err)
	}

	// Verify real_file was NOT overwritten.
	data, _ := os.ReadFile(target)
	if string(data) != "real" {
		t.Error("real_file was overwritten via symlink — O_NOFOLLOW not effective")
	}
}

func TestPresetDirs_IncludesStandardLocations(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := os.Mkdir(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatalf("mkdir .ssh: %v", err)
	}

	presets, err := deploy.PresetDirs()
	if err != nil {
		t.Fatalf("PresetDirs(): %v", err)
	}
	if len(presets) != 4 {
		t.Fatalf("expected 4 preset dirs with .ssh present, got %d: %+v", len(presets), presets)
	}
	for _, p := range presets {
		if p.Label == "" || !filepath.IsAbs(p.Path) {
			t.Errorf("invalid preset dir %+v", p)
		}
	}
}

func TestSanitizePath_TildeHomeAndSubdir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	gotHome, err := deploy.SanitizePath("~")
	if err != nil {
		t.Fatalf("SanitizePath(~): %v", err)
	}
	if gotHome != home {
		t.Errorf("SanitizePath(~) = %q; want %q", gotHome, home)
	}

	gotSub, err := deploy.SanitizePath("~/sub/cred")
	if err != nil {
		t.Fatalf("SanitizePath(~/sub/cred): %v", err)
	}
	wantSub := filepath.Join(home, "sub", "cred")
	if gotSub != wantSub {
		t.Errorf("SanitizePath(~/sub/cred) = %q; want %q", gotSub, wantSub)
	}
}

func TestDeployToFile_SecretDirPermAndSSHValidation(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".kube")
	tok := tokens.Token{
		ID:        "kubetok1",
		Type:      tokens.TypeKubeconfig,
		Value:     "apiVersion: v1\n",
		Filename:  "config",
		CreatedAt: time.Now().UTC(),
	}
	res, err := deploy.DeployToFile(tok, dir, deploy.Options{})
	if err != nil {
		t.Fatalf("DeployToFile(kube): %v", err)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("stat kube dir: %v", err)
		}
		if fi.Mode().Perm() != 0o700 {
			t.Errorf("kube dir perm = %04o; want 0700", fi.Mode().Perm())
		}
	}
	if _, err := os.Stat(res.DeployedTo); err != nil {
		t.Fatalf("expected deployed kubeconfig at %s: %v", res.DeployedTo, err)
	}

	badSSH := tokens.Token{
		ID:       "badssh",
		Type:     tokens.TypeSSHKey,
		Value:    "not-a-valid-ssh-pair-without-sentinel",
		Filename: "id_ed25519",
	}
	if _, err := deploy.DeployToFile(badSSH, t.TempDir(), deploy.Options{}); err == nil {
		t.Error("expected error for malformed SSH token without sentinel")
	}

	goodSSH := tokens.Token{
		ID:       "goodssh",
		Type:     tokens.TypeSSHKey,
		Value:    "PRIVKEY\n---PUBLIC KEY---\nssh-ed25519 AAAA\n",
		Filename: "id_ed25519",
	}
	sshDir := t.TempDir()
	dryRes, err := deploy.DeployToFile(goodSSH, sshDir, deploy.Options{DryRun: true})
	if err != nil {
		t.Fatalf("SSH DryRun failed: %v", err)
	}
	if !dryRes.DryRun || !dryRes.WouldCreate || len(dryRes.ExtraFiles) != 1 {
		t.Errorf("unexpected SSH DryRun result: %+v", dryRes)
	}
}
