package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/IronDeploy/IronBrake/internal/audit"
	"github.com/IronDeploy/IronBrake/internal/hook"
	"github.com/IronDeploy/IronBrake/internal/safefile"
	"github.com/IronDeploy/IronBrake/internal/setup"
	"github.com/IronDeploy/IronBrake/internal/toolpath"
	"github.com/IronDeploy/IronBrake/internal/watch"
)

const testEvent = `{"tool_name":"Bash","tool_input":{"command":"git push --force origin main"}}`

const (
	nameSettings = "settings.json contém o hook do Iron Brake"
	nameBinary   = "o binário do hook existe e é executável"
	nameResponds = "o hook bloqueia um force push de teste"
	nameTimeout  = "o timeout do hook dá tempo ao Iron Brake"
	nameTools    = "os programas que o Iron Brake executa (terraform, tofu, terragrunt) são confiáveis"
	nameAudit    = "o log de auditoria grava e a corrente está íntegra"
	nameVersion  = "versão"

	nameCoverage = "o hook está vendo os comandos que o agente executa (iron watch)"
	nameAWSTag   = "etiqueta do agente na AWS (opcional)"
)

type Result struct {
	Name   string
	OK     bool
	Detail string
	Fix    string // preenchido só quando falha
}

// Run executa as 4 verificações em ordem. Se uma falha, as que dependem dela
// também falham: não dá para afirmar que protege sem ter verificado.
func Run(settingsPath, version string, timeout time.Duration) []Result {
	settings, hooks := checkSettings(settingsPath)
	binary, responds := notChecked(nameBinary, 1), notChecked(nameResponds, 1)
	hookTimeout := notChecked(nameTimeout, 1)

	if settings.OK {
		chosen := pickHook(hooks)
		binary = checkBinary(chosen.Command)
		hookTimeout = checkTimeout(chosen.Timeout)

		if binary.OK {
			responds = checkResponds(chosen.Command, []string{setup.HookSubcommand}, testEvent, timeout)
		} else {
			responds = notChecked(nameResponds, 2)
		}
	}

	return []Result{settings, binary, responds, hookTimeout, {Name: nameVersion, OK: true, Detail: version}}
}

// Extra é o que o RunAll confere além do hook: de onde o Iron Brake executa
// terraform/tofu/terragrunt e o log de auditoria.
type Extra struct {
	Log        audit.Log
	ConfigPath string // ~/.iron/config.yaml
	ProjectDir string

	// Coverage, se não for nil, confere nos transcripts do agente se cada comando
	// recente teve uma decisão do hook.
	Coverage *Coverage

	// AWSTag, se não for vazio, é o AWS_SDK_UA_APP_ID que o "iron init" grava
	// para o agente; o doctor só informa se ele está no settings.json.
	AWSTag string
}

// Coverage diz onde conferir a cobertura do hook.
type Coverage struct {
	Dirs   []string     // pastas de transcripts
	Source watch.Source // classe dos comandos e decisões gravadas
	Since  time.Time    // só conta o que aconteceu depois (o hook não existia antes)
}

// RunAll é Run mais a verificação dos programas e a do log de auditoria, antes
// da versão. O log vem depois da resposta do hook: o force push de teste faz o
// hook gravar uma linha, e uma gravação boa apaga o aviso de falha anterior.
func RunAll(settingsPath, version string, timeout time.Duration, extra Extra) []Result {
	results := Run(settingsPath, version, timeout)
	last := len(results) - 1

	middle := []Result{CheckTools(extra.ConfigPath, extra.ProjectDir), CheckAudit(extra.Log)}
	if extra.Coverage != nil {
		middle = append(middle, CheckCoverage(*extra.Coverage))
	}
	if extra.AWSTag != "" {
		middle = append(middle, CheckAWSTag(settingsPath, extra.AWSTag))
	}
	return append(results[:last:last], append(middle, results[last])...)
}

