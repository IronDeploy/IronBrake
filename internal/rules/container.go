package rules

import (
	"slices"
	"strings"
)

const (
	dockerPruneDanger       = "docker prune apaga em massa contêineres, imagens ou volumes parados: dados de volumes sem uso no momento (como um banco parado) somem sem volta."
	dockerVolumeRmDanger    = "remover um volume do Docker apaga os dados persistentes dentro dele."
	dockerComposeDownDanger = "docker compose down -v apaga os volumes nomeados e os dados dentro deles."
	dockerForceRmDanger     = "docker rm -f remove um contêiner em execução na hora: o serviço cai e o que ele estava processando ou guardando só no contêiner se perde."
)

var containerDelete = dangerRule{name: "container-delete", match: matchContainer}

// dockerValueFlags: opções globais do docker que levam valor.
var dockerValueFlags = set(
	"--context", "-c", "--host", "-H", "--config", "--log-level", "-l",
	"--tlscacert", "--tlscert", "--tlskey",
)

func matchContainer(tokens []string) (string, bool) {
	if len(tokens) == 0 {
		return "", false
	}
	switch programName(tokens[0]) {
	case "docker", "podman":
		return matchDockerVerb(tokens[1:])
	case "docker-compose":
		if isComposeDownVolumes(tokens[1:]) {
			return dockerComposeDownDanger, true
		}
	}
	return "", false
}

func matchDockerVerb(args []string) (string, bool) {
	pos := positionals(args, dockerValueFlags)
	if len(pos) == 0 {
		return "", false
	}

	// docker compose down -v
	if pos[0] == "compose" && isComposeDownVolumes(args) {
		return dockerComposeDownDanger, true
	}

	first := pos[0]
	second := ""
	if len(pos) > 1 {
		second = pos[1]
	}

	switch {
	case second == "prune": // system/image/container/volume/network prune
		return dockerPruneDanger, true
	case first == "volume" && (second == "rm" || second == "remove"):
		return dockerVolumeRmDanger, true
	case (first == "rm" || first == "container" && second == "rm") && hasForceFlag(args):
		return dockerForceRmDanger, true
	}
	return "", false
}

func isComposeDownVolumes(args []string) bool {
	pos := positionals(args, dockerValueFlags)
	if !slices.Contains(pos, "down") {
		return false
	}
	for _, a := range args {
		if a == "-v" || a == "--volumes" {
			return true
		}
		// agrupadas, ex.: down -v junto de outras curtas
		if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "v") {
			return true
		}
	}
	return false
}

func hasForceFlag(args []string) bool {
	for _, a := range args {
		switch {
		case a == "--force", a == "-f":
			return true
		case strings.HasPrefix(a, "--"):
		case strings.HasPrefix(a, "-") && strings.Contains(a, "f"):
			return true
		}
	}
	return false
}
