package rules

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSplitCommands(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    [][]string
	}{
		{"simples", `git status`, [][]string{{"git", "status"}}},
		{"espaços extras", `  git   push   --force  `, [][]string{{"git", "push", "--force"}}},
		{"aspas duplas", `git push "--force"`, [][]string{{"git", "push", "--force"}}},
		{"aspas simples", `git push '-f'`, [][]string{{"git", "push", "-f"}}},
		{"aspas coladas", `git push --for"ce"`, [][]string{{"git", "push", "--force"}}},
		{"barra invertida", `git push --forc\e`, [][]string{{"git", "push", "--force"}}},
		{"espaço dentro de aspas", `git commit -m "fix: a b"`, [][]string{{"git", "commit", "-m", "fix: a b"}}},
		{"aspas sem fechar", `git push "--force`, [][]string{{"git", "push", "--force"}}},
		{"&&", `cd repo && git push -f`, [][]string{{"cd", "repo"}, {"git", "push", "-f"}}},
		{"; e |", `git status; git push -f | cat`, [][]string{{"git", "status"}, {"git", "push", "-f"}, {"cat"}}},
		{"quebra de linha", "git status\ngit push -f", [][]string{{"git", "status"}, {"git", "push", "-f"}}},
		{"operador dentro de aspas não separa", `echo "a && b"`, [][]string{{"echo", "a && b"}}},
		{"vazio", ``, nil},
		{"só espaços", `   `, nil},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := splitCommands(c.command)

			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("esperava %q, obtive %q", c.want, got)
			}
		})
	}
}

func TestIsSingleCommand(t *testing.T) {
	cases := []struct {
		command string
		want    bool
	}{
		{`terraform apply tfplan`, true},
		{`terraform apply "meu plano"`, true},
		{`echo "a && b"`, true}, // operador dentro de aspas não separa
		{`terraform apply tfplan && rm -rf ~/projeto`, false},
		{`terraform apply tfplan; echo fim`, false},
		{`terraform apply tfplan | tee log`, false},
		{"terraform apply tfplan\necho fim", false},
		{``, false},
	}

	for _, c := range cases {
		t.Run(c.command, func(t *testing.T) {
			if got := IsSingleCommand(c.command); got != c.want {
				t.Errorf("esperava %v, obtive %v", c.want, got)
			}
		})
	}
}

func TestSplitCommandsShellSemantics(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    [][]string
	}{
		{"continuação de linha", "kubectl delete \\\n  namespace prod", [][]string{{"kubectl", "delete", "namespace", "prod"}}},
		{"continuação no meio da palavra", "git push --for\\\nce", [][]string{{"git", "push", "--force"}}},
		{"continuação entre aspas duplas", "echo \"a\\\nb\"", [][]string{{"echo", "ab"}}},
		{"barra entre aspas duplas fica", `echo "a\qb"`, [][]string{{"echo", `a\qb`}}},
		{"aspas ANSI-C", `git push $'--force'`, [][]string{{"git", "push", "--force"}}},
		{"ANSI-C com hexa", `git push $'\x2d\x2dforce'`, [][]string{{"git", "push", "--force"}}},
		{"ANSI-C com octal e \\n", `echo $'a\nb\101'`, [][]string{{"echo", "a\nbA"}}},
		{"aspas $\"...\"", `echo $"x y"`, [][]string{{"echo", "x y"}}},

		{"for ... do", "for ns in a b; do kubectl delete namespace $ns; done", [][]string{{"for", "ns", "in", "a", "b"}, {"kubectl", "delete", "namespace", "$ns"}, {"done"}}},
		{"if ... then", "if true; then terraform destroy; fi", [][]string{{"true"}, {"terraform", "destroy"}, {"fi"}}},
		{"subshell", "(cd infra && terraform destroy)", [][]string{{"cd", "infra"}, {"terraform", "destroy"}}},
		{"grupo", "{ git push --force; }", [][]string{{"git", "push", "--force"}}},
		{"negação", "! git push --force", [][]string{{"git", "push", "--force"}}},

		{"time", "time terraform destroy", [][]string{{"terraform", "destroy"}}},
		{"atribuição na frente", "TF_LOG=1 A=b terraform destroy", [][]string{{"terraform", "destroy"}}},
		{"env", "env -i TF_LOG=1 terraform destroy", [][]string{{"terraform", "destroy"}}},
		{"sudo com usuário", "sudo -u root kubectl delete ns x", [][]string{{"kubectl", "delete", "ns", "x"}}},
		{"nohup e timeout", "nohup timeout 60 terraform destroy", [][]string{{"terraform", "destroy"}}},
		{"nice", "nice -n 10 git push -f", [][]string{{"git", "push", "-f"}}},
		{"xargs", "echo a | xargs -n 1 kubectl delete ns", [][]string{{"echo", "a"}, {"kubectl", "delete", "ns"}}},
		{"command e exec", "command git push -f; exec git push -f", [][]string{{"git", "push", "-f"}, {"git", "push", "-f"}}},

		{"bash -c", `bash -c "git push --force"`, [][]string{{"git", "push", "--force"}}},
		{"sh -lc com dois comandos", `sh -lc 'cd x && terraform destroy'`, [][]string{{"cd", "x"}, {"terraform", "destroy"}}},
		{"eval", `eval "git push --force"`, [][]string{{"git", "push", "--force"}}},
		{"watch", "watch -n 5 kubectl get pods", [][]string{{"kubectl", "get", "pods"}}},

		{"$(...) entre aspas duplas", `echo "$(git push --force)"`, [][]string{{"git", "push", "--force"}, {"echo", "$(git push --force)"}}},
		{"$(...) no meio do argumento", "kubectl --context=$(cat ctx) delete ns prod", [][]string{{"cat", "ctx"}, {"kubectl", "--context=$(cat ctx)", "delete", "ns", "prod"}}},
		{"crases", "echo `git push -f`", [][]string{{"git", "push", "-f"}, {"echo", "`git push -f`"}}},
		{"aspas simples não executam", `echo '$(git push --force)'`, [][]string{{"echo", "$(git push --force)"}}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := splitCommands(c.command); !reflect.DeepEqual(got, c.want) {
				t.Errorf("esperava %q, obtive %q", c.want, got)
			}
		})
	}
}

func TestSplitCommandsNestingIsBounded(t *testing.T) {
	commands := []string{
		strings.Repeat("eval ", 500) + "git push --force",
		strings.Repeat("$(", 5000) + "git push --force",
		strings.Repeat("bash -c ", 500) + "'git push --force'",
	}

	for _, command := range commands {
		done := make(chan struct{})
		go func() {
			splitCommands(command)
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("splitCommands não terminou para %.40q...", command)
		}
	}
}