// CheckCoverage falha se algum comando de shell dos transcripts rodou sem
// nenhuma decisão do hook: o hook não está disparando (agente sem hook,
// sessão aberta antes do "iron init", configuração errada). Sem transcripts ou
// sem comandos, passa: não há o que conferir.
func CheckCoverage(c Coverage) Result {
	result := Result{Name: nameCoverage}

	var dirs []string
	for _, dir := range c.Dirs {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			dirs = append(dirs, dir)
		}
	}
	if len(dirs) == 0 {
		result.OK = true
		result.Detail = "sem transcripts do agente para esta pasta: nada a conferir"
		return result
	}

	gaps, checked, _, err := watch.Scan(dirs, c.Source, c.Since)
	switch {
	case err != nil:
		result.Detail = "não consegui ler os transcripts: " + err.Error()
		result.Fix = "rode iron watch --once para ver o erro."
	case checked == 0:
		result.OK = true
		result.Detail = "nenhum comando de shell desde " + c.Since.Local().Format("02/01 15:04")
	case len(gaps) == 0:
		result.OK = true
		result.Detail = fmt.Sprintf("%d comando(s) de shell desde %s, todos com decisão do Iron Brake", checked, c.Since.Local().Format("02/01 15:04"))
	default:
		result.Detail = fmt.Sprintf("%d de %d comando(s) de shell desde %s rodaram SEM decisão do Iron Brake", len(gaps), checked, c.Since.Local().Format("02/01 15:04"))
		result.Fix = "o hook não está disparando para esse agente. Se instalou o hook com o agente aberto, reinicie a sessão; senão, confira a configuração do agente (no Claude Code, /hooks). Rode iron watch --once (com --agent=NOME para os agentes que não são o Claude Code) para ver quais."
	}
	return result
}

// CheckAWSTag só informa: a etiqueta é opcional (iron init --no-aws-tag).
func CheckAWSTag(settingsPath, appID string) Result {
	result := Result{Name: nameAWSTag, OK: true}

	data, err := safefile.Read(settingsPath, 1<<20)
	if err != nil {
		result.Detail = "não consegui ler o settings.json"
		return result
	}
	var doc struct {
		Env map[string]string `json:"env"`
	}
	if json.Unmarshal(data, &doc) != nil {
		result.Detail = "não consegui ler o env do settings.json"
		return result
	}

	switch current := doc.Env["AWS_SDK_UA_APP_ID"]; {
	case current == appID:
		result.Detail = "AWS_SDK_UA_APP_ID=" + appID + ": o CloudTrail mostra app/" + appID + " nas chamadas do agente"
	case current == "":
		result.Detail = "não gravada: o CloudTrail não distingue o agente. rode iron init (ou ignore, se usou --no-aws-tag de propósito)"
	default:
		result.Detail = fmt.Sprintf("AWS_SDK_UA_APP_ID vale %q: o CloudTrail não vai mostrar app/%s", current, appID)
	}
	return result
}

// CheckTools falha se o config.yaml tem erro, se um caminho configurado não
// serve ou se o programa achado no PATH é suspeito (dentro do projeto ou
// gravável por qualquer usuário). Programa não instalado não é falha: só
// importa a quem usa terraform apply.
func CheckTools(configPath, projectDir string) Result {
	result := Result{Name: nameTools}

	cfg, err := toolpath.Load(configPath)
	if err != nil {
		result.Detail = err.Error()
		result.Fix = "corrija " + configPath + " (tools.terraform: /caminho/absoluto/terraform) ou apague o arquivo."
		return result
	}

	var found, failed []string
	for _, tool := range toolpath.Tools {
		path, err := toolpath.Resolve(tool, cfg, projectDir)
		var untrusted *toolpath.UntrustedError
		switch {
		case errors.As(err, &untrusted):
			failed = append(failed, untrusted.Error())
		case err != nil:
			// não instalado
		default:
			source := "PATH"
			if cfg.Tools[tool] != "" {
				source = "config.yaml"
			}
			found = append(found, fmt.Sprintf("%s: %s (%s)", tool, path, source))
		}
	}

	switch {
	case len(failed) > 0:
		result.Detail = strings.Join(failed, "; ")
		result.Fix = "instale o programa num caminho só seu, ou informe o caminho absoluto em tools.<programa> no " + configPath + ". Um programa falso mostraria ao Iron Brake um plano inventado."
	case len(found) == 0:
		result.OK = true
		result.Detail = "nenhum instalado (só importa para terraform apply)"
	default:
		result.OK = true
		result.Detail = strings.Join(found, "; ")
	}
	return result
}

