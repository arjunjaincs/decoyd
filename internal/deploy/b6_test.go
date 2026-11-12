package deploy_test

import (
	"os"
	"testing"
	"time"

	"github.com/arjunjaincs/decoyd/internal/deploy"
	"github.com/arjunjaincs/decoyd/internal/tokens"
)

// TestDeploy_MtimeBackdated verifies that deployed files have an mtime
// between 30 and 400 days before now (B6 requirement).
func TestDeploy_MtimeBackdated(t *testing.T) {
	dir := t.TempDir()
	tok := makeTestToken(tokens.TypeEnvSecrets)
	res, err := deploy.DeployToFile(tok, dir, deploy.Options{})
	if err != nil {
		t.Fatalf("DeployToFile: %v", err)
	}
	fi, err := os.Stat(res.DeployedTo)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	mtime := fi.ModTime()
	now := time.Now()
	minAge := now.AddDate(0, 0, -401) // slightly beyond 400 to allow test latency
	maxAge := now.AddDate(0, 0, -29)  // slightly inside 30 to allow clock skew
	if mtime.After(maxAge) {
		t.Errorf("mtime %v is too recent (less than 30 days ago); backdating not applied", mtime)
	}
	if mtime.Before(minAge) {
		t.Errorf("mtime %v is too old (more than 401 days ago)", mtime)
	}
	t.Logf("deployed file mtime: %v (%.0f days ago)", mtime, now.Sub(mtime).Hours()/24)
}

// TestDeploy_SSHMtimeBackdated verifies both files in an SSH keypair are backdated.
func TestDeploy_SSHMtimeBackdated(t *testing.T) {
	dir := t.TempDir()
	sshTok, err := tokens.Generate(tokens.TypeSSHKey)
	if err != nil {
		t.Fatalf("Generate SSH key: %v", err)
	}
	res, err := deploy.DeployToFile(sshTok, dir, deploy.Options{})
	if err != nil {
		t.Fatalf("DeployToFile SSH: %v", err)
	}
	now := time.Now()
	for _, path := range append([]string{res.DeployedTo}, res.ExtraFiles...) {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		mtime := fi.ModTime()
		if mtime.After(now.AddDate(0, 0, -29)) {
			t.Errorf("file %s mtime %v is too recent; backdating not applied", path, mtime)
		}
		t.Logf("%s mtime: %v (%.0f days ago)", path, mtime, now.Sub(mtime).Hours()/24)
	}
}
