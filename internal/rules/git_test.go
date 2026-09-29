package rules

import (
	"strings"
	"testing"

	"github.com/IronDeploy/IronBrake/internal/hook"
)

func TestGitForcePush(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    hook.Decision
	}{
		// Force push explícito: deny.
		{"--force", `git push --force origin main`, hook.Deny},
		{"-f", `git push -f`, hook.Deny},
		{"flag no fim", `git push origin main --force`, hook.Deny},
		{"flag curta agrupada", `git push -fu origin feature`, hook.Deny},
		{"refspec com +", `git push origin +main`, hook.Deny},

		// Variações de escrita: espaços, aspas, barra invertida, caminho.
		{"espaços extras", `git   push    --force   origin   main`, hook.Deny},
		{"aspas duplas", `git push "--force" origin main`, hook.Deny},
		{"aspas simples", `git push '-f'`, hook.Deny},
		{"tudo entre aspas", `"git" 'push' "-f"`, hook.Deny},
		{"aspas coladas", `git push --for"ce"`, hook.Deny},
		{"barra invertida", `git push --forc\e`, hook.Deny},
		{"opção global antes do push", `git -C ../repo push --force`, hook.Deny},
		{"caminho completo do git", `/usr/bin/git push -f`, hook.Deny},

		// Comandos encadeados.
		{"depois de &&", `cd repo && git push -f`, hook.Deny},
		{"depois de ;", `git status; git push --force`, hook.Deny},
		{"lease e force juntos", `git push --force-with-lease --force`, hook.Deny},
		{"ask e depois deny", `git push --force-with-lease && git push -f`, hook.Deny},

		// --force-with-lease: ask (recomendação explicada na conversa).
		{"--force-with-lease", `git push --force-with-lease`, hook.Ask},
		{"--force-with-lease com valor", `git push --force-with-lease=main:abc123 origin main`, hook.Ask},
		{"--force-if-includes", `git push --force-with-lease --force-if-includes origin main`, hook.Ask},

		// Push normal e outros comandos: allow.
		{"push normal", `git push origin feature`, hook.Allow},
		{"push sem argumentos", `git push`, hook.Allow},
		{"push com -u", `git push -u origin feature`, hook.Allow},
		{"branch com f no nome", `git push origin fix-force-bug`, hook.Allow},
		{"--follow-tags não é force", `git push --follow-tags`, hook.Allow},
		{"git status", `git status`, hook.Allow},
		{"texto entre aspas não é flag", `git commit -m "docs: git push --force"`, hook.Allow},
		{"echo não é git", `echo git push --force`, hook.Allow},
		{"vazio", ``, hook.Allow},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, reason := checkRule(gitForcePush, c.command, devEnv)

			if got != c.want {
				t.Errorf("decisão: esperava %q, obtive %q", c.want, got)
			}
			if got != hook.Allow && reason == "" {
				t.Error("deny e ask precisam de um motivo")
			}
			if got == hook.Allow && reason != "" {
				t.Errorf("allow não deve ter motivo, obtive %q", reason)
			}
		})
	}
}

func TestGitPushDelete(t *testing.T) {
	runRuleCases(t, gitPushDelete, []ruleCase{
		// Branch protegida: deny em qualquer ambiente.
		{`git push origin :main`, hook.Deny, hook.Deny},
		{`git push origin :refs/heads/master`, hook.Deny, hook.Deny},
		{`git push --delete origin main`, hook.Deny, hook.Deny},
		{`git push -d origin production`, hook.Deny, hook.Deny},
		{`git push origin --delete develop`, hook.Deny, hook.Deny},

		// Outra branch: ask (apagar a remota errada também custa).
		{`git push origin :feature-x`, hook.Ask, hook.Ask},
		{`git push --delete origin old-feature`, hook.Ask, hook.Ask},

		// Push normal e afins: allow.
		{`git push origin main`, hook.Allow, hook.Allow},
		{`git push origin feature`, hook.Allow, hook.Allow},
		{`git push -u origin feature`, hook.Allow, hook.Allow},
		{`git status`, hook.Allow, hook.Allow},
		{`echo git push origin :main`, hook.Allow, hook.Allow},
	})
}

func TestGitForcePushReasonDoesNotLeakCommand(t *testing.T) {
	commands := []string{
		`git push https://user:s3cr3t-token@github.com/org/repo.git --force`,
		`git push https://user:s3cr3t-token@github.com/org/repo.git --force-with-lease`,
	}

	for _, command := range commands {
		_, reason := checkRule(gitForcePush, command, devEnv)

		if strings.Contains(reason, "s3cr3t-token") {
			t.Errorf("o motivo vazou o segredo: %q", reason)
		}
	}
}

