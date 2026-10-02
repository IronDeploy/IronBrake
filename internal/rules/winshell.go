package rules

import (
	"encoding/base64"
	"strings"
	"unicode/utf16"
)

// Os interpretadores do Windows (cmd, PowerShell) executam o texto que
// recebem: "cmd /c git push --force" é um force push, e não um comando
// chamado cmd. Sem abrir o invólucro, a linha passaria sem ser julgada.

// winScript prepara o texto de um cmd/PowerShell para a análise: o tokenizador
// é o do shell POSIX e trata "\" como escape, o que comeria as barras de
// "C:\dados\prod". Nesses interpretadores "\" é separador de pasta e o escape
// é outro (^ no cmd, ` no PowerShell), então vira "/".
func winScript(text string) string {
	return strings.ReplaceAll(text, `\`, "/")
}

// cmdScript devolve o comando de "cmd /c COMANDO" (também /k e /r). Tudo
// depois do /c é o comando, mesmo sem aspas.
func cmdScript(args []string) (string, bool) {
	for i, a := range args {
		switch strings.ToLower(a) {
		case "/c", "/k", "/r":
			if i+1 < len(args) {
				return winScript(strings.Join(args[i+1:], " ")), true
			}
			return "", false
		}
	}
	return "", false
}

// psValueFlags são as opções do powershell/pwsh que consomem o argumento seguinte.
var psValueFlags = set("-executionpolicy", "-ep", "-windowstyle", "-w", "-configurationname",
	"-inputformat", "-outputformat", "-workingdirectory", "-wd", "-version", "-psconsolefile", "-settingsfile")

// powershellScript devolve o comando de "powershell -Command ...", de
// "-EncodedCommand BASE64" (UTF-16LE) ou do primeiro argumento solto, que o
// PowerShell também trata como comando. -File roda um script: não é texto.
func powershellScript(args []string) (string, bool) {
	for i := 0; i < len(args); i++ {
		a := strings.ToLower(args[i])
		switch {
		case a == "-command" || a == "-c" || a == "-cmd":
			if i+1 >= len(args) || args[i+1] == "-" {
				return "", false // "-" lê o comando do stdin
			}
			return winScript(strings.Join(args[i+1:], " ")), true
		case a == "-encodedcommand" || a == "-e" || a == "-ec":
			if i+1 >= len(args) {
				return "", false
			}
			text, ok := decodePowershell(args[i+1])
			return winScript(text), ok
		case a == "-file" || a == "-f":
			return "", false
		case psValueFlags[a]:
			i++
		case strings.HasPrefix(a, "-"):
		default:
			return winScript(strings.Join(args[i:], " ")), true
		}
	}
	return "", false
}

// decodePowershell lê o -EncodedCommand: base64 de texto UTF-16 little-endian.
func decodePowershell(encoded string) (string, bool) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(raw) == 0 || len(raw)%2 != 0 {
		return "", false
	}
	units := make([]uint16, len(raw)/2)
	for i := range units {
		units[i] = uint16(raw[2*i]) | uint16(raw[2*i+1])<<8
	}
	return string(utf16.Decode(units)), true
}

// removalTargets devolve os caminhos que um comando de remoção recursiva
// apaga: rm -r, e os equivalentes do Windows (rmdir/rd /s, del /s,
// Remove-Item -Recurse). ok é false para o que não é remoção recursiva.
func removalTargets(tokens []string) (paths []string, ok bool) {
	args := tokens[1:]
	switch programName(tokens[0]) {
	case "rm":
		if isRecursive(args) {
			return positionalPaths(args), true
		}
	case "rmdir", "rd", "del", "erase":
		recursive := false
		for _, a := range args {
			if strings.EqualFold(a, "/s") {
				recursive = true
			} else if !isCmdFlag(a) {
				paths = append(paths, a)
			}
		}
		return paths, recursive
	case "remove-item", "ri":
		return removeItemTargets(args)
	}
	return nil, false
}

// isCmdFlag: opção do cmd, como /s /q /f. "/data" é caminho, não opção.
func isCmdFlag(a string) bool { return len(a) == 2 && a[0] == '/' }

// removeItemTargets lê os argumentos do Remove-Item. Os nomes das opções do
// PowerShell podem ser abreviados e não diferenciam maiúsculas.
func removeItemTargets(args []string) (paths []string, ok bool) {
	recursive := false
	for i := 0; i < len(args); i++ {
		a := strings.ToLower(args[i])
		switch {
		case len(a) >= 2 && strings.HasPrefix("-recurse", a):
			recursive = true
		case a == "-path" || a == "-literalpath" || a == "-lp":
			if i+1 < len(args) {
				paths = append(paths, args[i+1])
				i++
			}
		case a == "-filter" || a == "-include" || a == "-exclude" || a == "-stream" || a == "-credential":
			i++
		case strings.HasPrefix(a, "-"):
		default:
			paths = append(paths, args[i])
		}
	}
	return paths, recursive
}
