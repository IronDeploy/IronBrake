package rules

import (
	"path/filepath"
	"strings"

	"github.com/IronDeploy/IronBrake/internal/runenv"
)

// workspaceTexts devolve o workspace do terraform lido nas pastas que a linha
// usa além da atual: terraform -chdir=DIR e cd DIR && terraform ... (o da pasta
// atual já está em Env.Context). Sem leitor de arquivos, ou com um cd que não
// dá para resolver (cd ~, cd -, cd $VAR, popd), volta menos do que poderia.
func (e Env) workspaceTexts(commands [][]string) []string {
	if e.ReadFile == nil {
		return nil
	}

	var texts []string
	dir, known := e.Cwd, e.Cwd != ""
	for _, tokens := range commands {
		switch programName(tokens[0]) {
		case "cd", "pushd":
			dir, known = changeDir(dir, known, tokens[1:])
		case "popd":
			known = false
		case "terraform", "tofu":
			_, chdir, _ := terraformSubcommand(tokens)
			target, ok := workspaceDir(dir, known, chdir)
			if !ok || target == e.Cwd {
				continue
			}
			if name, found := runenv.Workspace(target, e.DataDir, e.ReadFile); found {
				texts = append(texts, name)
			}
		}
	}
	return texts
}

// changeDir aplica um cd (ou pushd) à pasta atual, se o destino for um texto
// simples. Opções (-P, -L) são puladas.
func changeDir(dir string, known bool, args []string) (string, bool) {
	var target string
	for _, a := range args {
		if !strings.HasPrefix(a, "-") || a == "-" {
			target = a
			break
		}
	}
	if target == "" || target == "-" || strings.HasPrefix(target, "~") || strings.ContainsAny(target, "$`*?[") {
		return dir, false
	}
	if filepath.IsAbs(target) {
		return filepath.Clean(target), true
	}
	if !known {
		return dir, false
	}
	return filepath.Join(dir, target), true
}

// workspaceDir junta a pasta atual e o -chdir do comando.
func workspaceDir(dir string, known bool, chdir string) (string, bool) {
	switch {
	case chdir != "" && filepath.IsAbs(chdir):
		return filepath.Clean(chdir), true
	case !known:
		return "", false
	case chdir != "":
		return filepath.Join(dir, chdir), true
	}
	return dir, true
}
