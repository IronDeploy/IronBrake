//go:build unix

package safefile

import (
	"os"
	"syscall"
)

// O_NONBLOCK: abrir um FIFO volta na hora em vez de esperar um escritor.
func openNonBlocking(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
