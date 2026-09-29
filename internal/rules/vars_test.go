package rules

import (
	"slices"
	"testing"

	"github.com/IronDeploy/IronBrake/internal/hook"
)

func TestVariablesAssignedOnTheSameLine(t *testing.T) {
	cases := []struct {
		command string
		want    string // um dos comandos analisados
	}{
		{`F=--force; git push $F`, "git push --force"},
		{`F=--force && git push origin main $F`, "git push origin main --force"},
		{`export F=--force; git push ${F}`, "git push --force"},
		{`A="--force --no-verify"; git push $A`, "git push --force --no-verify"},
		{`NS=prod; kubectl delete namespace $NS`, "kubectl delete namespace prod"},
		{`C=git; $C push --force`, "git push --force"},
		{`ARGS=push; git $ARGS --force`, "git push --force"},
	}
	for _, c := range cases {
		var got []string
		for _, tokens := range splitCommands(c.command) {
			got = append(got, joinTokens(tokens))
		}
		if !slices.Contains(got, c.want) {
			t.Errorf("%q: esperava analisar %q, obtive %q", c.command, c.want, got)
		}
	}

	// Não resolve o que o shell só sabe na hora, nem atribuição na frente de comando.
	for _, command := range []string{`F=$(echo --force); git push $F`, `F=--force git push $F`} {
		for _, tokens := range splitCommands(command) {
			if joinTokens(tokens) == "git push --force" {
				t.Errorf("%q: não devia resolver $F", command)
			}
		}
	}
}

func TestVariableForcePushIsBlocked(t *testing.T) {
	for _, command := range []string{`F=--force; git push $F`, `X=push; git $X --force origin main`, `C=git; $C push -f`} {
		if d, _ := CheckAll(command, devEnv); d == hook.Allow {
			t.Errorf("%q deveria pedir confirmação ou bloquear", command)
		}
	}
}

func TestUnresolvedCommand(t *testing.T) {
	runRuleCases(t, unresolvedCommand, []ruleCase{
		{`$RM -rf build`, hook.Ask, hook.Ask},
		{`${CMD} apply`, hook.Ask, hook.Ask},
		{`$(which terraform) destroy`, hook.Ask, hook.Ask},
		{`X=1; $TOOL run`, hook.Ask, hook.Ask},

		{`$HOME/.local/bin/iron version`, hook.Allow, hook.Allow},
		{`${HOME}/bin/tool`, hook.Allow, hook.Allow},
		{`./scripts/build.sh`, hook.Allow, hook.Allow},
		{`echo $HOME`, hook.Allow, hook.Allow},
		{`ls $DIR`, hook.Allow, hook.Allow},
		{`git push origin $BRANCH`, hook.Allow, hook.Allow},
		{`CMD=ls; $CMD`, hook.Allow, hook.Allow}, // resolvido na mesma linha
	})
}
