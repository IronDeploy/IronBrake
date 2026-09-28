package rules

import (
	"testing"

	"github.com/IronDeploy/IronBrake/internal/hook"
)

func TestPublishDestructive(t *testing.T) {
	runRuleCases(t, publishDestructive, []ruleCase{
		{`npm publish`, hook.Ask, hook.Deny},
		{`npm publish --access public`, hook.Ask, hook.Deny},
		{`pnpm publish`, hook.Ask, hook.Deny},
		{`yarn publish`, hook.Ask, hook.Deny},
		{`npm unpublish pacote@1.0.0`, hook.Ask, hook.Deny},
		{`cargo publish`, hook.Ask, hook.Deny},
		{`gem push pacote-1.0.0.gem`, hook.Ask, hook.Deny},
		{`twine upload dist/*`, hook.Ask, hook.Deny},

		// Inofensivos.
		{`npm install`, hook.Allow, hook.Allow},
		{`npm run build`, hook.Allow, hook.Allow},
		{`npm ci`, hook.Allow, hook.Allow},
		{`cargo build`, hook.Allow, hook.Allow},
		{`gem install rails`, hook.Allow, hook.Allow},
		{`yarn install`, hook.Allow, hook.Allow},
		{`echo npm publish`, hook.Allow, hook.Allow},
	})
}
