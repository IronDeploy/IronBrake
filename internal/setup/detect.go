package setup

import (
	"os"
	"path/filepath"

	"github.com/IronDeploy/IronBrake/internal/hook"
)

// Detected é um agente achado nesta máquina e a evidência.
type Detected struct {
	Agent    string // nome do hook.Agent
	Evidence string // o que foi achado, para mostrar à pessoa
}

// agentMarker é o que denuncia que o agente está instalado ou já foi usado: a
// pasta dele em home, ou o programa no PATH.
type agentMarker struct {
	agent   string
	dir     string // relativa a home
	program string
}

// A pasta do Antigravity fica dentro de ~/.gemini, que o Gemini CLI antigo
// também criava: por isso o marcador é a subpasta antigravity-cli.
var agentMarkers = []agentMarker{
	{hook.Claude.Name(), ".claude", "claude"},
	{hook.Kiro.Name(), ".kiro", "kiro-cli"},
	{hook.Antigravity.Name(), filepath.Join(".gemini", "antigravity-cli"), "agy"},
	{hook.Codex.Name(), ".codex", "codex"},
}

// DetectAgents devolve os agentes que parecem estar nesta máquina, na ordem
// fixa claude, kiro, antigravity, codex. lookPath é o exec.LookPath (trocável
// nos testes). O CODEX_HOME, se definido, também vale como pasta do Codex.
func DetectAgents(home string, lookPath func(string) (string, error)) []Detected {
	var found []Detected
	for _, m := range agentMarkers {
		dirs := []string{filepath.Join(home, m.dir)}
		if m.agent == hook.Codex.Name() {
			if custom := os.Getenv("CODEX_HOME"); custom != "" {
				dirs = append(dirs, custom)
			}
		}
		if evidence, ok := firstExistingDir(dirs); ok {
			found = append(found, Detected{m.agent, evidence})
		} else if path, err := lookPath(m.program); err == nil {
			found = append(found, Detected{m.agent, m.program + " em " + path})
		}
	}
	return found
}

func firstExistingDir(dirs []string) (string, bool) {
	for _, d := range dirs {
		if info, err := os.Stat(d); err == nil && info.IsDir() {
			return d, true
		}
	}
	return "", false
}