// CheckAudit falha se o log não pode ser gravado, se a última gravação do
// hook falhou ou se a corrente está quebrada. Sem isso, a falha só aparece
// no stderr do hook, que o Claude Code mostra apenas quando o comando é
// bloqueado.
func CheckAudit(log audit.Log) Result {
	result := Result{Name: nameAudit}
	h := log.Check()
	path := log.Path

	switch {
	case h.WriteErr != nil:
		result.Detail = fmt.Sprintf("não consigo gravar em %s: %v", path, h.WriteErr)
		result.Fix = "confira o dono e as permissões da pasta " + filepath.Dir(path) + " (0700, do seu usuário) e o espaço em disco. Enquanto isso, as decisões do Iron Brake não ficam registradas."
	case h.LastFailure != nil:
		result.Detail = fmt.Sprintf("a última gravação do hook falhou em %s: %s", h.LastFailure.Time.Format("2006-01-02 15:04:05"), h.LastFailure.Reason)
		result.Fix = "resolva o motivo acima (o aviso some na próxima gravação que der certo) e rode iron doctor de novo."
	case !h.Chain.OK && h.Chain.BrokenAt > 0:
		result.Detail = fmt.Sprintf("a corrente quebrou na linha %d de %s: %s", h.Chain.BrokenAt, h.Chain.File, h.Chain.Reason)
		result.Fix = "rode iron audit verify para ver o detalhe. Se a alteração foi legítima, mova os arquivos " + filepath.Base(path) + "* para outra pasta e o log recomeça."
	case !h.Chain.OK:
		result.Detail = "não consegui verificar o log: " + h.Chain.Reason
		result.Fix = "rode iron audit verify e rode iron doctor de novo."
	default:
		result.OK = true
		result.Detail = fmt.Sprintf("%s: %d linha(s) em %d arquivo(s), %s", path, h.Chain.Lines, h.Chain.Files, humanSize(h.Chain.Bytes))
		if h.Chain.Missing {
			result.Detail = path + ": ainda sem decisões registradas"
		}
	}
	return result
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

func Print(w io.Writer, results []Result) {
	failed := 0

	for i, r := range results {
		status := "OK   "
		if !r.OK {
			status = "FALHA"
			failed++
		}

		fmt.Fprintf(w, "%s  %d/%d  %s\n", status, i+1, len(results), r.Name)
		if r.Detail != "" {
			fmt.Fprintf(w, "             %s\n", r.Detail)
		}
		if r.Fix != "" {
			fmt.Fprintf(w, "             Como corrigir: %s\n", r.Fix)
		}
	}

	if failed > 0 {
		fmt.Fprintf(w, "\n%d de %d verificações falharam.\n", failed, len(results))
	} else {
		fmt.Fprintln(w, "\nTudo certo: o hook está instalado, bloqueou o force push de teste e o log de auditoria está gravando.")
	}
}

func AllOK(results []Result) bool {
	return !slices.ContainsFunc(results, func(r Result) bool { return !r.OK })
}

func notChecked(name string, dependsOn int) Result {
	return Result{
		Name:   name,
		Detail: "não foi possível verificar",
		Fix:    fmt.Sprintf("corrija primeiro a verificação %d e rode iron doctor de novo.", dependsOn),
	}
}

func checkSettings(settingsPath string) (Result, []setup.Hook) {
	result := Result{Name: nameSettings}

	hooks, err := setup.FindHooks(settingsPath)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		result.Detail = settingsPath + " não existe"
		result.Fix = "rode iron init na raiz do projeto."
	case err != nil:
		result.Detail = err.Error()
		result.Fix = "corrija o arquivo (JSON estrito: sem comentários nem vírgula sobrando) e rode iron doctor de novo."
	case len(hooks) == 0:
		if renamed, _ := setup.FindRenamedHooks(settingsPath); len(renamed) > 0 {
			result.Detail = fmt.Sprintf("há um hook com o argumento \"hook\" apontando para %s, mas o doctor só reconhece programas chamados iron (ou iron.exe)", renamed[0])
			result.Fix = "renomeie o binário para iron (o install.sh já instala assim) e rode iron init de novo na raiz do projeto; remova o hook antigo do settings.json."
			break
		}
		result.Detail = "o arquivo existe, mas não tem o hook do Iron Brake"
		result.Fix = "rode iron init para adicionar o hook sem apagar o resto do arquivo."
	default:
		result.OK = true
		result.Detail = fmt.Sprintf("%d hook(s) em %s", len(hooks), settingsPath)
	}

	return result, hooks
}

