//go:build windows

package safefile

import "os"

func openNonBlocking(path string) (*os.File, error) {
	return os.Open(path)
}
