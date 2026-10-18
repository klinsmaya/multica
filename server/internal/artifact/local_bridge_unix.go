//go:build unix

package artifact

import (
	"os"
	"syscall"
)

func processOwns(info os.FileInfo) (bool, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false, errOwnerUnavailable
	}
	if os.Geteuid() == 0 {
		return true, nil
	}
	return uint32(os.Geteuid()) == stat.Uid, nil
}
