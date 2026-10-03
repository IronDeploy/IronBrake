package dialog

import (
	"os/exec"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows"
)

// createNoWindow impede que o powershell.exe abra uma janela de console por
// cima da janela do Iron Brake.
//
// Não use HideWindow aqui: ele grava SW_HIDE no STARTUPINFO, e o Windows
// aplica esse valor à primeira janela que o processo mostra, que é o próprio
// formulário. A janela existiria, com o temporizador rodando, mas invisível.
const createNoWindow = 0x08000000

// powershellPath é o Windows PowerShell da pasta do sistema, por caminho
// absoluto: um powershell.exe falso no PATH aprovaria tudo sozinho. A pasta vem
// da API do Windows, não de variável de ambiente.
func powershellPath() string {
	dir, err := windows.GetSystemDirectory()
	if err != nil || dir == "" {
		dir = `C:\Windows\System32`
	}
	return filepath.Join(dir, "WindowsPowerShell", "v1.0", "powershell.exe")
}

func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
}
