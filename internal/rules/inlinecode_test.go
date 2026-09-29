package rules

import (
	"slices"
	"testing"

	"github.com/IronDeploy/IronBrake/internal/hook"
)

func TestSDKDelete(t *testing.T) {
	runRuleCases(t, sdkDelete, []ruleCase{
		// SDK de nuvem, HTTP e remoção recursiva escritos em outra linguagem.
		{`python3 -c "import boto3; boto3.client('s3').delete_bucket(Bucket='b')"`, hook.Ask, hook.Deny},
		{`python3 -c "import boto3; boto3.client('ec2').terminate_instances(InstanceIds=['i-1'])"`, hook.Ask, hook.Deny},
		{`python -c "import requests; requests.delete('https://api.example.com/db/1')"`, hook.Ask, hook.Deny},
		{`python3.12 -c "import shutil; shutil.rmtree('/data')"`, hook.Ask, hook.Deny},
		{`python3 -c "import boto3; boto3.resource('s3').Bucket('b').objects.all().delete()"`, hook.Ask, hook.Deny},
		{`node -e "fetch(u, {method: 'DELETE'})"`, hook.Ask, hook.Deny},
		{`node -e "require('fs').rmSync('/data', {recursive: true})"`, hook.Ask, hook.Deny},
		{`ruby -e "FileUtils.rm_rf('/data')"`, hook.Ask, hook.Deny},
		{`python3 -c "cur.execute('DROP TABLE users')"`, hook.Ask, hook.Deny},
		{`python3 -c "cur.execute(\"DELETE FROM users\")"`, hook.Ask, hook.Deny},
		{"python3 <<EOF\nimport boto3\nboto3.client('rds').delete_db_instance(DBInstanceIdentifier='x')\nEOF", hook.Ask, hook.Deny},

		// Inofensivos.
		{`python3 -c "print('hello')"`, hook.Allow, hook.Allow},
		{`python3 -c "import boto3; print(boto3.client('s3').list_buckets())"`, hook.Allow, hook.Allow},
		{`python3 -c "import pandas; df.drop_duplicates()"`, hook.Allow, hook.Allow},
		{`python3 -c "cur.execute('SELECT * FROM users WHERE id = 1')"`, hook.Allow, hook.Allow},
		{`python3 -c "cur.execute('DELETE FROM users WHERE id = 1')"`, hook.Allow, hook.Allow},
		{`python3 script.py`, hook.Allow, hook.Allow},
		// Sem interpretador na linha, é só texto.
		{`grep -r "delete_bucket(" src/`, hook.Allow, hook.Allow},
		{`git commit -m "usa client.delete_bucket() no teardown"`, hook.Allow, hook.Allow},
	})
}

// O que o código roda no shell é julgado pelas regras de sempre.
func TestInlineCodeShellOuts(t *testing.T) {
	cases := []struct {
		command string
		want    []string // comandos que precisam aparecer entre os analisados
	}{
		{`python3 -c "import os; os.system('git push --force')"`, []string{"git push --force"}},
		{`python3 -c "import subprocess; subprocess.run(['terraform', 'destroy', '-auto-approve'])"`, []string{"terraform destroy -auto-approve"}},
		{`python3 -c "import subprocess; subprocess.run('kubectl delete namespace app', shell=True)"`, []string{"kubectl delete namespace app"}},
		{`node -e "require('child_process').execSync('helm uninstall api')"`, []string{"helm uninstall api"}},
		{"ruby -e '`git reset --hard`'", []string{"git reset --hard"}},
	}
	for _, c := range cases {
		var got []string
		for _, tokens := range splitCommands(c.command) {
			got = append(got, joinTokens(tokens))
		}
		for _, want := range c.want {
			if !slices.Contains(got, want) {
				t.Errorf("%q: esperava analisar %q, obtive %q", c.command, want, got)
			}
		}
	}

	if got := splitCommands(`python3 -c "print('git push --force')"`); len(got) != 1 {
		t.Errorf("texto solto não roda nada: esperava só o python3, obtive %q", got)
	}
}

func TestInlineCodeThroughRegistry(t *testing.T) {
	for _, command := range []string{
		`python3 -c "import os; os.system('git push --force')"`,
		`python3 -c "import subprocess; subprocess.run(['git', 'push', '--force', 'origin', 'main'])"`,
		`node -e "require('child_process').execSync('terraform destroy')"`,
	} {
		if d, _ := CheckAll(command, devEnv); d == hook.Allow {
			t.Errorf("%q deveria pedir confirmação ou bloquear", command)
		}
		if d, _ := CheckAll(command, prodEnv); d != hook.Deny {
			t.Errorf("%q em produção: esperava deny, obtive %q", command, d)
		}
	}
	for _, command := range []string{
		`python3 -c "print('git push --force')"`,
		`python3 -c "import os; os.system('git status')"`,
	} {
		if d, why := CheckAll(command, devEnv); d != hook.Allow {
			t.Errorf("%q: esperava allow, obtive %q (%s)", command, d, why)
		}
	}
}

func joinTokens(tokens []string) string {
	out := ""
	for i, t := range tokens {
		if i > 0 {
			out += " "
		}
		out += t
	}
	return out
}
