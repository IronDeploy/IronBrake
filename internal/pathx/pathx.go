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
//
// Letra de unidade com barra ("C:/x", "C:\x") conta como absoluta em qualquer
// sistema: um caminho assim nunca é relativo ao projeto, e tratá-lo como
// relativo o deixaria passar por "dentro" (o hook pode julgar comandos de
// cmd e PowerShell vistos de fora do Windows).
func IsAbs(path string) bool {
	if filepath.IsAbs(path) || hasDrive(path) {
		return true
	}
	return runtime.GOOS == "windows" && path != "" && (path[0] == '/' || path[0] == '\\')
}

func hasDrive(path string) bool {
	if len(path) < 3 || path[1] != ':' || (path[2] != '/' && path[2] != '\\') {
		return false
	}
	c := path[0] | 0x20 // minúscula
	return c >= 'a' && c <= 'z'
}
