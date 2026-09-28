package rules

import (
	"strings"

	"github.com/IronDeploy/IronBrake/internal/hook"
)

// Motivos nunca repetem o comando: ele pode conter caminhos ou segredos.
const (
	rmRootReason         = "Iron Brake: rm recursivo em um caminho crítico (raiz, home ou diretório de sistema) bloqueado. Isso apaga a máquina inteira e não tem volta. Apague um caminho específico do projeto."
	ddDeviceReason       = "Iron Brake: dd escrevendo direto em um disco (of=/dev/...) bloqueado. Isso destrói a partição inteira. Confirme o dispositivo e peça ao usuário para executar."
	mkfsReason           = "Iron Brake: formatar um dispositivo (mkfs) bloqueado. Isso apaga todo o conteúdo do disco. Peça ao usuário para executar."
	wipefsReason         = "Iron Brake: wipefs apaga a assinatura do sistema de arquivos e torna o disco ilegível. Peça ao usuário para executar."
	shredDeviceReason    = "Iron Brake: shred sobre um dispositivo de bloco (/dev/...) destrói o disco inteiro sem volta. Peça ao usuário para executar."
	redirectDeviceReason = "Iron Brake: redirecionar a saída para um disco (> /dev/...) sobrescreve a partição inteira. Peça ao usuário para executar."
	chmodRootDanger      = "chmod/chown recursivo em um caminho de sistema quebra permissões da máquina inteira."

	findDeleteDanger = "find sem filtro de nome com -delete ou -exec rm apaga tudo sob o caminho, sem volta (adicione -name/-path, ou rode sem -delete para ver o que casaria)."
	shredFileDanger  = "shred sobrescreve o arquivo para impedir recuperação."
	rmForceDanger    = "rm -rf apaga uma árvore inteira de arquivos sem volta."
)

var (
	fsCatastrophic = commandRule{name: "fs-catastrophic", check: checkCatastrophicFS}
	fsDangerous    = dangerRule{name: "fs-dangerous", match: matchDangerousFS}
)

// catastrophicPaths: apagar recursivamente qualquer um destes derruba a máquina.
// Guardamos as formas com e sem barra final; o wildcard /* aparece literal
// porque o hook vê a linha antes de o shell expandir.
var catastrophicPaths = set(
	"/", "/*", "/.", "/..", ".", "..", "*",
	"~", "~/", "~/*", "$HOME", "${HOME}", "$HOME/", "$HOME/*", "${HOME}/", "${HOME}/*",
)

// systemDirs são os diretórios de topo cujo apagar recursivo é catastrófico.
var systemDirs = set(
	"/bin", "/sbin", "/etc", "/usr", "/var", "/lib", "/lib32", "/lib64", "/libx32",
	"/boot", "/dev", "/proc", "/sys", "/opt", "/root", "/home", "/srv", "/run",
	"/mnt", "/media", "/cores",
	// macOS
	"/System", "/Library", "/Applications", "/Users", "/private", "/Volumes",
)

// deviceSafe são /dev/ que não são discos: escrever neles é normal.
var deviceSafe = set(
	"/dev/null", "/dev/zero", "/dev/random", "/dev/urandom",
	"/dev/stdout", "/dev/stderr", "/dev/stdin", "/dev/tty",
)

func checkCatastrophicFS(tokens []string) (hook.Decision, string) {
	if len(tokens) == 0 {
		return hook.Allow, ""
	}
	// Redirecionar para um disco não depende do programa: echo, cat, tee, o
	// que for. Por isso vem antes do switch por nome.
	if writesRedirectToDisk(tokens) {
		return hook.Deny, redirectDeviceReason
	}

	prog := programName(tokens[0])
	args := tokens[1:]

	switch {
	case prog == "rm":
		if isCatastrophicRm(args) {
			return hook.Deny, rmRootReason
		}
	case prog == "dd":
		if writesToDisk(args) {
			return hook.Deny, ddDeviceReason
		}
	case prog == "mkfs" || strings.HasPrefix(prog, "mkfs."):
		return hook.Deny, mkfsReason
	case prog == "wipefs":
		return hook.Deny, wipefsReason
	case prog == "shred" && hasDeviceTarget(args):
		return hook.Deny, shredDeviceReason
	case prog == "chmod" || prog == "chown" || prog == "chgrp":
		if isRecursive(args) && hasCatastrophicTarget(afterDoubleDash(args)) {
			return hook.Deny, "Iron Brake: " + chmodRootDanger + " Peça ao usuário para executar."
		}
	}
	return hook.Allow, ""
}

func matchDangerousFS(tokens []string) (string, bool) {
	if len(tokens) == 0 {
		return "", false
	}
	switch programName(tokens[0]) {
	case "find":
		// Só marca o find sem filtro: find . -delete apaga tudo. Com um filtro
		// (-name '*.pyc') é limpeza rotineira e segura, então libera.
		if hasFindDelete(tokens[1:]) && !hasFindNameFilter(tokens[1:]) {
			return findDeleteDanger, true
		}
	case "shred":
		if len(positionalPaths(tokens[1:])) > 0 {
			return shredFileDanger, true
		}
	}
	return "", false
}

