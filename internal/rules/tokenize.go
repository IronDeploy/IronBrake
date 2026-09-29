package rules

import (
	"path/filepath"
	"strings"
	"unicode"
)

// maxNesting limita a profundidade de $(...), sh -c, eval etc.
const maxNesting = 8

// splitCommands quebra a linha nos comandos simples que o bash vai executar,
// cada um em tokens: aspas, continuação de linha, $'...', subshells,
// substituições de comando e comandos embrulhados (sudo, env, sh -c, eval...).
// Toda diferença em relação ao bash é uma forma de contornar as regras.
func splitCommands(command string) [][]string {
	return split(command, 0)
}

func split(command string, depth int) [][]string {
	var result [][]string
	vars := map[string]string{} // VAR=valor escritos antes, na mesma linha
	for _, c := range lex(command) {
		if depth < maxNesting {
			for _, sub := range c.substitutions {
				result = append(result, split(sub, depth+1)...)
			}
		}
		tokens := substituteVars(c.tokens, vars)
		recordAssignments(tokens, vars)
		result = append(result, expand(tokens, depth)...)
	}
	return result
}

func IsSingleCommand(command string) bool {
	return len(splitCommands(command)) == 1
}

// programName: sem caminho, em minúsculas (o macOS não diferencia) e sem .exe.
func programName(token string) string {
	return strings.TrimSuffix(strings.ToLower(filepath.Base(token)), ".exe")
}

type lexedCommand struct {
	tokens        []string
	substitutions []string // $(...) e `...`: o shell executa esse conteúdo
}

func lex(command string) []lexedCommand {
	runes := []rune(command)
	var commands []lexedCommand
	var current lexedCommand
	var token strings.Builder
	inToken := false

	endToken := func() {
		if inToken {
			current.tokens = append(current.tokens, token.String())
			token.Reset()
			inToken = false
		}
	}
	endCommand := func() {
		endToken()
		if len(current.tokens) > 0 || len(current.substitutions) > 0 {
			commands = append(commands, current)
		}
		current = lexedCommand{}
	}

	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case r == '\\':
			if i+1 < len(runes) {
				i++
				if runes[i] != '\n' { // barra + quebra de linha: continuação
					token.WriteRune(runes[i])
					inToken = true
				}
			}
		case r == '\'':
			end := indexOf(runes, i+1, '\'')
			token.WriteString(string(runes[i+1 : end]))
			inToken, i = true, end
		case r == '"':
			i = lexDoubleQuoted(runes, i+1, &token, &current.substitutions)
			inToken = true
		case r == '$' && next(runes, i) == '\'':
			i = lexANSIC(runes, i+2, &token)
			inToken = true
		case r == '$' && next(runes, i) == '"':
			i = lexDoubleQuoted(runes, i+2, &token, &current.substitutions)
			inToken = true
		case r == '$' && next(runes, i) == '(', r == '`':
			i = lexSubstitution(runes, i, &token, &current.substitutions)
			inToken = true
		case r == ';' || r == '&' || r == '|' || r == '\n' || r == '(' || r == ')':
			endCommand()
		case unicode.IsSpace(r):
			endToken()
		default:
			token.WriteRune(r)
			inToken = true
		}
	}
	endCommand()
	return commands
}

// lexDoubleQuoted devolve a posição da aspa que fecha. Aqui a barra só
// escapa $ ` " \ e a quebra de linha.
func lexDoubleQuoted(runes []rune, start int, token *strings.Builder, subs *[]string) int {
	for i := start; i < len(runes); i++ {
		r := runes[i]
		switch {
		case r == '"':
			return i
		case r == '\\' && i+1 < len(runes):
			switch runes[i+1] {
			case '\n':
				i++
			case '$', '`', '"', '\\':
				token.WriteRune(runes[i+1])
				i++
			default:
				token.WriteRune('\\')
			}
		case r == '$' && next(runes, i) == '(', r == '`':
			i = lexSubstitution(runes, i, token, subs)
		default:
			token.WriteRune(r)
		}
	}
	return len(runes)
}

