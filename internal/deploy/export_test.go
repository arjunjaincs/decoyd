package deploy

import "io"

// SetRootWarnDepsForTest injects the UID lookup and warning writer used by
// warnIfRootOutsideSystemDirs during tests and returns a cleanup function.
func SetRootWarnDepsForTest(uidFn func() int, w io.Writer) func() {
	prevUID := deployGetUID
	prevOut := deployWarnOut
	if uidFn != nil {
		deployGetUID = uidFn
	}
	if w != nil {
		deployWarnOut = w
	}
	return func() {
		deployGetUID = prevUID
		deployWarnOut = prevOut
	}
}
