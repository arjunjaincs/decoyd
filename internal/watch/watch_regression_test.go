//go:build linux

// watch_regression_test.go — regression documentation for the B1 fix.
//
// Before commit 2257f31 (B1), the linuxWatcher only added inotify watches on
// specific file paths. Files created or renamed into a watched directory AFTER
// the watcher started were invisible until Reload() was called.
//
// The fix: parent-directory inotify watches with IN_CREATE|IN_MOVED_TO|
// IN_DELETE|IN_MOVED_FROM mask. The five tests in watch_parentdir_test.go
// prove the post-fix behaviour.
package watch

import "testing"

// TestWatch_B1RegressionDocumented is a documentation anchor for the B1 fix.
//
// Pre-B1 watcher behaviour (commit a76b71d, before 2257f31):
//   - start() called inotify_add_watch on each DeployedPath only.
//   - A file renamed into the directory via os.Rename was not detected.
//   - A file created in the directory was not detected.
//   - Reload() had to be called after every new deployment.
//
// Post-B1 (2257f31):
//   - start() additionally calls inotify_add_watch on each parent directory.
//   - IN_MOVED_TO and IN_CREATE fire when a file appears in a watched dir.
//   - The event loop cross-references filename against dirTokens map.
//   - IN_DELETE and IN_MOVED_FROM remove the file-level watch.
//
// Proof tests:
//
//	TestWatch_NewTokenPickedUpByParentDirWatch  (watch_parentdir_test.go)
//	TestWatch_HeadlessParentDirWatch             (watch_parentdir_test.go)
//	TestWatch_DeleteRecreateTriggerAlert         (watch_parentdir_test.go)
//	TestWatch_AtomicRenameDetectedWithin1s       (watch_parentdir_test.go)
//	TestWatch_RemovedTokenNoLongerAlerts         (watch_parentdir_test.go)
func TestWatch_B1RegressionDocumented(t *testing.T) {
	t.Log("B1 regression documented: see watch_parentdir_test.go for 5 proof tests")
}
