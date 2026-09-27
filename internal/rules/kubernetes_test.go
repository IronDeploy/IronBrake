package rules

import (
	"testing"

	"github.com/IronDeploy/IronBrake/internal/hook"
)

func TestKubectlDelete(t *testing.T) {
	runRuleCases(t, kubectlDelete, []ruleCase{
		// Apagar namespace apaga tudo o que está dentro dele: ask fora de produção, deny em produção.
		{`kubectl delete namespace prod`, hook.Deny, hook.Deny},
		{`kubectl delete ns prod`, hook.Deny, hook.Deny},
		{`kubectl delete namespaces app worker`, hook.Ask, hook.Deny},
		{`kubectl delete namespace/prod`, hook.Deny, hook.Deny},
		{`kubectl --context prod delete ns app`, hook.Deny, hook.Deny},
		{`kubectl delete -n default ns prod`, hook.Deny, hook.Deny},
		{`/usr/local/bin/kubectl delete ns prod`, hook.Deny, hook.Deny},

		// --all apaga todos os recursos do tipo: ask fora de produção, deny em produção.
		{`kubectl delete pods --all`, hook.Ask, hook.Deny},
		{`kubectl delete pods --all -n prod`, hook.Deny, hook.Deny},
		{`kubectl delete deploy,svc --all=true`, hook.Ask, hook.Deny},

		// drain expulsa todos os pods de um nó: ask fora de produção, deny em produção.
		{`kubectl drain node-1`, hook.Ask, hook.Deny},
		{`kubectl drain node-1 --ignore-daemonsets`, hook.Ask, hook.Deny},

		// Parecidos e inofensivos: allow em qualquer ambiente.
		{`kubectl get namespaces`, hook.Allow, hook.Allow},
		{`kubectl get ns`, hook.Allow, hook.Allow},
		{`kubectl describe namespace prod`, hook.Allow, hook.Allow},
		{`kubectl create namespace dev`, hook.Allow, hook.Allow},
		{`kubectl delete pod web-1`, hook.Allow, hook.Allow},
		{`kubectl delete pod web-1 -n prod`, hook.Allow, hook.Allow},
		{`kubectl -n ns delete pod web-1`, hook.Allow, hook.Allow}, // "ns" aqui é o nome do namespace
		{`kubectl delete pods -l app=web`, hook.Allow, hook.Allow},
		{`kubectl get pods --all-namespaces`, hook.Allow, hook.Allow},
		{`kubectl cordon node-1`, hook.Allow, hook.Allow},
		{`echo kubectl delete ns prod`, hook.Allow, hook.Allow},
	})
}
