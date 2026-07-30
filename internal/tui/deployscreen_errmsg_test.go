package tui

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/arjunjaincs/decoyd/internal/deploy"
)

func TestDeployErrMsg(t *testing.T) {
	const dir = "/some/target/dir"
	const dest = "/some/target/dir/id_ed25519"

	tests := []struct {
		name       string
		err        error
		targetDir  string
		deployedTo string
		wantPrefix string // message must START with this string
		wantSubstr string // message must CONTAIN this string
	}{
		{
			name:       "ErrAlreadyExists shows file path and actionable hint",
			err:        fmt.Errorf("wrapped: %w", deploy.ErrAlreadyExists),
			targetDir:  dir,
			deployedTo: dest,
			wantPrefix: "File already exists:",
			wantSubstr: dest,
		},
		{
			name:       "permission denied shows target dir and fix hint",
			err:        fmt.Errorf("deploy: write %s: open %s: %w", dest, dest, os.ErrPermission),
			targetDir:  dir,
			deployedTo: "",
			wantPrefix: "No write access to",
			wantSubstr: dir,
		},
		{
			name:       "ErrInvalid shows clean message",
			err:        fmt.Errorf("deploy: create directory: mkdir %s: %w", dir, os.ErrInvalid),
			targetDir:  dir,
			deployedTo: "",
			wantPrefix: "Invalid path",
			wantSubstr: "try again",
		},
		{
			name:       "generic error strips 'deploy: ' prefix",
			err:        errors.New("deploy: some unusual condition"),
			targetDir:  dir,
			deployedTo: "",
			wantPrefix: "Deploy failed:",
			wantSubstr: "some unusual condition",
		},
		{
			name:       "generic error without prefix is preserved",
			err:        errors.New("disk full"),
			targetDir:  dir,
			deployedTo: "",
			wantPrefix: "Deploy failed:",
			wantSubstr: "disk full",
		},
		{
			name:       "ErrAlreadyExists not-found deployedTo still shows hint",
			err:        deploy.ErrAlreadyExists,
			targetDir:  dir,
			deployedTo: "",
			wantPrefix: "File already exists:",
			wantSubstr: "Delete it first",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deployErrMsg(tt.err, tt.targetDir, tt.deployedTo)
			if !strings.HasPrefix(got, tt.wantPrefix) {
				t.Errorf("got %q\nwant prefix %q", got, tt.wantPrefix)
			}
			if tt.wantSubstr != "" && !strings.Contains(got, tt.wantSubstr) {
				t.Errorf("got %q\nwant substring %q", got, tt.wantSubstr)
			}
		})
	}
}
