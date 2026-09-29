package rules

import (
	"slices"
	"strings"

	"github.com/IronDeploy/IronBrake/internal/hook"
)

// Motivos e descrições nunca repetem o comando: ele pode ter segredos.
const (
	forcePushDenyReason = "Iron Brake: force push bloqueado. Ele reescreve o histórico remoto e pode apagar o trabalho de outras pessoas. Use um push normal; se realmente for necessário, peça ao usuário para executar."
	forcePushAskReason  = "Iron Brake: --force-with-lease ainda reescreve o histórico remoto. Confirme com o usuário antes de continuar. Se rodar, commits de outras pessoas podem ser apagados do remoto sem volta."
	resetHardDanger     = "git reset --hard descarta alterações não commitadas sem volta (git stash guarda em vez de apagar)."
	cleanForceDanger    = "git clean -f apaga arquivos não rastreados pelo git sem volta (git clean -n mostra o que seria apagado)."

	branchDeleteDanger    = "git branch -D apaga a branch mesmo sem merge: o trabalho dela pode se perder."
	tagDeleteDanger       = "git tag -d apaga a tag; com push --delete some também da remota: releases e pipelines que dependem dessa versão deixam de encontrá-la."
	stashDropDanger       = "git stash drop/clear apaga alterações guardadas sem volta."
	reflogExpireDanger    = "git reflog expire remove o histórico de recuperação: depois dele um commit perdido não volta."
	gcPruneDanger         = "git gc --prune=now apaga objetos inalcançáveis e elimina a rede de segurança do reflog."
	filterBranchDanger    = "git filter-branch/filter-repo reescreve todo o histórico do repositório: todos os commits mudam de hash, e o histórico antigo se perde se o resultado for enviado com force push."
	updateRefDeleteDanger = "git update-ref -d apaga uma referência direto, sem rede de segurança."
	restoreDanger         = "git restore/checkout descarta alterações do diretório de trabalho sem volta (git stash guarda em vez de apagar)."

	pushDeleteProtectedReason = "Iron Brake: apagar uma branch protegida no remoto bloqueado. Isso remove a branch principal para todo mundo. Peça ao usuário para executar."
	pushDeleteDanger          = "git push apaga a branch remota: quem estiver baseado nela perde a referência."
)

var (
	gitForcePush       = commandRule{name: "git-force-push", check: checkGitPush}
	gitPushDelete      = commandRule{name: "git-push-delete", check: checkGitPushDelete}
	gitDiscard         = dangerRule{name: "git-discard", match: matchGitDiscard}
	gitPushVar    Rule = gitPushVarRule{}
)

const pushVarReason = "Iron Brake: confirme antes de executar: git push com uma variável que a linha não define ($F, $OPTS...). Ela pode conter --force, e o Iron Brake não vê o valor."

// gitPushVarRule: git push com argumento que vem de variável ou de comando e
// pode esconder --force. Variável com nome de branch, remoto, tag ou ref
// ($BRANCH, $REMOTE) e o resultado de um comando git ($(git branch
// --show-current)) são o uso normal e passam. Dentro de scripts lidos de
// arquivo, o normal é usar variável, então não vale.
type gitPushVarRule struct{}

func (gitPushVarRule) Name() string { return "git-push-variable" }

func (gitPushVarRule) Check(commands [][]string, env Env) (hook.Decision, string) {
	if env.inScript {
		return hook.Allow, ""
	}
	for _, tokens := range commands {
		sub, args := gitSubcommand(tokens)
		if sub != "push" {
			continue
		}
		for _, arg := range args {
			if hasUnresolvedVar(arg) && !isPlainRefVar(arg) {
				return hook.Ask, pushVarReason
			}
		}
	}
	return hook.Allow, ""
}

var refVarNames = []string{"branch", "remote", "ref", "tag", "origin", "head", "upstream", "repo", "url", "dest", "target"}

