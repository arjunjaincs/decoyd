// reconcile.go — snapshot reconciliation from the token store.
//
// ReconcileSnapshot rebuilds deployed_tokens.json from the bbolt store,
// ensuring the snapshot reflects every token with a non-empty DeployedPath.
//
// This is called at TUI startup to make tokens deployed before the snapshot
// mechanism existed visible to a headless watcher without re-deploying them.
//
// The headless watcher (cmd_watch.go) cannot call this directly because it
// intentionally does not open the bbolt database. Reconciliation must be
// driven by the TUI process that owns the bbolt write lock.
package watch

import (
	"fmt"

	"github.com/arjunjaincs/decoyd/internal/store"
	"github.com/arjunjaincs/decoyd/internal/tokens"
)

// ReconcileSnapshot reads all tokens from st and rewrites deployed_tokens.json
// to include every token with a non-empty DeployedPath. Tokens without a
// DeployedPath are excluded (they are not active canaries).
//
// This is an overwrite operation — any tokens in the current snapshot that are
// not in the store are removed, ensuring the snapshot stays consistent with
// the database after manual cleanups or imports.
//
// ReconcileSnapshot is idempotent: calling it multiple times with the same
// store state produces the same snapshot.
//
// Returns nil when st is nil (no-op — safe to call from tests or contexts
// where no store is available).
func ReconcileSnapshot(st *store.Store, dataDir string) error {
	if st == nil {
		return nil
	}

	all, err := st.ListTokens()
	if err != nil {
		return fmt.Errorf("reconcile snapshot: list tokens: %w", err)
	}

	var deployed []tokens.Token
	for _, t := range all {
		if t.DeployedPath != "" {
			deployed = append(deployed, t)
		}
	}

	snap := make([]DeployedToken, 0, len(deployed))
	for _, t := range deployed {
		snap = append(snap, DeployedToken{
			ID:             t.ID,
			Type:           string(t.Type),
			DeployedPath:   t.DeployedPath,
			AlertChannelID: t.AlertChannelID,
		})
	}

	if err := WriteDeployedSnapshot(dataDir, snap); err != nil {
		return fmt.Errorf("reconcile snapshot: write: %w", err)
	}
	return nil
}
