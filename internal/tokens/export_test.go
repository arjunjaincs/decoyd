package tokens

// GHPChecksumForTest exports the internal ghpChecksum function for use in
// external tests. This symbol is only compiled into test binaries.
func GHPChecksumForTest(entropy string) string {
	return ghpChecksum(entropy)
}