// isPlainRefVar: o argumento só usa variáveis com cara de branch/remoto/tag,
// ou o resultado de um comando git de leitura. "+$X" é refspec de força.
func isPlainRefVar(arg string) bool {
	if strings.HasPrefix(arg, "+") || strings.HasPrefix(arg, "-") {
		return false
	}
	if strings.HasPrefix(arg, "$(git ") || strings.HasPrefix(arg, "`git ") {
		return !strings.ContainsAny(arg, ";&|") && !strings.Contains(arg, "push")
	}
	refs := varRef.FindAllStringSubmatch(arg, -1)
	if len(refs) == 0 {
		return false // crase ou $( que não é git
	}
	rest := varRef.ReplaceAllString(arg, "")
	if strings.ContainsAny(rest, "$`") {
		return false
	}
	for _, m := range refs {
		name := strings.ToLower(m[1] + m[2])
		if !slices.ContainsFunc(refVarNames, func(n string) bool { return strings.Contains(name, n) }) {
			return false
		}
	}
	return true
}

// protectedBranches: apagar uma destas no remoto é sempre bloqueado.
var protectedBranches = set("main", "master", "production", "prod", "prd", "release", "develop", "trunk")

var gitValueFlags = map[string]bool{
	"-C": true, "-c": true, "--git-dir": true, "--work-tree": true, "--namespace": true,
}

// gitSubcommand devolve o subcomando e os argumentos depois dele ("" se não
// for git).
func gitSubcommand(tokens []string) (string, []string) {
	if len(tokens) == 0 || programName(tokens[0]) != "git" {
		return "", nil
	}
	for i := 1; i < len(tokens); i++ {
		switch {
		case gitValueFlags[tokens[i]]:
			i++
		case strings.HasPrefix(tokens[i], "-"):
		default:
			return tokens[i], tokens[i+1:]
		}
	}
	return "", nil
}

func checkGitPush(tokens []string) (hook.Decision, string) {
	sub, args := gitSubcommand(tokens)
	if sub != "push" {
		return hook.Allow, ""
	}

	decision, reason := hook.Allow, ""
	for _, arg := range args {
		switch {
		case isForceFlag(arg):
			return hook.Deny, forcePushDenyReason
		case strings.HasPrefix(arg, "--force-"): // --force-with-lease, --force-if-includes
			decision, reason = hook.Ask, forcePushAskReason
		}
	}

	return decision, reason
}

// checkGitPushDelete pega git push origin :branch e git push --delete/-d.
// Branch protegida (main, master, production...) é deny; qualquer outra é ask,
// porque apagar a branch remota errada também custa caro.
func checkGitPushDelete(tokens []string) (hook.Decision, string) {
	sub, args := gitSubcommand(tokens)
	if sub != "push" {
		return hook.Allow, ""
	}

	deleteFlag := false
	var branches []string
	for _, a := range args {
		switch {
		case a == "--delete", a == "-d":
			deleteFlag = true
		case strings.HasPrefix(a, ":") && len(a) > 1: // refspec de exclusão :main
			branches = append(branches, branchName(a[1:]))
		}
	}
	if deleteFlag { // git push --delete origin main: o remoto e a(s) branch(es) são positionais
		for _, a := range args {
			if !strings.HasPrefix(a, "-") {
				branches = append(branches, branchName(a))
			}
		}
	}
	if len(branches) == 0 {
		return hook.Allow, ""
	}
	for _, b := range branches {
		if protectedBranches[strings.ToLower(b)] {
			return hook.Deny, pushDeleteProtectedReason
		}
	}
	return hook.Ask, "Iron Brake: confirme antes de executar: " + pushDeleteDanger
}

func branchName(ref string) string {
	return strings.TrimPrefix(ref, "refs/heads/")
}

