package rules

import (
	"slices"
	"strings"
)

const (
	cloudDeleteDanger  = "o comando de nuvem apaga ou encerra recursos."
	cloudStorageDanger = "o comando apaga objetos em massa ou um bucket inteiro de armazenamento na nuvem."
)

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
		if len(args) >= 2 {
			service, op := args[0], args[1]
			switch {
			case op == "terminate-instances" || strings.HasPrefix(op, "delete-"):
				return cloudDeleteDanger, true
			case service == "s3" && op == "rm" && hasLongFlag(tokens, "--recursive"):
				return cloudStorageDanger, true
			case service == "s3" && op == "rb" && hasLongFlag(tokens, "--force"):
				return cloudStorageDanger, true
			}
		}
	case "az":
		pos := positionals(tokens[1:], azValueFlags)
		if slices.ContainsFunc(pos, func(a string) bool {
			return a == "delete" || a == "purge" || strings.HasPrefix(a, "delete-")
		}) {
			return cloudDeleteDanger, true
		}
	case "gcloud":
		pos := positionals(tokens[1:], gcloudValueFlags)
		if slices.Contains(pos, "delete") {
			return cloudDeleteDanger, true
		}
		if len(pos) >= 2 && pos[0] == "storage" && pos[1] == "rm" {
			return cloudStorageDanger, true
		}
	case "gsutil":
		// gsutil rm [-r] gs://bucket/... apaga objetos ou o bucket.
		if slices.Contains(positionals(tokens[1:], nil), "rm") {
			return cloudStorageDanger, true
		}
	}
	return "", false
}

func hasLongFlag(tokens []string, flag string) bool {
	return slices.Contains(tokens, flag)
}
