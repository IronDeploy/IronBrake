package rules

import (
	"testing"

	"github.com/IronDeploy/IronBrake/internal/hook"
)

func TestContainerDelete(t *testing.T) {
	runRuleCases(t, containerDelete, []ruleCase{
		{`docker system prune -a`, hook.Ask, hook.Deny},
		{`docker system prune --volumes`, hook.Ask, hook.Deny},
		{`docker volume prune`, hook.Ask, hook.Deny},
		{`docker image prune -a`, hook.Ask, hook.Deny},
		{`docker container prune`, hook.Ask, hook.Deny},
		{`docker volume rm dados-loja`, hook.Ask, hook.Deny},
		{`docker volume remove dados`, hook.Ask, hook.Deny},
		{`docker rm -f api`, hook.Ask, hook.Deny},
		{`docker --context staging rm -f api`, hook.Ask, hook.Deny},
		{`podman volume rm dados`, hook.Ask, hook.Deny},
		{`docker compose down -v`, hook.Ask, hook.Deny},
		{`docker compose down --volumes`, hook.Ask, hook.Deny},
		{`docker-compose down -v`, hook.Ask, hook.Deny},

		// Inofensivos.
		{`docker ps`, hook.Allow, hook.Allow},
		{`docker rm api`, hook.Allow, hook.Allow}, // sem -f, o contêiner precisa estar parado
		{`docker compose down`, hook.Allow, hook.Allow},
		{`docker compose up -d`, hook.Allow, hook.Allow},
		{`docker volume ls`, hook.Allow, hook.Allow},
		{`docker build -t app .`, hook.Allow, hook.Allow},
		{`docker-compose down`, hook.Allow, hook.Allow},
	})
}
