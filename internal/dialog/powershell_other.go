//go:build !windows

package dialog

import "os/exec"

// Fora do Windows não há janela de PowerShell: o caminho só existe para os
// testes conferirem que é absoluto e da pasta do sistema.
func powershellPath() string { return `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe` }

func hideWindow(*exec.Cmd) {}
