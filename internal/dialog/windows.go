package dialog

import (
	"encoding/base64"
	"strconv"
	"time"
)

// maxMessageRunes limita o texto da janela: a linha de comando do Windows
// aceita 32767 caracteres, e um cartão maior que isso não cabe numa tela.
const maxMessageRunes = 4000

// formScript é a janela do Windows: Windows Forms, com o motivo numa caixa
// de texto rolável e os botões Executar e Cancelar. Cancelar é o padrão (Enter)
// e o que o Esc e o X acionam; o tempo esgotado devolve "timeout". Imprime a
// resposta e sai.
//
// Regras do script:
//   - a mensagem NUNCA entra aqui: nomes de recursos vêm do .tf, que o agente
//     escreve. Ela chega como argumento, em base64, e é decodificada dentro;
//   - sem aspas duplas: o texto vai numa linha de comando do Windows;
//   - sem janela de desktop interativo (serviço, SSH) sai com 3, e a resposta
//     vira Unavailable em vez de esperar um clique que ninguém vê.
const formScript = `& { param($b, $s)
$ErrorActionPreference = 'Stop'
if (-not [Environment]::UserInteractive) { exit 3 }
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
[System.Windows.Forms.Application]::EnableVisualStyles()
$m = [System.Text.Encoding]::UTF8.GetString([System.Convert]::FromBase64String($b)) -replace '\r?\n', [Environment]::NewLine
$res = @{ v = 'Cancelar' }
$f = New-Object System.Windows.Forms.Form
$f.Text = 'Iron Brake'
$f.TopMost = $true
$f.StartPosition = 'CenterScreen'
$f.FormBorderStyle = 'FixedDialog'
$f.MaximizeBox = $false
$f.MinimizeBox = $false
$f.ClientSize = New-Object System.Drawing.Size(540, 280)
$t = New-Object System.Windows.Forms.TextBox
$t.Multiline = $true
$t.ReadOnly = $true
$t.TabStop = $false
$t.ScrollBars = 'Vertical'
$t.BorderStyle = 'None'
$t.BackColor = [System.Drawing.SystemColors]::Control
$t.Location = New-Object System.Drawing.Point(16, 16)
$t.Size = New-Object System.Drawing.Size(508, 205)
$t.Text = $m
$run = New-Object System.Windows.Forms.Button
$run.Text = 'Executar'
$run.Size = New-Object System.Drawing.Size(110, 32)
$run.Location = New-Object System.Drawing.Point(298, 236)
$cancel = New-Object System.Windows.Forms.Button
$cancel.Text = 'Cancelar'
$cancel.Size = New-Object System.Drawing.Size(110, 32)
$cancel.Location = New-Object System.Drawing.Point(414, 236)
$f.Controls.Add($t)
$f.Controls.Add($run)
$f.Controls.Add($cancel)
$f.AcceptButton = $cancel
$f.CancelButton = $cancel
$run.Add_Click({ $res.v = 'Executar'; $f.Close() }.GetNewClosure())
$cancel.Add_Click({ $f.Close() }.GetNewClosure())
$timer = New-Object System.Windows.Forms.Timer
$timer.Interval = [Math]::Max(1000, [int]$s * 1000)
$timer.Add_Tick({ $res.v = 'timeout'; $f.Close() }.GetNewClosure())
$timer.Start()
$f.Add_Shown({ $f.Activate(); $cancel.Focus() }.GetNewClosure())
[void]$f.ShowDialog()
[Console]::Out.Write($res.v)
}`

// powershellArgs monta a linha de comando do PowerShell: o script fixo, mais a
// mensagem em base64 (só A-Z a-z 0-9 + / =, sem nada que o PowerShell
// interprete) e o tempo em segundos.
func powershellArgs(message string, wait time.Duration) []string {
	encoded := base64.StdEncoding.EncodeToString([]byte(truncate(message)))
	command := formScript + " '" + encoded + "' " + strconv.Itoa(int(wait.Seconds()))
	return []string{"-NoProfile", "-NonInteractive", "-Sta", "-WindowStyle", "Hidden", "-Command", command}
}

// truncate corta o texto no limite, por caracteres (nunca no meio de um).
func truncate(message string) string {
	runes := []rune(message)
	if len(runes) <= maxMessageRunes {
		return message
	}
	return string(runes[:maxMessageRunes]) + "…"
}
