package rules

import (
	"slices"
	"strings"

	"github.com/IronDeploy/IronBrake/internal/hook"
)

// Motivos e descrições nunca repetem o comando: ele pode ter segredos.
const (
	forcePushDenyReason = "Iron Brake: force push bloqueado. Ele reescreve o histórico remoto e pode apagar o trabalho de outras pessoas. Use um push normal; se realmente for necessário, peça ao usuário para executar."
	forcePushAskReason  = "Iron Brake: --force-with-lease ainda reescreve o histórico remoto. Confirme com o usuário antes de continuar."
	resetHardDanger     = "git reset --hard descarta alterações não commitadas sem volta (git stash guarda em vez de apagar)."
	cleanForceDanger    = "git clean -f apaga arquivos não rastreados pelo git sem volta (git clean -n mostra o que seria apagado)."
)

var (
	gitForcePush = commandRule{name: "git-force-push", check: checkGitPush}
	gitDiscard   = dangerRule{name: "git-discard", match: matchGitDiscard}
)

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

func matchGitDiscard(tokens []string) (string, bool) {
	sub, args := gitSubcommand(tokens)
	switch {
	case sub == "reset" && slices.Contains(args, "--hard"):
		return resetHardDanger, true
	case sub == "clean" && isCleanForced(args):
		return cleanForceDanger, true
	}
	return "", false
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
