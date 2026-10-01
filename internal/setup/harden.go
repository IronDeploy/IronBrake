package setup

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/IronDeploy/IronBrake/internal/shield"
)

// HardenDenyRules são as regras deny de LEITURA que tiram as credenciais do
// caminho do agente. Vêm de internal/shield.Categories — a mesma tabela que
// liga cada regra à fonte que o iron scan relata, para as duas nunca
// desalinharem. Templates sem segredo (.env.example) ficam de fora de
// propósito.
var HardenDenyRules = shield.AllRules()

// Harden acrescenta as HardenDenyRules em permissions.deny do settings, sem
// alterar o resto do arquivo nem duplicar regras. Devolve as regras que
// realmente adicionou (vazio = já estava tudo lá). Funciona mesmo que
// settingsPath nunca tenha existido — não depende de "iron init" ter rodado
// antes.
func Harden(settingsPath string) ([]string, error) {
	return HardenRules(settingsPath, HardenDenyRules)
}

// Unharden remove exatamente as HardenDenyRules do deny do settings — nunca
// mexe em nenhuma outra regra que o usuário (ou outra ferramenta) tenha
// gravado ali. Devolve as regras que realmente tirou (vazio = já não tinha
// nenhuma, mesmo que "iron init --harden" nunca tenha rodado).
func Unharden(settingsPath string) ([]string, error) {
	return UnhardenRules(settingsPath, HardenDenyRules)
}

// HardenRules é o Harden genérico: acrescenta só as regras da lista dada
// (usado para travar uma categoria por vez em "iron scan --manage").
func HardenRules(settingsPath string, rules []string) (added []string, err error) {
	doc, deny, err := loadDenyRules(settingsPath)
	if err != nil {
		return nil, err
	}

	for _, rule := range rules {
		if !slices.Contains(deny, rule) {
			deny = append(deny, rule)
			added = append(added, rule)
		}
	}
	if len(added) == 0 {
		return nil, nil
	}
	if err := writeDenyRules(settingsPath, doc, deny); err != nil {
		return nil, err
	}
	return added, nil
}

// UnhardenRules é o Unharden genérico: remove só as regras da lista dada,
// preservando qualquer outra regra deny que já estivesse no settings.
func UnhardenRules(settingsPath string, rules []string) (removed []string, err error) {
	doc, deny, err := loadDenyRules(settingsPath)
	if err != nil {
		return nil, err
	}

	var kept []string
	for _, rule := range deny {
		if slices.Contains(rules, rule) {
			removed = append(removed, rule)
			continue
		}
		kept = append(kept, rule)
	}
	if len(removed) == 0 {
		return nil, nil
	}
	if err := writeDenyRules(settingsPath, doc, kept); err != nil {
		return nil, err
	}
	return removed, nil
}

// ShieldStatus separa as HardenDenyRules em travadas (já no deny) e destravadas
// (faltando), para "iron shield status" mostrar o que está protegido agora.
type ShieldStatus struct {
	Locked   []string
	Unlocked []string
}

// Status lê o settings e devolve o que está travado e o que não está.
// settingsPath ausente conta como tudo destravado, sem erro.
func Status(settingsPath string) (ShieldStatus, error) {
	locked, unlocked, err := StatusFor(settingsPath, HardenDenyRules)
	if err != nil {
		return ShieldStatus{}, err
	}
	return ShieldStatus{Locked: locked, Unlocked: unlocked}, nil
}

// StatusFor é o Status genérico: separa só as regras da lista dada em
// travadas e destravadas. É o que "iron scan --manage" usa para saber se
// cada categoria está oculta do agente ou não.
func StatusFor(settingsPath string, rules []string) (locked, unlocked []string, err error) {
	_, deny, err := loadDenyRules(settingsPath)
	if err != nil {
		return nil, nil, err
	}

	for _, rule := range rules {
		if slices.Contains(deny, rule) {
			locked = append(locked, rule)
		} else {
			unlocked = append(unlocked, rule)
		}
	}
	return locked, unlocked, nil
}

// loadDenyRules lê o settings (documento cru + deny já decodificado). Um
// settingsPath ausente devolve um documento vazio, não um erro — todas as
// operações do Iron Shield funcionam mesmo sem "iron init" ter rodado antes.
func loadDenyRules(settingsPath string) (doc document, deny []string, err error) {
	doc, err = load(settingsPath)
	if errors.Is(err, fs.ErrNotExist) {
		doc = document{settings: map[string]json.RawMessage{}, hooks: map[string]json.RawMessage{}}
	} else if err != nil {
		return document{}, nil, err
	}

	perms, err := decodeObject(doc.settings["permissions"])
	if err != nil {
		return document{}, nil, fmt.Errorf(`%s: a chave "permissions" %w`, settingsPath, err)
	}
	if raw := perms["deny"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &deny); err != nil {
			return document{}, nil, fmt.Errorf(`%s: "permissions.deny" não é uma lista`, settingsPath)
		}
	}
	return doc, deny, nil
}

// writeDenyRules regrava o settings inteiro com o novo deny, preservando
// todo o resto do documento.
func writeDenyRules(settingsPath string, doc document, deny []string) error {
	perms, err := decodeObject(doc.settings["permissions"])
	if err != nil {
		return fmt.Errorf(`%s: a chave "permissions" %w`, settingsPath, err)
	}
	if perms["deny"], err = encode(deny, ""); err != nil {
		return err
	}
	if doc.settings["permissions"], err = encode(perms, ""); err != nil {
		return err
	}
	out, err := encode(doc.settings, "  ")
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		return err
	}
	return writeFileAtomic(settingsPath, append(out, '\n'))
}
