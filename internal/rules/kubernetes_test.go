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

		// Apagar PVC/PV apaga os dados persistentes.
		{`kubectl delete pvc dados-0`, hook.Ask, hook.Deny},
		{`kubectl delete persistentvolumeclaim dados`, hook.Ask, hook.Deny},
		{`kubectl delete pv volume-1`, hook.Ask, hook.Deny},

		// Escalar para zero derruba o serviço.
		{`kubectl scale deploy/api --replicas=0`, hook.Ask, hook.Deny},
		{`kubectl scale deploy api --replicas 0`, hook.Ask, hook.Deny},

		// replace --force apaga e recria.
		{`kubectl replace --force -f deploy.yaml`, hook.Ask, hook.Deny},

		// Parecidos e inofensivos: allow em qualquer ambiente.
		{`kubectl scale deploy/api --replicas=3`, hook.Allow, hook.Allow},
		{`kubectl replace -f deploy.yaml`, hook.Allow, hook.Allow},
		{`kubectl get pvc`, hook.Allow, hook.Allow},
		{`kubectl delete configmap app-config`, hook.Allow, hook.Allow},
		{`kubectl delete -f app.yaml`, hook.Allow, hook.Allow}, // sem leitor: quem cuida é a regra kubectl-delete-file
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

func TestKubectlDeleteFile(t *testing.T) {
	files := map[string]string{
		"/work/ns.yaml":    "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: app\n",
		"/work/pvc.yaml":   "kind: PersistentVolumeClaim\nmetadata: {name: dados}\n",
		"/work/multi.yaml": "kind: ConfigMap\n---\nkind: PersistentVolume\n",
		"/work/cm.yaml":    "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: app\n",
	}
	dev := withFiles(devEnv, files)
	prod := withFiles(prodEnv, files)

	cases := []struct {
		command   string
		dev, prod hook.Decision
	}{
		{`kubectl delete -f ns.yaml`, hook.Ask, hook.Deny},
		{`kubectl delete -f pvc.yaml`, hook.Ask, hook.Deny},
		{`kubectl delete -f multi.yaml`, hook.Ask, hook.Deny}, // PV no 2º documento
		{`kubectl delete --filename=ns.yaml`, hook.Ask, hook.Deny},

		// ConfigMap não é perigoso.
		{`kubectl delete -f cm.yaml`, hook.Allow, hook.Allow},

		// Alvo ilegível: allow fora de produção, ask em produção.
		{`kubectl delete -f https://exemplo.com/ns.yaml`, hook.Allow, hook.Ask},
		{`kubectl delete -f -`, hook.Allow, hook.Ask},
		{`kubectl delete -k overlays/base`, hook.Allow, hook.Ask},
		{`kubectl delete -f nao-existe.yaml`, hook.Allow, hook.Ask},

		// Não é delete.
		{`kubectl apply -f ns.yaml`, hook.Allow, hook.Allow},
	}

	for _, c := range cases {
		t.Run(c.command, func(t *testing.T) {
			if got, r := checkRule(kubectlDeleteFile, c.command, dev); got != c.dev {
				t.Errorf("fora de produção: esperava %q, obtive %q (%q)", c.dev, got, r)
			}
			if got, r := checkRule(kubectlDeleteFile, c.command, prod); got != c.prod {
				t.Errorf("produção: esperava %q, obtive %q (%q)", c.prod, got, r)
			}
		})
	}
}
