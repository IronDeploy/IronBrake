package main

import (
	"bytes"
	"strings"
	"testing"
)

func BenchmarkRunHook(b *testing.B) {
	cases := []struct {
		name    string
		command string
	}{
		{"git_status_allow", "git status"},
		{"git_push_force_deny", "git push --force origin main"},
		{"kubectl_delete_ns_prod_deny", "kubectl delete namespace prod"},
		{"kubectl_get_pods_allow", "kubectl get pods"},
		{"terraform_apply_sem_plano_deny", "terraform apply -auto-approve"},
		{"terraform_apply_plano_falso_allow", "terraform apply create.tfplan"},
	}

	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			event := `{"session_id":"bench","tool_name":"Bash","cwd":"/srv/loja","tool_input":{"command":"` + c.command + `"}}`
			deps := testDeps()

			for b.Loop() {
				var stdout, stderr bytes.Buffer
				runHook(strings.NewReader(event), &stdout, &stderr, deps)
			}
		})
	}
}

func BenchmarkLoadEnv(b *testing.B) {
	for b.Loop() {
		loadEnv("/srv/loja")
	}
}
