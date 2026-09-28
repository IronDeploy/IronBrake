package rules

import (
	"testing"

	"github.com/IronDeploy/IronBrake/internal/hook"
)

func TestHelmDestructive(t *testing.T) {
	runRuleCases(t, helmDestructive, []ruleCase{
		{`helm uninstall api`, hook.Ask, hook.Deny},
		{`helm delete api`, hook.Ask, hook.Deny},
		{`helm -n app uninstall api`, hook.Ask, hook.Deny},
		{`helm rollback api 3`, hook.Ask, hook.Deny},
		{`helm --namespace app rollback api`, hook.Ask, hook.Deny},

		// Inofensivos.
		{`helm install api ./chart`, hook.Allow, hook.Allow},
		{`helm upgrade api ./chart`, hook.Allow, hook.Allow},
		{`helm list`, hook.Allow, hook.Allow},
		{`helm status api`, hook.Allow, hook.Allow},
		{`helm template ./chart`, hook.Allow, hook.Allow},
		{`echo helm uninstall api`, hook.Allow, hook.Allow},
	})
}
