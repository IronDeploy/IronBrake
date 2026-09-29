package rules

import (
	"regexp"
	"slices"
	"strings"

	"github.com/IronDeploy/IronBrake/internal/hook"
)

const (
	deleteNamespaceDanger = "kubectl delete namespace apaga tudo o que está dentro do namespace."
	deleteAllDanger       = "kubectl delete --all apaga todos os recursos do tipo (apague pelo nome)."
	deleteAllNsDanger     = "kubectl delete -A (--all-namespaces) apaga em todos os namespaces do cluster, não só no atual."
	deleteDataDanger      = "kubectl delete de PVC/PV apaga o volume e os dados persistentes dentro dele."
	deleteCRDDanger       = "kubectl delete de CustomResourceDefinition apaga também todos os recursos criados a partir dela, em todos os namespaces."
	drainDanger           = "kubectl drain expulsa todos os pods do nó."
	scaleZeroDanger       = "kubectl scale --replicas=0 derruba todas as réplicas: o serviço fica fora do ar."
	replaceForceDanger    = "kubectl replace --force apaga e recria o recurso: perde estado e causa indisponibilidade."
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
		if slices.ContainsFunc(tokens, isAllNamespacesFlag) {
			return deleteAllNsDanger, true
		}
		if slices.ContainsFunc(args[verb+1:], isNamespaceKind) {
			return deleteNamespaceDanger, true
		}
		if slices.ContainsFunc(args[verb+1:], isDataKind) {
			return deleteDataDanger, true
		}
		if slices.ContainsFunc(args[verb+1:], isCRDKind) {
			return deleteCRDDanger, true
		}
	case "scale":
		if scalesToZero(tokens) {
			return scaleZeroDanger, true
		}
	case "replace":
		if slices.ContainsFunc(tokens, isForceOption) {
			return replaceForceDanger, true
		}
	}
	return "", false
}

// isDataKind reconhece volumes persistentes: apagá-los apaga os dados.
func isDataKind(kinds string) bool {
	for _, kind := range strings.Split(kinds, ",") {
		kind, _, _ = strings.Cut(kind, "/")
		switch strings.ToLower(kind) {
		case "pvc", "persistentvolumeclaim", "persistentvolumeclaims",
			"pv", "persistentvolume", "persistentvolumes":
			return true
		}
	}
	return false
}

// isCRDKind reconhece "crd", "crds", "customresourcedefinition(s)" e "crd/nome",
// também com grupo ("crd.apiextensions.k8s.io").
func isCRDKind(kinds string) bool {
	for _, kind := range strings.Split(kinds, ",") {
		kind, _, _ = strings.Cut(kind, "/")
		kind, _, _ = strings.Cut(strings.ToLower(kind), ".")
		switch kind {
		case "crd", "crds", "customresourcedefinition", "customresourcedefinitions":
			return true
		}
	}
	return false
}

// scalesToZero: --replicas=0 ou --replicas 0.
func scalesToZero(tokens []string) bool {
	for i, t := range tokens {
		switch {
		case t == "--replicas=0":
			return true
		case t == "--replicas" && i+1 < len(tokens) && tokens[i+1] == "0":
			return true
		}
	}
	return false
}

func isForceOption(arg string) bool {
	return arg == "--force" || arg == "--force=true"
}

// kubectlFileRule cobre kubectl delete -f arquivo.yaml: o tipo a apagar está
// dentro do arquivo, então precisa lê-lo (usa o Env, por isso não é dangerRule).
type kubectlFileRule struct{}

var kubectlDeleteFile Rule = kubectlFileRule{}

func (kubectlFileRule) Name() string { return "kubectl-delete-file" }

func (kubectlFileRule) Check(commands [][]string, env Env) (hook.Decision, string) {
	unreadable := false
	for _, tokens := range commands {
		targets, isDelete := kubectlDeleteFiles(tokens)
		if !isDelete {
			continue
		}
		for _, t := range targets {
			if isUnreadableTarget(t) {
				unreadable = true
				continue
			}
			data, ok := env.readTargetFile(t)
			if !ok {
				unreadable = true
				continue
			}
			if danger, found := dangerousManifestKind(data); found {
				return decideByEnvironment(danger, commands, env)
			}
		}
	}
	if unreadable {
		return decideUnreadableTarget("um manifesto de kubectl delete -f (remoto, stdin, kustomize ou ilegível)", commands, env)
	}
	return hook.Allow, ""
}

// kubectlDeleteFiles: se for kubectl delete, devolve os alvos de -f/--filename
// e -k/--kustomize.
func kubectlDeleteFiles(tokens []string) (targets []string, isDelete bool) {
	if len(tokens) == 0 || programName(tokens[0]) != "kubectl" {
		return nil, false
	}
	args := positionals(tokens[1:], kubectlValueFlags)
	if verb := slices.IndexFunc(args, func(a string) bool { return kubectlVerbs[a] }); verb < 0 || args[verb] != "delete" {
		return nil, false
	}
	for i := 1; i < len(tokens); i++ {
		switch t := tokens[i]; {
		case t == "-f", t == "--filename", t == "-k", t == "--kustomize":
			if i+1 < len(tokens) {
				targets = append(targets, tokens[i+1])
				i++
			}
		case strings.HasPrefix(t, "-f="):
			targets = append(targets, strings.TrimPrefix(t, "-f="))
		case strings.HasPrefix(t, "--filename="):
			targets = append(targets, strings.TrimPrefix(t, "--filename="))
		case strings.HasPrefix(t, "--kustomize="), strings.HasPrefix(t, "-k="):
			_, v, _ := strings.Cut(t, "=")
			targets = append(targets, v)
		}
	}
	return targets, true
}

// manifestKindPattern pega kind: X (YAML), "kind": "X" (JSON, inclusive em
// uma linha só) e kind: X dentro de "- " ou de uma lista (kind: List).
var manifestKindPattern = regexp.MustCompile(`(?i)(?:^|[\s{,\-])["']?kind["']?\s*:\s*["']?([A-Za-z]+)`)

// dangerousManifestKind procura kind: Namespace, PersistentVolumeClaim,
// PersistentVolume ou CustomResourceDefinition num manifesto YAML ou JSON (um
// ou vários documentos). Outros kinds (Deployment, Service...) ficam de fora,
// como no kubectl delete deploy NOME, que também é permitido.
func dangerousManifestKind(data []byte) (string, bool) {
	for _, m := range manifestKindPattern.FindAllSubmatch(data, -1) {
		switch strings.ToLower(string(m[1])) {
		case "namespace":
			return deleteNamespaceDanger, true
		case "persistentvolumeclaim", "persistentvolume":
			return deleteDataDanger, true
		case "customresourcedefinition":
			return deleteCRDDanger, true
		}
	}
	return "", false
}

// isUnreadableTarget: stdin, URL remota ou diretório do kustomize não dá para
// ler como um arquivo simples.
func isUnreadableTarget(target string) bool {
	return target == "" || target == "-" ||
		strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://")
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

// isAllNamespacesFlag: -A, --all-namespaces e --all-namespaces=true.
func isAllNamespacesFlag(arg string) bool {
	return arg == "-A" || arg == "--all-namespaces" || arg == "--all-namespaces=true"
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
