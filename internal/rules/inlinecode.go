package rules

import (
	"regexp"
	"strings"

	"github.com/IronDeploy/IronBrake/internal/hook"
)

const (
	sdkDeleteDanger = "código de outra linguagem (python, node, ruby, perl, php) que apaga recursos: chamada de SDK de nuvem (delete_*, terminate_*), requisição HTTP DELETE, DROP/TRUNCATE ou remoção recursiva de pastas."
	cloudAPIDanger  = "requisição HTTP direta a uma API de nuvem com uma ação de exclusão (Action=Delete*, Terminate*, X-Amz-Target: ...Delete*)."
)

// inlineFlags: a opção de cada interpretador que recebe o código no argumento.
var inlineFlags = map[string]map[string]bool{
	"python": set("-c"),
	"node":   set("-e", "--eval", "-p", "--print"),
	"ruby":   set("-e"),
	"perl":   set("-e", "-E"),
	"php":    set("-r"),
	"bun":    set("-e", "--eval"),
	"deno":   set("eval"),
}

// interpreterFamily: python3.12 vale python, nodejs vale node.
func interpreterFamily(token string) string {
	name := programName(token)
	switch {
	case strings.HasPrefix(name, "python"):
		return "python"
	case name == "nodejs":
		return "node"
	}
	if _, ok := inlineFlags[name]; ok {
		return name
	}
	return ""
}

// inlineCode devolve o código que um interpretador recebe no argumento
// (python -c "...", node -e "...").
func inlineCode(tokens []string) (string, bool) {
	if len(tokens) < 3 {
		return "", false
	}
	flags := inlineFlags[interpreterFamily(tokens[0])]
	if flags == nil {
		return "", false
	}
	for i := 1; i+1 < len(tokens); i++ {
		if flags[tokens[i]] {
			return tokens[i+1], true
		}
	}
	return "", false
}

func callsInterpreter(commands [][]string) bool {
	for _, tokens := range commands {
		if len(tokens) > 0 && interpreterFamily(tokens[0]) != "" {
			return true
		}
	}
	return false
}

// stringLiteral casa 'texto' ou "texto" (com \ escapando).
const stringLiteral = `(?:'(?:[^'\\]|\\.)*'|"(?:[^"\\]|\\.)*")`

var (
	// shellOutCall: chamadas que rodam um comando de shell, seguidas do
	// argumento (um texto ou uma lista de textos).
	shellOutCall = regexp.MustCompile(
		`(?:\bos\.(?:system|popen)|\bsubprocess\.(?:run|call|check_call|check_output|Popen|getoutput)|` +
			`\b(?:exec|execSync|execFile|execFileSync|spawn|spawnSync|system|popen|shell_exec|passthru|proc_open)|` +
			`\bKernel\.system|\bopen3\.\w+|\bIO\.popen)\s*\(\s*(\[[^\]]*\]|` + stringLiteral + `)`)
	literalRe = regexp.MustCompile(stringLiteral)
	// crase em ruby/perl: `git push --force`
	backtickRe = regexp.MustCompile("`([^`]+)`")
)

// shellOuts extrai os comandos de shell que o código roda (os.system("..."),
// subprocess.run(["git", "push", "--force"]), execSync('...')), para que as
// regras de sempre julguem cada um. Só olha o argumento das chamadas: um
// texto solto (print("git push --force")) não roda nada.
func shellOuts(code string) []string {
	var commands []string
	for _, m := range shellOutCall.FindAllStringSubmatch(code, -1) {
		arg := m[1]
		if strings.HasPrefix(arg, "[") {
			var words []string
			for _, lit := range literalRe.FindAllString(arg, -1) {
				words = append(words, unquoteLiteral(lit))
			}
			commands = append(commands, quoteWords(words))
			continue
		}
		commands = append(commands, unquoteLiteral(arg))
	}
	for _, m := range backtickRe.FindAllStringSubmatch(code, -1) {
		commands = append(commands, m[1])
	}
	return commands
}

// unquoteLiteral tira as aspas e desfaz as barras invertidas mais comuns.
func unquoteLiteral(lit string) string {
	body := lit[1 : len(lit)-1]
	return strings.NewReplacer(`\"`, `"`, `\'`, `'`, `\\`, `\`, `\n`, "\n", `\t`, "\t").Replace(body)
}

// quoteWords monta uma linha de shell em que cada palavra é um argumento.
func quoteWords(words []string) string {
	quoted := make([]string, len(words))
	for i, w := range words {
		quoted[i] = "'" + strings.ReplaceAll(w, "'", `'\''`) + "'"
	}
	return strings.Join(quoted, " ")
}

// sdkDeletePatterns tolera a falta do "(": num heredoc o tokenizer desmonta o
// código e os parênteses somem.
var sdkDeletePatterns = regexp.MustCompile(
	`\.(?:delete|terminate|purge|deregister)_\w+` + // boto3: client.delete_bucket(, ec2.terminate_instances(
		`|\.delete(?:\s|\(|$)` + // requests.delete(, bucket.delete(, qs.delete()
		`|(?i:method\s*[:=]\s*['"]?delete\b)` + // fetch(url, {method: 'DELETE'})
		`|Net::HTTP::Delete|\.DeleteObject|\.DeleteBucket|\.TerminateInstances` +
		`|\brmtree\b|\.(?:rmSync|rmdirSync)\b|\bfs\.rm\b|\bFileUtils\.rm_rf?\b|\brm_rf\b`)

// sdkRule cobre exclusão feita por código de outra linguagem (SDK, HTTP),
// que não aparece como comando de CLI. Olha a linha inteira quando há um
// interpretador, porque o código também chega por heredoc ou pipe.
type sdkRule struct{}

var sdkDelete Rule = sdkRule{}

func (sdkRule) Name() string { return "sdk-delete" }

func (sdkRule) Check(commands [][]string, env Env) (hook.Decision, string) {
	if !callsInterpreter(commands) {
		return hook.Allow, ""
	}
	for _, text := range sqlCandidates(commands) {
		if sdkDeletePatterns.MatchString(text) {
			return decideByEnvironment(sdkDeleteDanger, commands, env)
		}
		if hasDestructiveSQL(text) { // linha de heredoc: DROP TABLE x
			return decideByEnvironment(sdkDeleteDanger, commands, env)
		}
		for _, lit := range literalRe.FindAllString(text, -1) {
			if hasDestructiveSQL(unquoteLiteral(lit)) {
				return decideByEnvironment(sdkDeleteDanger, commands, env)
			}
		}
	}
	return hook.Allow, ""
}

var cloudAction = regexp.MustCompile(
	`(?i)\bAction=(?:Delete|Terminate|Purge|Deregister|Remove)\w+|` +
		`X-Amz-Target:[^\n]*\.(?:Delete|Terminate|Purge)\w+|` +
		`X-HTTP-Method-Override:\s*DELETE`)

// hasCloudDeleteAction reconhece curl/wget que pedem uma exclusão sem usar o
// método DELETE (API de consulta da AWS, X-Amz-Target, override de método).
func hasCloudDeleteAction(tokens []string) bool {
	if len(tokens) == 0 || !downloaders[programName(tokens[0])] {
		return false
	}
	for _, arg := range tokens[1:] {
		if cloudAction.MatchString(arg) {
			return true
		}
	}
	return false
}
