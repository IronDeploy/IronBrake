package rules

import (
	"slices"
	"strings"
)

const (
	shutdownDanger      = "desligar ou reiniciar a máquina derruba tudo o que está rodando nela."
	systemctlStopDanger = "systemctl stop/disable/mask derruba um serviço; em produção tira o sistema do ar."
	crontabRemoveDanger = "crontab -r apaga todos os agendamentos do usuário sem confirmar."
	firewallFlushDanger = "limpar as regras de firewall pode expor a máquina ou cortar o seu próprio acesso."
)

var systemDestructive = dangerRule{name: "system-destructive", match: matchSystem}

var systemctlValueFlags = set(
	"-H", "--host", "-M", "--machine", "-t", "--type", "--state", "--property", "-p",
	"--signal", "-s", "--kill-whom", "--job-mode",
)

var systemctlStopVerbs = set("stop", "disable", "mask", "kill")

func matchSystem(tokens []string) (string, bool) {
	if len(tokens) == 0 {
		return "", false
	}
	args := tokens[1:]
	switch programName(tokens[0]) {
	case "shutdown", "reboot", "poweroff", "halt":
		return shutdownDanger, true
	case "init", "telinit":
		if slices.Contains(args, "0") || slices.Contains(args, "6") {
			return shutdownDanger, true
		}
	case "systemctl":
		verbs := positionals(args, systemctlValueFlags)
		if len(verbs) > 0 && systemctlStopVerbs[verbs[0]] {
			return systemctlStopDanger, true
		}
	case "crontab":
		if slices.Contains(args, "-r") {
			return crontabRemoveDanger, true
		}
	case "iptables", "ip6tables":
		if slices.Contains(args, "-F") || slices.Contains(args, "--flush") {
			return firewallFlushDanger, true
		}
	case "nft":
		if slices.Contains(args, "flush") && slices.Contains(args, "ruleset") {
			return firewallFlushDanger, true
		}
	case "ufw":
		if len(args) > 0 && (args[0] == "disable" || slices.Contains(args, "reset")) {
			return firewallFlushDanger, true
		}
	case "pfctl":
		// -F all / -Fa limpam regras, estado ou NAT do firewall do macOS/BSD.
		for _, a := range args {
			if a == "-F" || strings.HasPrefix(a, "-F") && len(a) > 2 {
				return firewallFlushDanger, true
			}
		}
	}
	return "", false
}
