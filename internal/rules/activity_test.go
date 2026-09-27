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

	want := Normalize(same[0])
	for _, command := range same[1:] {
		if got := Normalize(command); got != want {
			t.Errorf("%q: esperava %q, obtive %q", command, want, got)
		}
	}
}

func TestNormalizeDifferentCommands(t *testing.T) {
	different := []string{
		`terraform apply tfplan`,
		`terraform apply outro.tfplan`,
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
