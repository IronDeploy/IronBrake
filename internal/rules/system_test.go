package rules

import (
	"testing"

	"github.com/IronDeploy/IronBrake/internal/hook"
)

func TestSystemDestructive(t *testing.T) {
	runRuleCases(t, systemDestructive, []ruleCase{
		{`shutdown -h now`, hook.Ask, hook.Deny},
		{`reboot`, hook.Ask, hook.Deny},
		{`poweroff`, hook.Ask, hook.Deny},
		{`halt`, hook.Ask, hook.Deny},
		{`init 0`, hook.Ask, hook.Deny},
		{`telinit 6`, hook.Ask, hook.Deny},

		{`systemctl stop nginx`, hook.Ask, hook.Deny},
		{`systemctl disable nginx`, hook.Ask, hook.Deny},
		{`systemctl mask nginx`, hook.Ask, hook.Deny},
		{`systemctl -H host stop nginx`, hook.Ask, hook.Deny},

		{`crontab -r`, hook.Ask, hook.Deny},

		{`iptables -F`, hook.Ask, hook.Deny},
		{`iptables --flush`, hook.Ask, hook.Deny},
		{`nft flush ruleset`, hook.Ask, hook.Deny},
		{`ufw disable`, hook.Ask, hook.Deny},
		{`ufw --force reset`, hook.Ask, hook.Deny},
		{`pfctl -F all`, hook.Ask, hook.Deny},

		// Inofensivos.
		{`systemctl status nginx`, hook.Allow, hook.Allow},
		{`systemctl start nginx`, hook.Allow, hook.Allow},
		{`systemctl restart nginx`, hook.Allow, hook.Allow},
		{`crontab -l`, hook.Allow, hook.Allow},
		{`iptables -L`, hook.Allow, hook.Allow},
		{`ufw status`, hook.Allow, hook.Allow},
		{`init`, hook.Allow, hook.Allow},
		{`echo shutdown now`, hook.Allow, hook.Allow},
	})
}