func lexSubstitution(runes []rune, start int, token *strings.Builder, subs *[]string) int {
	var inner, end int
	if runes[start] == '`' {
		inner = start + 1
		end = indexOf(runes, inner, '`')
	} else {
		inner = start + 2
		end = matchParen(runes, inner)
	}
	*subs = append(*subs, string(runes[inner:end]))
	token.WriteString(string(runes[start:min(end+1, len(runes))]))
	return end
}

// matchParen ignora parênteses entre aspas; devolve len(runes) se não fechar.
func matchParen(runes []rune, start int) int {
	depth := 1
	for i := start; i < len(runes); i++ {
		switch runes[i] {
		case '\\':
			i++
		case '\'':
			i = indexOf(runes, i+1, '\'')
		case '"':
			for i++; i < len(runes) && runes[i] != '"'; i++ {
				if runes[i] == '\\' {
					i++
				}
			}
		case '(':
			depth++
		case ')':
			if depth--; depth == 0 {
				return i
			}
		}
	}
	return len(runes)
}

// lexANSIC traduz os escapes de $'...'.
func lexANSIC(runes []rune, start int, token *strings.Builder) int {
	for i := start; i < len(runes); i++ {
		r := runes[i]
		if r == '\'' {
			return i
		}
		if r != '\\' || i+1 >= len(runes) {
			token.WriteRune(r)
			continue
		}
		i++
		switch c := runes[i]; c {
		case 'n':
			token.WriteRune('\n')
		case 't':
			token.WriteRune('\t')
		case 'r':
			token.WriteRune('\r')
		case 'a':
			token.WriteRune('\a')
		case 'b':
			token.WriteRune('\b')
		case 'f':
			token.WriteRune('\f')
		case 'v':
			token.WriteRune('\v')
		case 'e', 'E':
			token.WriteRune(0x1b)
		case '\\', '\'', '"', '?':
			token.WriteRune(c)
		case 'x', 'u', 'U':
			limit := map[rune]int{'x': 2, 'u': 4, 'U': 8}[c]
			value, n := readDigits(runes, i+1, limit, 16)
			if n == 0 {
				token.WriteRune('\\')
				token.WriteRune(c)
				continue
			}
			token.WriteRune(rune(value))
			i += n
		case '0', '1', '2', '3', '4', '5', '6', '7':
			value, n := readDigits(runes, i, 3, 8)
			token.WriteRune(rune(value))
			i += n - 1
		case 'c':
			if i+1 < len(runes) {
				i++
				token.WriteRune(runes[i] & 0x1f)
			}
		default:
			token.WriteRune('\\')
			token.WriteRune(c)
		}
	}
	return len(runes)
}

func readDigits(runes []rune, start, limit, base int) (value, n int) {
	for n < limit && start+n < len(runes) {
		d := digitValue(runes[start+n])
		if d < 0 || d >= base {
			break
		}
		value = value*base + d
		n++
	}
	return value, n
}

func digitValue(r rune) int {
	switch {
	case r >= '0' && r <= '9':
		return int(r - '0')
	case r >= 'a' && r <= 'f':
		return int(r-'a') + 10
	case r >= 'A' && r <= 'F':
		return int(r-'A') + 10
	}
	return -1
}

func next(runes []rune, i int) rune {
	if i+1 < len(runes) {
		return runes[i+1]
	}
	return 0
}

func indexOf(runes []rune, start int, r rune) int {
	for i := start; i < len(runes); i++ {
		if runes[i] == r {
			return i
		}
	}
	return len(runes)
}

// expand desembrulha o comando e, se ele só roda um script (sh -c, eval,
// watch), analisa o script no lugar dele.
func expand(tokens []string, depth int) [][]string {
	tokens = unwrap(tokens)
	if len(tokens) == 0 {
		return nil
	}
	if depth < maxNesting {
		if script, ok := innerScript(tokens); ok {
			return split(script, depth+1)
		}
		// python -c, node -e...: o comando pode continuar valendo como
		// interpretador (para a regra de SDK), e o que o código roda no shell
		// (os.system, subprocess) é julgado como comando de verdade.
		if code, ok := inlineCode(tokens); ok {
			result := [][]string{tokens}
			for _, command := range shellOuts(code) {
				result = append(result, split(command, depth+1)...)
			}
			return result
		}
	}
	return [][]string{tokens}
}