// isCatastrophicRm: rm recursivo em caminho crítico, ou --no-preserve-root.
func isCatastrophicRm(args []string) bool {
	recursive, noPreserve := false, false
	for _, a := range parseUntilDoubleDash(args) {
		switch {
		case a == "--no-preserve-root":
			noPreserve = true
		case a == "--recursive", a == "--dir": // --dir (-d) apaga diretórios vazios
			recursive = true
		case strings.HasPrefix(a, "--"):
		case strings.HasPrefix(a, "-"):
			recursive = recursive || strings.ContainsAny(a, "rR")
		}
	}
	if noPreserve {
		return true
	}
	if !recursive {
		return false
	}
	return hasCatastrophicTarget(afterDoubleDash(args))
}

func hasCatastrophicTarget(paths []string) bool {
	for _, p := range paths {
		if isCatastrophicPath(p) {
			return true
		}
	}
	return false
}

// isCatastrophicPath reconhece a raiz, o home e os diretórios de sistema,
// com ou sem barra final e com o glob /* que o shell ainda não expandiu.
func isCatastrophicPath(p string) bool {
	if catastrophicPaths[p] {
		return true
	}
	trimmed := strings.TrimSuffix(strings.TrimSuffix(p, "/*"), "/")
	if trimmed == "" { // era "/" ou "/*"
		return true
	}
	return systemDirs[trimmed]
}

// writesToDisk: dd of=/dev/DISCO (não os /dev/ inofensivos).
func writesToDisk(args []string) bool {
	for _, a := range args {
		if target, ok := strings.CutPrefix(a, "of="); ok && isDiskDevice(target) {
			return true
		}
	}
	return false
}

func hasDeviceTarget(args []string) bool {
	for _, p := range positionalPaths(args) {
		if isDiskDevice(p) {
			return true
		}
	}
	return false
}

// isDiskDevice: /dev/algo que não seja um dos /dev/ inofensivos.
func isDiskDevice(path string) bool {
	return strings.HasPrefix(path, "/dev/") && !deviceSafe[path]
}

// writesRedirectToDisk procura > /dev/DISCO, >> e >| na mesma linha, com o
// descritor opcional (1>, 2>>) e o alvo colado (>/dev/sda) ou solto (> /dev/sda).
func writesRedirectToDisk(tokens []string) bool {
	for i, tok := range tokens {
		target, ok := redirectTarget(tok)
		if !ok {
			continue
		}
		if target == "" && i+1 < len(tokens) {
			target = tokens[i+1]
		}
		if isDiskDevice(target) {
			return true
		}
	}
	return false
}

// redirectTarget reconhece um operador de redireção e devolve o alvo colado
// (vazio quer dizer que o alvo é o próximo token).
func redirectTarget(tok string) (target string, ok bool) {
	s := tok
	for len(s) > 0 && s[0] >= '0' && s[0] <= '9' { // descritor: 1>, 2>>
		s = s[1:]
	}
	if !strings.HasPrefix(s, ">") {
		return "", false
	}
	s = strings.TrimPrefix(s, ">")
	s = strings.TrimPrefix(s, ">") // >>
	s = strings.TrimPrefix(s, "|") // >|
	return s, true
}

func hasFindDelete(args []string) bool {
	for _, a := range args {
		if a == "-delete" || a == "-exec" || a == "-execdir" || a == "-ok" || a == "-okdir" {
			return true
		}
	}
	return false
}

// hasFindNameFilter: um filtro por nome/caminho restringe o find a um subconjunto
// (limpeza rotineira), então não é o find catastrófico que apaga tudo.
func hasFindNameFilter(args []string) bool {
	for _, a := range args {
		switch a {
		case "-name", "-iname", "-path", "-ipath", "-wholename", "-iwholename",
			"-regex", "-iregex", "-lname", "-ilname":
			return true
		}
	}
	return false
}

func isRecursive(args []string) bool {
	for _, a := range parseUntilDoubleDash(args) {
		switch {
		case a == "--recursive":
			return true
		case strings.HasPrefix(a, "--"):
		case strings.HasPrefix(a, "-"):
			if strings.ContainsAny(a, "rR") {
				return true
			}
		}
	}
	return false
}

// positionalPaths: argumentos que não são opções, respeitando o "--".
func positionalPaths(args []string) []string {
	var paths []string
	for _, a := range afterDoubleDashInclusive(args) {
		if a != "" && !strings.HasPrefix(a, "-") {
			paths = append(paths, a)
		}
	}
	return paths
}

// parseUntilDoubleDash devolve os argumentos até o "--" (as opções).
func parseUntilDoubleDash(args []string) []string {
	for i, a := range args {
		if a == "--" {
			return args[:i]
		}
	}
	return args
}

// afterDoubleDash: só o que vem depois do "--", se houver.
func afterDoubleDash(args []string) []string {
	for i, a := range args {
		if a == "--" {
			return args[i+1:]
		}
	}
	return args
}

// afterDoubleDashInclusive: os positionais antes e depois do "--".
func afterDoubleDashInclusive(args []string) []string {
	var result []string
	past := false
	for _, a := range args {
		if a == "--" {
			past = true
			continue
		}
		if past || !strings.HasPrefix(a, "-") {
			result = append(result, a)
		}
	}
	return result
}
