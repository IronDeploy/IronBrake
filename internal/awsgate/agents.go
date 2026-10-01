package awsgate

import (
	"path/filepath"
	"regexp"
	"strings"
)

// knownAgents liga o nome do programa ao nome do agente.
var knownAgents = map[string]string{
	"claude":        "claude",
	"kiro-cli":      "kiro",
	"kiro-cli-chat": "kiro",
	"gemini":        "gemini",
	"codex":         "codex",
	"cursor-agent":  "cursor",
}

// knownPackages: agentes instalados como pacote npm rodam como
// "node .../node_modules/@anthropic-ai/claude-code/cli.js", e o nome do
// arquivo ("cli") não diz nada: vale o nome da pasta do pacote.
var knownPackages = map[string]string{
	"claude-code": "claude",
	"gemini-cli":  "gemini",
	"codex":       "codex",
	"kiro-cli":    "kiro",
}

// interpreter reconhece quem roda o agente como script: node, bun, deno,
// python, python3, python3.11...
var interpreter = regexp.MustCompile(`^(node|nodejs|bun|deno|py|python[0-9.]*)$`)

// maxScriptArgs: quantos argumentos que não são opção olhar depois do interpretador.
const maxScriptArgs = 2

// DetectAgent diz qual agente está na cadeia de processos, ou "" se nenhum.
// Compara o nome do programa (e, para interpretadores, o do script ou da pasta
// do pacote), nunca o texto livre da linha de comando: "tail -f claude.log" não
// é o Claude.
func DetectAgent(chain []Proc) string {
	for _, p := range chain {
		if name := agentOf(p.Command); name != "" {
			return name
		}
	}
	return ""
}

func agentOf(command string) string {
	// O app do Kiro CLI tem um espaço no caminho ("Kiro CLI.app").
	if strings.Contains(command, "/Kiro CLI.app/Contents/MacOS/kiro-cli") {
		return "kiro"
	}

	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ""
	}
	program := baseName(fields[0])
	if !interpreter.MatchString(program) {
		return knownAgents[program]
	}

	// Pula as opções do interpretador (node --inspect script.js).
	seen := 0
	for _, arg := range fields[1:] {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		if name := knownAgents[baseName(arg)]; name != "" {
			return name
		}
		if name := packageOf(arg); name != "" {
			return name
		}
		if seen++; seen >= maxScriptArgs {
			break
		}
	}
	return ""
}

// packageOf procura o nome de um pacote de agente entre as pastas do caminho.
func packageOf(path string) string {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if name := knownPackages[strings.ToLower(part)]; name != "" {
			return name
		}
	}
	return ""
}

// baseName: nome do arquivo, em minúsculas, sem .exe nem extensão de script.
func baseName(path string) string {
	name := strings.ToLower(filepath.Base(path))
	name = strings.TrimSuffix(name, ".exe")
	for _, ext := range []string{".js", ".mjs", ".cjs", ".py"} {
		name = strings.TrimSuffix(name, ext)
	}
	return name
}
