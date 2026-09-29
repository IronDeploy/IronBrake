package rules

import (
	"strings"

	"github.com/IronDeploy/IronBrake/internal/hook"
)

const (
	unresolvedCommandReason = "Iron Brake: confirme antes de executar: o programa a rodar é uma variável ou vem de um comando ($CMD, $(...)), e o Iron Brake não consegue ver o que vai executar."
	rmUnknownPathReason     = "Iron Brake: confirme antes de executar: rm recursivo num caminho com variável ($DIR/x). Se a variável estiver vazia, o caminho vira outro (rm -rf $DIR/ apaga a raiz)."
)

// unresolvedRule: um comando cujo programa é uma variável ($RM -rf /) ou
// sai de outro comando não tem como ser julgado. Sempre é ask (não dá para
// saber se é perigoso), e só vale para o que o agente digitou: dentro de
// scripts lidos de arquivo, variáveis são o normal.
type unresolvedRule struct{}

var unresolvedCommand Rule = unresolvedRule{}

func (unresolvedRule) Name() string { return "unresolved-command" }

func (unresolvedRule) Check(commands [][]string, env Env) (hook.Decision, string) {
	if env.inScript {
		return hook.Allow, ""
	}
	for _, tokens := range commands {
		if len(tokens) > 0 && hasUnresolvedVar(stripKnownRoot(tokens[0])) {
			return hook.Ask, unresolvedCommandReason
		}
	}
	return hook.Allow, ""
}

// stripKnownRoot tira o começo que se resolve sozinho ($HOME/bin/x, ~/bin/x).
func stripKnownRoot(token string) string {
	for _, prefix := range []string{"${HOME}", "$HOME", "${PWD}", "$PWD"} {
		if rest, ok := strings.CutPrefix(token, prefix); ok && (rest == "" || rest[0] == '/') {
			return rest
		}
	}
	return token
}
