package rules

import "testing"

func TestNormalizeSameCommand(t *testing.T) {
	same := []string{
		`terraform apply tfplan`,
		`terraform   apply    tfplan`,
		`terraform apply "tfplan"`,
		`TERRAFORM apply tfplan`,
		`  terraform apply 'tfplan'  `,
	}

	// Só o número muda: o agente em loop refaz o plano com outro nome.
	sameNumbers := []string{
		`terraform apply plan1`,
		`terraform apply plan2`,
		`terraform apply plan10`,
		`terraform apply "PLAN1"`,
	}
	for _, command := range []string{`terraform apply plan-20260929-1030`, `terraform apply plan-1-2`, `terraform apply plan-a`} {
		if got, want := Normalize(command), "terraform apply <plan>"; got != want {
			t.Errorf("%q: esperava %q, obtive %q", command, want, got)
		}
	}
	wantNumbers := Normalize(sameNumbers[0])
	for _, command := range sameNumbers[1:] {
		if got := Normalize(command); got != wantNumbers {
			t.Errorf("%q: esperava %q, obtive %q", command, wantNumbers, got)
		}
	}

	want := Normalize(same[0])
	for _, command := range same[1:] {
		if got := Normalize(command); got != want {
			t.Errorf("%q: esperava %q, obtive %q", command, want, got)
		}
	}
}

// O agente em loop troca o nome do plano (mesmo sem número) ou a ordem das opções.
func TestNormalizeNamesAndOptionOrder(t *testing.T) {
	groups := [][]string{
		{`terraform apply plan-a`, `terraform apply plan-b`, `terraform apply outro.tfplan`, `terraform apply /tmp/x/novo.tfplan`},
		{`terraform apply -auto-approve -input=false tfplan`, `terraform apply -input=false -auto-approve tfplan`, `terraform apply -input=false tfplan -auto-approve`},
		{`terraform plan -out=a.tfplan`, `terraform plan -out=b.tfplan`},
		{`terraform -chdir=infra apply plan-x`, `terraform -chdir=infra apply plan-y`},
		{`kubectl delete -f a.yaml`, `kubectl delete -f b.yaml`},
		{`kubectl delete pod web --force --grace-period=0`, `kubectl delete pod web --grace-period=0 --force`},
	}
	for _, group := range groups {
		want := Normalize(group[0])
		for _, command := range group[1:] {
			if got := Normalize(command); got != want {
				t.Errorf("%q: esperava %q, obtive %q", command, want, got)
			}
		}
	}
}

func TestNormalizeKeepsRealDifferences(t *testing.T) {
	different := []string{
		`terraform apply tfplan`,
		`terraform apply -target=aws_instance.web tfplan`,
		`terraform apply -target=aws_instance.db tfplan`,
		`terraform -chdir=infra apply tfplan`,
		`terraform -chdir=other apply tfplan`,
		`terraform destroy`,
		`terraform plan tfplan`,
		`kubectl delete pod web`,
		`kubectl delete pod db`,
		`kubectl delete pod web -n prod`,
		`kubectl delete pod web -n dev`,
	}
	seen := map[string]string{}
	for _, command := range different {
		n := Normalize(command)
		if other, ok := seen[n]; ok {
			t.Errorf("%q e %q não deveriam normalizar igual (%q)", command, other, n)
		}
		seen[n] = command
	}
}

func TestNormalizeDifferentCommands(t *testing.T) {
	different := []string{
		`terraform apply tfplan`,
		`terraform apply -target=aws_instance.web tfplan`,
		`terraform destroy`,
		`terraform apply plan1 && terraform apply plan2`,
		`terraform plan -out=tfplan`,
		`terraform apply tfplan && kubectl get pods`,
	}

	seen := map[string]string{}
	for _, command := range different {
		n := Normalize(command)
		if other, ok := seen[n]; ok {
			t.Errorf("%q e %q não deveriam normalizar igual (%q)", command, other, n)
		}
		seen[n] = command
	}
}

func TestTouchesInfra(t *testing.T) {
	cases := []struct {
		command string
		want    bool
	}{
		{`terraform apply tfplan`, true},
		{`kubectl get pods`, true},
		{`helm upgrade app ./chart`, true},
		{`aws s3 ls`, true},
		{`az group list`, true},
		{`gcloud compute instances list`, true},
		{`psql -c "select 1"`, true},
		{`docker exec db psql -c "select 1"`, true},
		{`git push origin main`, true},
		{`cd infra && terraform plan`, true},
		{`/opt/homebrew/bin/terraform show`, true},

		// Ciclo normal de trabalho: não conta repetição.
		{`go test ./...`, false},
		{`git status`, false},
		{`git commit -m "terraform apply"`, false},
		{`npm test`, false},
		{`echo kubectl delete ns prod`, false},
		{``, false},
	}

	for _, c := range cases {
		t.Run(c.command, func(t *testing.T) {
			if got := TouchesInfra(c.command); got != c.want {
				t.Errorf("esperava %v, obtive %v", c.want, got)
			}
		})
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		command string
		want    string
	}{
		{`terraform apply tfplan`, "terraform apply"},
		{`terraform -chdir=infra destroy -auto-approve`, "terraform destroy"},
		{`terraform s3cr3t-subcomando`, "terraform"}, // subcomando fora da lista: só o programa
		{`git push https://u:s3cr3t@github.com/o/r.git --force`, "git push"},
		{`git -C repo reset --hard`, "git reset"},
		{`kubectl --token s3cr3t delete ns prod`, "kubectl delete"},
		{`helm uninstall app`, "helm uninstall"},
		{`aws --profile s3cr3t ec2 terminate-instances`, "aws"},
		{`psql "postgres://u:s3cr3t@h/db" -c "DROP TABLE x"`, "sql"},
		{`docker exec db psql -c "select 1"`, "sql"},
		{`cd infra && terraform apply tfplan`, "outro, terraform apply"},
		{`git status; git status`, "git status"},
		{`./deploy-s3cr3t.sh`, "outro"},
		{`export TOKEN=s3cr3t`, "outro"},
		{``, ""},
	}

	for _, c := range cases {
		t.Run(c.command, func(t *testing.T) {
			if got := Classify(c.command); got != c.want {
				t.Errorf("esperava %q, obtive %q", c.want, got)
			}
		})
	}
}
