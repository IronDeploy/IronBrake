package setup

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
)

// HardenDenyRules são as regras deny de LEITURA que tiram as credenciais do
// caminho do agente. Cobrem os mesmos lugares que o iron scan (Iron Shield)
// inspeciona. Templates sem segredo (.env.example) ficam de fora de propósito.
var HardenDenyRules = []string{
	"Read(~/.aws/**)",
	"Read(~/.ssh/**)",
	"Read(~/.kube/**)",
	"Read(~/.config/gcloud/**)",
	"Read(~/.azure/**)",
	"Read(~/.terraform.d/**)",
	"Read(~/.npmrc)",
	"Read(~/.pypirc)",
	"Read(**/.pypirc)",
	"Read(~/.docker/config.json)",
	"Read(~/.config/gh/**)",
	"Read(~/.netrc)",
	"Read(~/.pgpass)",
	"Read(**/.env)",
	"Read(**/.env.local)",
	"Read(**/.env.development)",
	"Read(**/.env.production)",
	"Read(**/.env.prod)",
	"Read(**/terraform.tfstate)",
	"Read(**/terraform.tfstate.backup)",
}

// Harden acrescenta as HardenDenyRules em permissions.deny do settings, sem
// alterar o resto do arquivo nem duplicar regras. Devolve as regras que
// realmente adicionou (vazio = já estava tudo lá).
func Harden(settingsPath string) (added []string, err error) {
	doc, err := load(settingsPath)
	if errors.Is(err, fs.ErrNotExist) {
		doc = document{settings: map[string]json.RawMessage{}, hooks: map[string]json.RawMessage{}}
	} else if err != nil {
		return nil, err
	}

	perms, err := decodeObject(doc.settings["permissions"])
	if err != nil {
		return nil, fmt.Errorf(`%s: a chave "permissions" %w`, settingsPath, err)
	}

	var deny []string
	if raw := perms["deny"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &deny); err != nil {
			return nil, fmt.Errorf(`%s: "permissions.deny" não é uma lista`, settingsPath)
		}
	}

	for _, rule := range HardenDenyRules {
		if !slices.Contains(deny, rule) {
			deny = append(deny, rule)
			added = append(added, rule)
		}
	}
	if len(added) == 0 {
		return nil, nil
	}

	if perms["deny"], err = encode(deny, ""); err != nil {
		return nil, err
	}
	if doc.settings["permissions"], err = encode(perms, ""); err != nil {
		return nil, err
	}
	out, err := encode(doc.settings, "  ")
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(settingsPath, append(out, '\n'), 0o644); err != nil {
		return nil, err
	}
	return added, nil
}
