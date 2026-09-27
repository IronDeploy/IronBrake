package filelock

import "time"

const retryDelay = 5 * time.Millisecond

// Acquire trava o caminho (criando o arquivo com 0600) e devolve a função que
// solta. Espera até timeout; o sistema solta a trava se o processo morrer.
func Acquire(path string, timeout time.Duration) (release func(), err error) {
	return acquire(path, timeout)
}
