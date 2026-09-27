package rules

import (
	"slices"
	"strings"
)

const cloudDeleteDanger = "o comando de nuvem apaga ou encerra recursos."

var cloudDelete = dangerRule{name: "cloud-delete", match: matchCloudDelete}

var (
	// No aws, toda opção longa recebe um valor, menos estas.
	awsBoolFlags = set(
		"--debug", "--no-verify-ssl", "--no-paginate", "--no-sign-request", "--no-cli-pager",
		"--cli-auto-prompt", "--no-cli-auto-prompt", "--version", "--dry-run", "--no-dry-run",
	)
	azValueFlags = map[string]bool{
		"--name": true, "-n": true, "--resource-group": true, "-g": true,
		"--subscription": true, "--query": true, "--output": true, "-o": true,
		"--ids": true, "--location": true, "-l": true,
	}
	gcloudValueFlags = map[string]bool{
		"--project": true, "--account": true, "--configuration": true,
		"--zone": true, "--region": true, "--format": true, "--filter": true,
		"--impersonate-service-account": true, "--verbosity": true,
	}
)

// awsPositionals devolve os argumentos que não são opções nem valores delas.
func awsPositionals(args []string) []string {
	var result []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case !strings.HasPrefix(arg, "-"):
			result = append(result, arg)
		case strings.Contains(arg, "=") || awsBoolFlags[arg]:
		default:
			i++
		}
	}
	return result
}

func matchCloudDelete(tokens []string) (string, bool) {
	if len(tokens) == 0 {
		return "", false
	}

	switch programName(tokens[0]) {
	case "aws":
		args := awsPositionals(tokens[1:])
		if len(args) >= 2 && (args[1] == "terminate-instances" || strings.HasPrefix(args[1], "delete-")) {
			return cloudDeleteDanger, true
		}
	case "az":
		if slices.Contains(positionals(tokens[1:], azValueFlags), "delete") {
			return cloudDeleteDanger, true
		}
	case "gcloud":
		if slices.Contains(positionals(tokens[1:], gcloudValueFlags), "delete") {
			return cloudDeleteDanger, true
		}
	}
	return "", false
}
