package rules

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/IronDeploy/IronBrake/internal/hook"
	"github.com/IronDeploy/IronBrake/internal/pathx"
)

const rmOutsideDanger = "rm recursivo em um caminho fora da pasta do projeto: apaga dados que o projeto não controla, sem volta."

// tempRoots são pastas descartáveis: apagar ali é rotina.
var tempRoots = []string{"/tmp", "/private/tmp", "/var/tmp", "/private/var/tmp", "/var/folders", "/private/var/folders"}

// rmOutsideRule cobre o rm -rf que não é catastrófico universal (isso é
// fsCatastrophic), mas aponta para fora da pasta do projeto: /data/prod,
// ../outro-repo, ~/dados. Em produção é deny; fora, ask. Caminho com
// variável que não dá para resolver ($DIR/x) é ask.
type rmOutsideRule struct{}

var rmOutside Rule = rmOutsideRule{}

func (rmOutsideRule) Name() string { return "rm-outside-project" }

func (rmOutsideRule) Check(commands [][]string, env Env) (hook.Decision, string) {
	for _, tokens := range commands {
		if len(tokens) == 0 || programName(tokens[0]) != "rm" || !isRecursive(tokens[1:]) {
			continue
		}
		for _, path := range positionalPaths(tokens[1:]) {
			outside, unknown := classifyPath(path, env.Cwd)
			switch {
			case outside:
				return decideByEnvironment(rmOutsideDanger, commands, env)
			case unknown && !env.inScript:
				return hook.Ask, rmUnknownPathReason
			case unknown && bareParamVar(path, env):
				return hook.Ask, rmScriptEmptyVarReason
			}
		}
	}
	return hook.Allow, ""
}

const rmScriptEmptyVarReason = "Iron Brake: confirme antes de executar: o script faz rm recursivo numa variável que ele não define nem protege ($1, $DIR, $DIR/*). Se ela vier vazia, o caminho vira a raiz. Use set -u ou ${VAR:?}."

// bareVar: o caminho é só uma variável (com ou sem / e /* no fim): $1, "$DIR/",
// ${DIR}/*. $(NAME) do make só conta com a barra, porque sem ela o rm fica sem
// argumento.
var bareVar = regexp.MustCompile(`^(?:\$\{?([A-Za-z0-9_]+)\}?(?:/\*?)?|\$\(([A-Za-z0-9_]+)\)/\*?)$`)

// bareParamVar: dentro de um script, o caminho é só uma variável que o
// próprio script não define, sem set -u nem ${VAR:?}. É o clássico
// rm -rf $DIR/* com DIR vazio. Variável definida no script (DIR=$(mktemp -d))
// e caminho com mais texto ($DIR/build) ficam de fora.
func bareParamVar(path string, env Env) bool {
	if !env.inScript || env.scriptNounset || strings.Contains(path, ":?") {
		return false
	}
	m := bareVar.FindStringSubmatch(path)
	if m == nil {
		return false
	}
	return !env.scriptAssigned[m[1]+m[2]]
}

// tempVars apontam para pastas descartáveis.
var tempVars = []string{"${TMPDIR}", "$TMPDIR", "${TMP}", "$TMP", "${TEMP}", "$TEMP"}

// classifyPath: outside quando o caminho, resolvido a partir de cwd, fica fora
// dela e fora das pastas temporárias; unknown quando ainda tem variável que o
// Iron Brake não resolve. Sem cwd, só os absolutos contam como fora.
func classifyPath(path, cwd string) (outside, unknown bool) {
	for _, v := range tempVars {
		if rest, ok := strings.CutPrefix(path, v); ok && (rest == "" || rest[0] == '/') {
			return false, false
		}
	}
	if cwd != "" {
		for _, v := range []string{"${PWD}", "$PWD"} {
			if rest, ok := strings.CutPrefix(path, v); ok && (rest == "" || rest[0] == '/') {
				path = cwd + rest
			}
		}
	}
	path, ok := expandHome(path)
	if !ok {
		return true, false
	}
	if hasUnresolvedVar(path) {
		return false, true
	}
	return outsideProject(path, cwd), false
}

// outsideProject: o caminho, já sem variáveis, fica fora de cwd e das pastas temporárias.
func outsideProject(path, cwd string) bool {
	if !pathx.IsAbs(path) {
		if cwd == "" {
			return strings.HasPrefix(path, "..")
		}
		path = filepath.Join(cwd, path)
	}
	path = filepath.Clean(path)
	if cwd != "" && within(path, filepath.Clean(cwd)) {
		return false
	}
	for _, root := range tempRoots {
		if within(path, filepath.Clean(root)) {
			return false
		}
	}
	// No Windows a pasta temporária é %TEMP% (C:\Users\...\AppData\Local\Temp).
	if tmp := os.TempDir(); runtime.GOOS == "windows" && tmp != "" && within(path, filepath.Clean(tmp)) {
		return false
	}
	return true
}

// expandHome troca ~, $HOME e ${HOME} no começo pelo home do usuário.
func expandHome(path string) (string, bool) {
	for _, prefix := range []string{"~", "${HOME}", "$HOME"} {
		rest, found := strings.CutPrefix(path, prefix)
		if !found || (rest != "" && rest[0] != '/' && rest[0] != filepath.Separator) {
			continue
		}
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", false
		}
		return home + rest, true
	}
	return path, true
}

// within diz se path é root ou está dentro dele. Os dois já passaram por
// filepath.Clean, então o separador é o do sistema (barra invertida no Windows).
func within(path, root string) bool {
	sep := string(filepath.Separator)
	return path == root || strings.HasPrefix(path, strings.TrimSuffix(root, sep)+sep)
}
