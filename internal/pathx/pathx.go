// Package pathx tem o que o filepath não resolve sozinho para os caminhos que
// o agente digita num comando de shell.
package pathx

import (
	"path/filepath"
	"runtime"
)

// IsAbs diz se um caminho escrito num comando não depende da pasta atual.
//
// No Windows o filepath.IsAbs só aceita "C:\x", mas o agente roda comandos em
// bash (Git Bash, WSL) e escreve "/data/prod" ou "\x": caminho com raiz, que
// também ignora a pasta atual. Tratar como relativo juntaria o caminho ao
// projeto e um "rm -rf /data/prod" pareceria estar dentro dele.
func IsAbs(path string) bool {
	if filepath.IsAbs(path) {
		return true
	}
	return runtime.GOOS == "windows" && path != "" && (path[0] == '/' || path[0] == '\\')
}
