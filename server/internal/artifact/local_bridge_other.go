//go:build !unix

package artifact

import "os"

func processOwns(info os.FileInfo) (bool, error) {
	return false, errOwnerUnavailable
}