func TestGitDiscard(t *testing.T) {
	runRuleCases(t, gitDiscard, []ruleCase{
		// reset --hard descarta alterações não commitadas: ask fora de produção, deny em produção.
		{`git reset --hard`, hook.Ask, hook.Deny},
		{`git reset --hard HEAD~3`, hook.Ask, hook.Deny},
		{`git -C ../repo reset --hard origin/main`, hook.Ask, hook.Deny},
		{`cd repo && git reset --hard`, hook.Ask, hook.Deny},

		// clean com force apaga arquivos não rastreados: ask fora de produção, deny em produção.
		{`git clean -fd`, hook.Ask, hook.Deny},
		{`git clean -df`, hook.Ask, hook.Deny},
		{`git clean -fdx`, hook.Ask, hook.Deny},
		{`git clean -f -d`, hook.Ask, hook.Deny},
		{`git clean --force -d`, hook.Ask, hook.Deny},
		{`git clean -f`, hook.Ask, hook.Deny},

		// Apagar branch/tag sem merge, e destruir a rede de recuperação.
		{`git branch -D feature`, hook.Ask, hook.Deny},
		{`git branch --delete --force feature`, hook.Ask, hook.Deny},
		{`git branch -Df feature`, hook.Ask, hook.Deny},
		{`git tag -d v1.0.0`, hook.Ask, hook.Deny},
		{`git tag --delete v1.0.0`, hook.Ask, hook.Deny},
		{`git stash drop`, hook.Ask, hook.Deny},
		{`git stash clear`, hook.Ask, hook.Deny},
		{`git reflog expire --expire=now --all`, hook.Ask, hook.Deny},
		{`git gc --prune=now`, hook.Ask, hook.Deny},
		{`git gc --prune=all`, hook.Ask, hook.Deny},
		{`git filter-branch --tree-filter 'rm -f senha' HEAD`, hook.Ask, hook.Deny},
		{`git filter-repo --path secrets --invert-paths`, hook.Ask, hook.Deny},
		{`git update-ref -d refs/heads/feature`, hook.Ask, hook.Deny},

		// restore/checkout que descartam o diretório de trabalho.
		{`git restore .`, hook.Ask, hook.Deny},
		{`git restore src/app.go`, hook.Ask, hook.Deny},
		{`git restore --staged --worktree arquivo`, hook.Ask, hook.Deny},
		{`git checkout .`, hook.Ask, hook.Deny},
		{`git checkout -- src/app.go`, hook.Ask, hook.Deny},

		// Parecidos e inofensivos: allow em qualquer ambiente.
		{`git reset --soft HEAD~1`, hook.Allow, hook.Allow},
		{`git reset HEAD arquivo.txt`, hook.Allow, hook.Allow},
		{`git reset --mixed`, hook.Allow, hook.Allow},
		{`git clean -n`, hook.Allow, hook.Allow},
		{`git clean -nd`, hook.Allow, hook.Allow},
		{`git clean -fdn`, hook.Allow, hook.Allow},        // -n (dry run) só mostra, não apaga
		{`git branch -d feature`, hook.Allow, hook.Allow}, // -d sem force recusa branch não mergeada
		{`git branch feature`, hook.Allow, hook.Allow},
		{`git branch -a`, hook.Allow, hook.Allow},
		{`git tag v1.0.0`, hook.Allow, hook.Allow},
		{`git stash`, hook.Allow, hook.Allow},
		{`git stash pop`, hook.Allow, hook.Allow},
		{`git stash list`, hook.Allow, hook.Allow},
		{`git gc`, hook.Allow, hook.Allow},
		{`git gc --prune=never`, hook.Allow, hook.Allow},
		{`git reflog`, hook.Allow, hook.Allow},
		{`git restore --staged arquivo`, hook.Allow, hook.Allow}, // só tira do stage
		{`git checkout main`, hook.Allow, hook.Allow},
		{`git checkout -b nova-feature`, hook.Allow, hook.Allow},
		{`git status`, hook.Allow, hook.Allow},
		{`git commit -m "git reset --hard"`, hook.Allow, hook.Allow},
		{`echo git clean -fd`, hook.Allow, hook.Allow},
	})
}

func TestGitPushVariable(t *testing.T) {
	runRuleCases(t, gitPushVar, []ruleCase{
		// Variável que a linha não define e que pode esconder --force.
		{`git push $F`, hook.Ask, hook.Ask},
		{`git push origin main $FLAGS`, hook.Ask, hook.Ask},
		{`git push ${OPTS} origin main`, hook.Ask, hook.Ask},
		{`git push origin +$BRANCH`, hook.Ask, hook.Ask},
		{`git push origin "$(echo --force)"`, hook.Ask, hook.Ask},
		{`git push origin $(cat flags.txt)`, hook.Ask, hook.Ask},

		// Uso normal: branch, remoto, tag e comando git de leitura.
		{`git push origin $BRANCH`, hook.Allow, hook.Allow},
		{`git push $REMOTE $BRANCH`, hook.Allow, hook.Allow},
		{`git push origin "$CURRENT_BRANCH"`, hook.Allow, hook.Allow},
		{`git push origin refs/heads/$BRANCH`, hook.Allow, hook.Allow},
		{`git push origin $TAG_NAME`, hook.Allow, hook.Allow},
		{`git push -u origin $(git branch --show-current)`, hook.Allow, hook.Allow},
		{`git push origin HEAD`, hook.Allow, hook.Allow},
		{`git pull origin $F`, hook.Allow, hook.Allow},
		{`git push origin main`, hook.Allow, hook.Allow},
		// Variável definida na mesma linha é resolvida (e o force push, barrado à parte).
		{`F=-u; git push $F origin main`, hook.Allow, hook.Allow},
	})
}
