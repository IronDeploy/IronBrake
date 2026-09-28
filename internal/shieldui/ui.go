package shieldui

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/IronDeploy/IronBrake/internal/scan"
	"github.com/IronDeploy/IronBrake/internal/setup"
	"github.com/IronDeploy/IronBrake/internal/shield"
)

// AuditFunc grava uma ação lock/unlock no log de auditoria — injetado para
// reaproveitar a mesma linha que "iron shield lock/unlock" já escreve, sem
// este pacote depender do formato exato do log.
type AuditFunc func(action, category string, ruleCount int)

// Run roda a tela interativa até o usuário sair (Esc ou q). settingsPath é o
// .claude/settings.json a travar/destravar; findings vem de um scan.Scan já
// rodado — a mesma varredura que "iron scan" mostra, sem lê-la de novo. Cada
// Enter grava a mudança na hora: não existe "salvar ao sair", pra uma queda
// de terminal no meio não perder o que já foi travado/destravado.
func Run(settingsPath string, findings []scan.Finding, in, out *os.File, audit AuditFunc) error {
	rows, err := loadRows(settingsPath, findings)
	if err != nil {
		return err
	}

	// Nada achado: só mostra a mensagem e sai — sem entrar em modo raw nem
	// esperar tecla, que não teria nada a gerenciar mesmo.
	if len(rows) == 0 {
		fmt.Fprint(out, Render(rows, -1))
		return nil
	}

	if !term.IsTerminal(int(in.Fd())) {
		return fmt.Errorf("iron scan --manage precisa rodar num terminal interativo (tty)")
	}

	oldState, err := term.MakeRaw(int(in.Fd()))
	if err != nil {
		return err
	}
	defer term.Restore(int(in.Fd()), oldState)

	cursor := 0
	buf := make([]byte, 8)
	for {
		fmt.Fprint(out, "\x1b[2J\x1b[H") // limpa a tela e volta pro topo, sem flicker
		fmt.Fprint(out, crlf(Render(rows, cursor)))

		n, err := in.Read(buf)
		if err != nil {
			return err
		}
		key, _ := DecodeKey(buf[:n])

		switch key {
		case KeyUp:
			if len(rows) > 0 {
				cursor = (cursor - 1 + len(rows)) % len(rows)
			}
		case KeyDown:
			if len(rows) > 0 {
				cursor = (cursor + 1) % len(rows)
			}
		case KeyEnter:
			if len(rows) > 0 {
				rows[cursor], err = toggle(settingsPath, rows[cursor], audit)
				if err != nil {
					return err
				}
			}
		case KeyEsc, KeyQuit:
			fmt.Fprint(out, "\x1b[2J\x1b[H")
			return nil
		}
	}
}

// loadRows lê o settings.json UMA vez (não a cada linha) e cruza com os
// achados do scan já em mãos.
func loadRows(settingsPath string, findings []scan.Finding) ([]Row, error) {
	lockedRules, _, err := setup.StatusFor(settingsPath, shield.AllRules())
	if err != nil {
		return nil, err
	}
	locked := make(map[string]bool, len(lockedRules))
	for _, r := range lockedRules {
		locked[r] = true
	}
	isFullyLocked := func(rules []string) bool {
		for _, r := range rules {
			if !locked[r] {
				return false
			}
		}
		return true
	}
	return BuildRows(findings, isFullyLocked), nil
}

// crlf troca \n por \r\n: em modo raw o terminal desliga o pós-processamento
// de saída (OPOST), então um \n sozinho só desce a linha sem voltar pra
// coluna 0 — cada linha nova sai mais deslocada que a anterior. É a causa do
// texto "espalhado" quando isso falta.
func crlf(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") }

// toggle grava a troca de estado de UMA categoria e devolve a linha já
// atualizada. HardenRules/UnhardenRules garantem que, ao voltar sem erro, a
// categoria fica inteiramente travada ou inteiramente destravada — nunca
// parcial — então dá pra fixar r.Hidden direto, sem reler o arquivo.
func toggle(settingsPath string, r Row, audit AuditFunc) (Row, error) {
	if !r.Hidable() {
		return r, nil
	}
	if r.Hidden {
		removed, err := setup.UnhardenRules(settingsPath, r.Category.Rules)
		if err != nil {
			return r, err
		}
		if audit != nil {
			audit("unlock", r.Category.Name, len(removed))
		}
		r.Hidden = false
	} else {
		added, err := setup.HardenRules(settingsPath, r.Category.Rules)
		if err != nil {
			return r, err
		}
		if audit != nil {
			audit("lock", r.Category.Name, len(added))
		}
		r.Hidden = true
	}
	return r, nil
}
