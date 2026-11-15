package setup

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// CopilotConfigPath é o config.json do Copilot CLI, onde ele guarda as pastas
// confiadas ("trustedFolders"): $COPILOT_HOME/config.json, ou ~/.copilot/config.json.
func CopilotConfigPath(home string) string {
	if custom := os.Getenv("COPILOT_HOME"); custom != "" {
		return filepath.Join(custom, "config.json")
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".copilot", "config.json")
}

// CopilotTrust é o estado de confiança lido do config.json do Copilot.
type CopilotTrust struct {
	// FolderTrusted: a pasta está em trustedFolders, a lista que a opção "Yes,
	// and remember this folder for future sessions" preenche. Quem escolheu só
	// "Yes" confiou apenas naquela sessão, e isso não deixa rastro no arquivo.
	FolderTrusted bool
}

// ReadCopilotTrust lê a confiança de dir no config.json do usuário. Sem o
// arquivo, nada está confiado (e o erro é nil). O arquivo traz linhas de
// comentário "//" no topo, que não são JSON.
func ReadCopilotTrust(configPath, dir string) (CopilotTrust, error) {
	data, err := os.ReadFile(configPath)
	if errors.Is(err, fs.ErrNotExist) {
		return CopilotTrust{}, nil
	}
	if err != nil {
		return CopilotTrust{}, err
	}
	doc, err := decodeObject(stripLineComments(data))
	if err != nil {
		return CopilotTrust{}, fmt.Errorf("%s: %w", configPath, err)
	}
	var folders []string
	if raw := doc["trustedFolders"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &folders); err != nil {
			return CopilotTrust{}, fmt.Errorf(`%s: a chave "trustedFolders" não é uma lista de textos`, configPath)
		}
	}
	root := filepath.Clean(dir)
	for _, f := range folders {
		if filepath.Clean(f) == root {
			return CopilotTrust{FolderTrusted: true}, nil
		}
	}
	return CopilotTrust{}, nil
}

// stripLineComments tira as linhas que começam com "//". Só linhas inteiras:
// um "//" dentro de um texto (https://...) não é tocado.
func stripLineComments(data []byte) []byte {
	var out bytes.Buffer
	for _, line := range bytes.Split(data, []byte("\n")) {
		if bytes.HasPrefix(bytes.TrimSpace(line), []byte("//")) {
			continue
		}
		out.Write(line)
		out.WriteByte('\n')
	}
	return out.Bytes()
}