// shellKeywords vêm antes do comando: "do kubectl delete ...".
var shellKeywords = set("!", "{", "}", "if", "then", "else", "elif", "do", "while", "until")

// wrapper descreve um programa que só roda outro.
type wrapper struct {
	valueFlags  map[string]bool
	skipArgs    int
	assignments bool
}

var wrappers = map[string]wrapper{
	"sudo":       {valueFlags: set("-u", "-g", "-h", "-p", "-C", "-r", "-t", "-U", "-D", "-R", "-T")},
	"doas":       {valueFlags: set("-u", "-C")},
	"env":        {valueFlags: set("-u", "-C", "-S", "--unset", "--chdir", "--split-string"), assignments: true},
	"command":    {},
	"builtin":    {},
	"exec":       {valueFlags: set("-a")},
	"time":       {},
	"nohup":      {},
	"nice":       {valueFlags: set("-n", "--adjustment")},
	"ionice":     {valueFlags: set("-c", "-n", "-p", "--class", "--classdata")},
	"timeout":    {valueFlags: set("-s", "-k", "--signal", "--kill-after"), skipArgs: 1},
	"stdbuf":     {valueFlags: set("-i", "-o", "-e")},
	"caffeinate": {valueFlags: set("-t", "-w")},
	"xargs":      {valueFlags: set("-I", "-n", "-P", "-L", "-d", "-a", "-E", "-s", "--max-args", "--max-procs", "--delimiter", "--arg-file", "--replace", "--max-lines")},
}

func unwrap(tokens []string) []string {
	for len(tokens) > 0 {
		switch first := tokens[0]; {
		case isAssignment(first), shellKeywords[first]:
			tokens = tokens[1:]
		default:
			w, ok := wrappers[programName(first)]
			if !ok {
				return tokens
			}
			tokens = skipWrapperArgs(tokens[1:], w)
		}
	}
	return tokens
}

func skipWrapperArgs(args []string, w wrapper) []string {
	positional := w.skipArgs
	for len(args) > 0 {
		arg := args[0]
		switch {
		case arg == "--":
			return args[1:]
		case len(arg) > 1 && arg[0] == '-':
			if w.valueFlags[arg] && len(args) > 1 {
				args = args[2:]
			} else {
				args = args[1:]
			}
		case w.assignments && isAssignment(arg):
			args = args[1:]
		case positional > 0:
			positional--
			args = args[1:]
		default:
			return args
		}
	}
	return args
}

func isAssignment(token string) bool {
	name, _, found := strings.Cut(token, "=")
	if !found || name == "" || unicode.IsDigit(rune(name[0])) {
		return false
	}
	for _, r := range name {
		if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func innerScript(tokens []string) (string, bool) {
	switch programName(tokens[0]) {
	case "sh", "bash", "zsh", "dash", "ksh":
		for i := 1; i < len(tokens); i++ {
			flag := tokens[i]
			switch {
			case flag == "-o" || flag == "+o":
				i++
			case strings.HasPrefix(flag, "--"):
			case len(flag) > 1 && (flag[0] == '-' || flag[0] == '+'):
				if strings.ContainsRune(flag[1:], 'c') && i+1 < len(tokens) {
					return tokens[i+1], true
				}
			default:
				return "", false // bash script.sh
			}
		}
	case "eval":
		if len(tokens) > 1 {
			return strings.Join(tokens[1:], " "), true
		}
	case "watch":
		args := skipWrapperArgs(tokens[1:], wrapper{valueFlags: set("-n", "--interval")})
		if len(args) > 0 {
			return strings.Join(args, " "), true
		}
	}
	return "", false
}

func set(values ...string) map[string]bool {
	m := make(map[string]bool, len(values))
	for _, v := range values {
		m[v] = true
	}
	return m
}