func matchGitDiscard(tokens []string) (string, bool) {
	sub, args := gitSubcommand(tokens)
	switch {
	case sub == "reset" && slices.Contains(args, "--hard"):
		return resetHardDanger, true
	case sub == "clean" && isCleanForced(args):
		return cleanForceDanger, true
	case sub == "branch" && isBranchForceDelete(args):
		return branchDeleteDanger, true
	case sub == "tag" && (slices.Contains(args, "-d") || slices.Contains(args, "--delete")):
		return tagDeleteDanger, true
	case sub == "stash" && isStashDrop(args):
		return stashDropDanger, true
	case sub == "reflog" && slices.Contains(args, "expire"):
		return reflogExpireDanger, true
	case sub == "gc" && isPruneNow(args):
		return gcPruneDanger, true
	case sub == "filter-branch" || sub == "filter-repo":
		return filterBranchDanger, true
	case sub == "update-ref" && (slices.Contains(args, "-d") || slices.Contains(args, "--delete")):
		return updateRefDeleteDanger, true
	case sub == "restore" && restoreTouchesWorktree(args):
		return restoreDanger, true
	case sub == "checkout" && isCheckoutDiscard(args):
		return restoreDanger, true
	}
	return "", false
}

// isBranchForceDelete: -D, --delete --force, ou agrupadas (-Df, -fD).
func isBranchForceDelete(args []string) bool {
	deleteAsked, forceAsked := false, false
	for _, a := range args {
		switch {
		case a == "-D":
			return true
		case a == "--delete", a == "-d":
			deleteAsked = true
		case a == "--force", a == "-f":
			forceAsked = true
		case strings.HasPrefix(a, "--"):
		case strings.HasPrefix(a, "-"): // agrupadas
			if strings.Contains(a, "D") {
				return true
			}
			deleteAsked = deleteAsked || strings.Contains(a, "d")
			forceAsked = forceAsked || strings.Contains(a, "f")
		}
	}
	return deleteAsked && forceAsked
}

func isStashDrop(args []string) bool {
	return len(args) > 0 && (args[0] == "drop" || args[0] == "clear")
}

// isPruneNow: --prune=now ou --prune=all (não --prune=never).
func isPruneNow(args []string) bool {
	for _, a := range args {
		if v, ok := strings.CutPrefix(a, "--prune="); ok && v != "never" {
			return true
		}
	}
	return false
}

// restoreTouchesWorktree: restore mexe no diretório de trabalho, a menos que
// seja só --staged (que apenas tira do stage).
func restoreTouchesWorktree(args []string) bool {
	staged, worktree := false, false
	for _, a := range args {
		switch a {
		case "--staged", "-S":
			staged = true
		case "--worktree", "-W":
			worktree = true
		}
	}
	if staged && !worktree {
		return false
	}
	return true
}

// isCheckoutDiscard: git checkout . ou git checkout -- <path> descartam
// alterações. Trocar de branch (git checkout main, -b nova) não conta.
func isCheckoutDiscard(args []string) bool {
	for _, a := range args {
		if a == "-b" || a == "-B" || a == "--orphan" {
			return false
		}
	}
	return slices.Contains(args, "--") || slices.Contains(args, ".")
}

// isCleanForced: tem -f e não tem -n (dry run).
func isCleanForced(args []string) bool {
	force, dryRun := false, false
	for _, arg := range args {
		switch {
		case arg == "--force":
			force = true
		case arg == "--dry-run":
			dryRun = true
		case strings.HasPrefix(arg, "--"):
		case strings.HasPrefix(arg, "-"): // agrupadas: -fd, -fdx, -nd
			force = force || strings.Contains(arg, "f")
			dryRun = dryRun || strings.Contains(arg, "n")
		}
	}
	return force && !dryRun
}

func isForceFlag(arg string) bool {
	switch {
	case arg == "--force", arg == "--mirror": // --mirror força e apaga refs remotas
		return true
	case strings.HasPrefix(arg, "+"): // refspec +main
		return true
	case strings.HasPrefix(arg, "--"):
		return false
	case strings.HasPrefix(arg, "-"): // agrupadas: -f, -fu, -uf
		return strings.Contains(arg, "f")
	}
	return false
}
