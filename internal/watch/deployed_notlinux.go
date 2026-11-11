//go:build !linux

package watch

func currentOwnerUID() *uint32 {
	return nil
}
