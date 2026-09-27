package rules

import (
	"slices"
	"strings"
)

const (
	deleteNamespaceDanger = "kubectl delete namespace apaga tudo o que está dentro do namespace."
	deleteAllDanger       = "kubectl delete --all apaga todos os recursos do tipo (apague pelo nome)."
	drainDanger           = "kubectl drain expulsa todos os pods do nó."
)

var kubectlDelete = dangerRule{name: "kubectl-delete", match: matchKubectl}

var kubectlValueFlags = set(
	"-n", "--namespace", "--context", "--kubeconfig", "--cluster", "--user", "-s", "--server",
	"--token", "--as", "--as-group", "--as-uid", "--username", "--password", "--cache-dir",
	"--certificate-authority", "--client-certificate", "--client-key", "--tls-server-name",
	"--request-timeout", "--profile", "--profile-output", "-v", "--v", "--vmodule", "--log-file",
	"-l", "--selector", "--field-selector", "-o", "--output", "-f", "--filename", "-k", "--kustomize",
	"--grace-period", "--timeout", "--cascade", "--raw",
)

func matchKubectl(tokens []string) (string, bool) {
	if len(tokens) == 0 || programName(tokens[0]) != "kubectl" {
		return "", false
	}

	// Procura o verbo em vez de confiar na posição: uma opção com valor fora
	// da lista (--request-timeout 5s) deslocaria os argumentos.
	args := positionals(tokens[1:], kubectlValueFlags)
	verb := slices.IndexFunc(args, func(a string) bool { return kubectlVerbs[a] })
	if verb < 0 {
		return "", false
	}

	switch args[verb] {
	case "drain":
		return drainDanger, true
	case "delete":
		if slices.ContainsFunc(tokens, isAllFlag) {
			return deleteAllDanger, true
		}
		if slices.ContainsFunc(args[verb+1:], isNamespaceKind) {
			return deleteNamespaceDanger, true
		}
	}
	return "", false
}

var kubectlVerbs = set(
	"get", "describe", "delete", "drain", "cordon", "uncordon", "taint", "apply", "create", "edit",
	"patch", "replace", "scale", "autoscale", "rollout", "logs", "exec", "run", "expose", "label",
	"annotate", "top", "port-forward", "proxy", "cp", "auth", "config", "explain", "api-resources",
	"api-versions", "version", "wait", "debug", "events", "diff", "kustomize", "set", "attach",
	"certificate", "cluster-info", "plugin", "completion", "alpha",
)

// isAllFlag: --all e --all=true, não --all-namespaces.
func isAllFlag(arg string) bool {
	return arg == "--all" || arg == "--all=true"
}

// isNamespaceKind aceita "ns", "namespaces", "namespace/prod" e "ns,pods".
func isNamespaceKind(kinds string) bool {
	for _, kind := range strings.Split(kinds, ",") {
		kind, _, _ = strings.Cut(kind, "/")
		switch strings.ToLower(kind) {
		case "namespace", "namespaces", "ns":
			return true
		}
	}
	return false
}