// pickHook prefere o primeiro hook cujo binário funciona.
func pickHook(hooks []setup.Hook) setup.Hook {
	for _, h := range hooks {
		if checkBinary(h.Command).OK {
			return h
		}
	}
	return hooks[0]
}

// checkTimeout: um timeout menor que hook.MinTimeout faz o Claude Code
// desistir antes do deny por prazo do Iron Brake, e o comando passa.
func checkTimeout(seconds float64) Result {
	result := Result{Name: nameTimeout}
	minimum := hook.MinTimeout.Seconds()

	switch {
	case seconds == 0:
		result.OK = true
		result.Detail = "padrão do Claude Code (600 s)"
	case seconds >= minimum:
		result.OK = true
		result.Detail = fmt.Sprintf("%g s", seconds)
	default:
		result.Detail = fmt.Sprintf("%g s: o Claude Code desiste antes do prazo do Iron Brake (%g s) e deixa o comando passar", seconds, hook.Deadline.Seconds())
		result.Fix = fmt.Sprintf("remova o campo \"timeout\" do hook no settings.json (o padrão é 600 s) ou use pelo menos %g.", minimum)
	}
	return result
}

func checkBinary(command string) Result {
	result := Result{Name: nameBinary}

	// Caminho relativo seria resolvido dentro do projeto, que pode ser de
	// terceiros: só executamos caminho absoluto ou procurado no PATH.
	// "/" também é separador no Windows: ./bin/iron é relativo lá também.
	if strings.ContainsAny(command, "/"+string(filepath.Separator)) && !filepath.IsAbs(command) {
		result.Detail = fmt.Sprintf("o caminho %s é relativo", command)
		result.Fix = "rode iron init de novo para gravar o caminho absoluto."
		return result
	}

	path, err := exec.LookPath(command)
	if err != nil {
		result.Detail = fmt.Sprintf("o binário %s não existe ou não tem permissão de execução", command)
		result.Fix = "recompile com go build -o <destino>/iron ./cmd/iron e rode iron init de novo."
		return result
	}

	result.OK = true
	result.Detail = path
	return result
}

// checkResponds roda o hook como o agente faria, sem shell, e exige o código 2
// para um force push. event é o stdin no formato do agente.
func checkResponds(command string, args []string, event string, timeout time.Duration) Result {
	return checkRespondsWith(command, args, event, timeout, func(code int, _ string) (bool, string) {
		return code == 2, "o force push de teste foi bloqueado (código 2)"
	})
}

// checkRespondsWith é o checkResponds com a definição de "bloqueou" do agente:
// blocked recebe o código de saída e o stdout, e devolve se bloqueou e como.
func checkRespondsWith(command string, args []string, event string, timeout time.Duration, blocked func(code int, stdout string) (bool, string)) Result {
	result := Result{Name: nameResponds}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var stdout strings.Builder
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Stdin = strings.NewReader(event)
	cmd.Stdout = &stdout
	cmd.WaitDelay = time.Second
	err := cmd.Run()

	var exitErr *exec.ExitError
	code := 0
	if errors.As(err, &exitErr) {
		code = exitErr.ExitCode()
	}

	switch ok, how := blocked(code, stdout.String()); {
	case ctx.Err() != nil:
		result.Detail = fmt.Sprintf("o hook não respondeu em %s", timeout)
		result.Fix = "rode " + command + " hook e veja por que trava; o agente deixa passar o comando de um hook que estoura o tempo."
	case ok:
		result.OK = true
		result.Detail = how
	case err == nil:
		result.Detail = "o hook liberou o force push de teste (saiu com código 0 sem bloquear)"
		result.Fix = "recompile o binário (go build) e rode iron init de novo; este hook NÃO está protegendo."
	case exitErr != nil:
		result.Detail = fmt.Sprintf("o hook saiu com código %d e não bloqueou como esperado", code)
		result.Fix = "recompile o binário (go build) e rode iron init de novo."
	default:
		result.Detail = "não consegui executar o hook: " + err.Error()
		result.Fix = "confira as permissões do arquivo e rode iron doctor de novo."
	}

	return result
}
