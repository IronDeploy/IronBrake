package rules

import (
	"slices"
	"strings"

	"github.com/IronDeploy/IronBrake/internal/hook"
)

const deleteWorkloadDanger = "Iron Brake: confirme antes de executar: kubectl delete de Deployment, StatefulSet, DaemonSet, Service ou Ingress em produção tira o serviço do ar até alguém recriá-lo."

// kubectlWorkloadRule: apagar um workload ou a rede na frente dele não perde
// dados (um apply recria), mas em produção derruba o serviço. Por isso só
// pergunta em produção, e nunca bloqueia; fora dela passa, como o dia a dia
// de um deploy. Cobre kubectl delete deploy NOME e kubectl delete -f com
// kind: Deployment. Pod fica de fora: apagar um pod é como se reinicia.
type kubectlWorkloadRule struct{}

var kubectlDeleteWorkload Rule = kubectlWorkloadRule{}

func (kubectlWorkloadRule) Name() string { return "kubectl-delete-workload" }

func (kubectlWorkloadRule) Check(commands [][]string, env Env) (hook.Decision, string) {
	if !env.isProduction(commands) {
		return hook.Allow, ""
	}
	for _, tokens := range commands {
		if deletesWorkloadByName(tokens) {
			return hook.Ask, deleteWorkloadDanger
		}
		targets, isDelete := kubectlDeleteFiles(tokens)
		if !isDelete {
			continue
		}
		for _, t := range targets {
			if isUnreadableTarget(t) {
				continue // a regra de kubectl delete -f já pergunta em produção
			}
			if data, ok := env.readTargetFile(t); ok && hasWorkloadManifestKind(data) {
				return hook.Ask, deleteWorkloadDanger
			}
		}
	}
	return hook.Allow, ""
}

// deletesWorkloadByName: kubectl delete TIPO NOME ou TIPO/NOME. Só o primeiro
// argumento vale como tipo (um pod chamado "svc" não conta); os outros só na
// forma TIPO/NOME.
func deletesWorkloadByName(tokens []string) bool {
	if len(tokens) == 0 || programName(tokens[0]) != "kubectl" {
		return false
	}
	args := positionals(tokens[1:], kubectlValueFlags)
	verb := slices.IndexFunc(args, func(a string) bool { return kubectlVerbs[a] })
	if verb < 0 || args[verb] != "delete" {
		return false
	}
	for i, arg := range args[verb+1:] {
		if i > 0 && !strings.Contains(arg, "/") {
			continue
		}
		if isWorkloadKind(arg) {
			return true
		}
	}
	return false
}

// isWorkloadKind aceita "deploy", "deployments", "deploy/api", "sts,svc" e
// nomes com grupo ("deployments.apps").
func isWorkloadKind(kinds string) bool {
	for _, kind := range strings.Split(kinds, ",") {
		kind, _, _ = strings.Cut(kind, "/")
		kind, _, _ = strings.Cut(strings.ToLower(kind), ".")
		switch kind {
		case "deployment", "deployments", "deploy",
			"statefulset", "statefulsets", "sts",
			"daemonset", "daemonsets", "ds",
			"service", "services", "svc",
			"ingress", "ingresses", "ing":
			return true
		}
	}
	return false
}

func hasWorkloadManifestKind(data []byte) bool {
	for _, m := range manifestKindPattern.FindAllSubmatch(data, -1) {
		switch strings.ToLower(string(m[1])) {
		case "deployment", "statefulset", "daemonset", "service", "ingress":
			return true
		}
	}
	return false
}
