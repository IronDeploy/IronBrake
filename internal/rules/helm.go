package rules

const (
	helmUninstallDanger = "helm uninstall remove do cluster todos os recursos do release."
	helmRollbackDanger  = "helm rollback volta o release para outra revisão e pode derrubar o que está no ar."
)

var helmDestructive = dangerRule{name: "helm-destructive", match: matchHelm}

var helmValueFlags = set(
	"-n", "--namespace", "--kube-context", "--kubeconfig", "--kube-apiserver",
	"--kube-token", "--kube-as-user", "--kube-as-group", "--kube-ca-file",
	"--registry-config", "--repository-cache", "--repository-config", "-o", "--output",
	"--timeout", "--description",
)

func matchHelm(tokens []string) (string, bool) {
	if len(tokens) == 0 || programName(tokens[0]) != "helm" {
		return "", false
	}
	pos := positionals(tokens[1:], helmValueFlags)
	if len(pos) == 0 {
		return "", false
	}
	switch pos[0] {
	case "uninstall", "delete", "del", "un": // aliases do helm uninstall
		return helmUninstallDanger, true
	case "rollback":
		return helmRollbackDanger, true
	}
	return "", false
}
