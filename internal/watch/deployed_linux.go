//go:build linux

package watch

import (
	"math"
	"os"
)

func currentOwnerUID() *uint32 {
	uid := os.Getuid()
	if uid < 0 || uid > math.MaxInt32 {
		return nil
	}
	u := uint32(uid)
	return &u
}
