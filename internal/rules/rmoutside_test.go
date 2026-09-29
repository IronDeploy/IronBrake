package rules

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/IronDeploy/IronBrake/internal/hook"
)

func TestRmOutsideProject(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("sem home")
	}
	cases := []struct {
		command string
		outside hook.Decision
		inProd  hook.Decision
	}{
		// Fora da pasta do projeto.
		{`rm -rf /data/staging`, hook.Ask, hook.Deny},
		{`rm -rf /data/prod`, hook.Deny, hook.Deny}, // "prod" no caminho já é produção
		{`sudo rm -rf /srv2/dados`, hook.Ask, hook.Deny},
		{`rm -r /data/staging/*`, hook.Ask, hook.Deny},
		{`rm -Rf ../outro-repo`, hook.Ask, hook.Deny},
		{`rm -rf ../../etc2`, hook.Ask, hook.Deny},
		{`rm --recursive --force /data/staging`, hook.Ask, hook.Deny},
		{`rm -rf ~/dados`, hook.Ask, hook.Deny},
		{`rm -rf $HOME/dados`, hook.Ask, hook.Deny},
		{`rm -rf -- /data/staging`, hook.Ask, hook.Deny},
		{`rm -rf build /data/staging`, hook.Ask, hook.Deny},

		// Dentro do projeto, temporários, sem -r ou sem caminho resolvível.
		{`rm -rf build`, hook.Allow, hook.Allow},
		{`rm -rf ./dist/prod-assets`, hook.Allow, hook.Allow},
		{`rm -rf /work/proj/build`, hook.Allow, hook.Allow},
		{`rm -rf /tmp/x`, hook.Allow, hook.Allow},
		{`rm -rf /var/folders/ab/cd/T/x`, hook.Allow, hook.Allow},
		{`rm -rf sub/../build`, hook.Allow, hook.Allow},
		{`rm /data/staging/arquivo.txt`, hook.Allow, hook.Allow},
		{`rm -f /data/staging/arquivo.txt`, hook.Allow, hook.Allow},
		{`rm -rf $TMPDIR/x`, hook.Allow, hook.Allow},
		{`rm -rf "${TMPDIR}/x"`, hook.Allow, hook.Allow},
		{`rm -rf $PWD/build`, hook.Allow, hook.Allow},
		{`DIR=build; rm -rf $DIR/x`, hook.Allow, hook.Allow},

		// Variável que o Iron Brake não resolve: ask (nunca deny, não dá para saber).
		{`rm -rf $DIR/x`, hook.Ask, hook.Ask},
		{`rm -rf "$BUILD_DIR"`, hook.Ask, hook.Ask},
		{`rm -rf $(cat dir.txt)`, hook.Ask, hook.Ask},
		// Variável definida na mesma linha é resolvida.
		{`DIR=/data/staging; rm -rf $DIR/x`, hook.Ask, hook.Deny},
		{`export DIR=/data/staging && rm -rf ${DIR}`, hook.Ask, hook.Deny},
		{`ls /data/staging`, hook.Allow, hook.Allow},
	}
	for _, c := range cases {
		for _, e := range []struct {
			name string
			env  Env
			want hook.Decision
		}{{"fora de produção", devEnv, c.outside}, {"produção", prodEnv, c.inProd}} {
			env := e.env
			env.Cwd = "/work/proj"
			t.Run(c.command+"/"+e.name, func(t *testing.T) {
				got, reason := checkRule(rmOutside, c.command, env)
				if got != e.want {
					t.Errorf("esperava %q, obtive %q (%s)", e.want, got, reason)
				}
			})
		}
	}

	env := devEnv
	env.Cwd = filepath.Join(home, "proj")
	if got, _ := checkRule(rmOutside, `rm -rf ~/proj/build`, env); got != hook.Allow {
		t.Errorf("~/proj/build está dentro do projeto: esperava allow, obtive %q", got)
	}
	if got, _ := checkRule(rmOutside, `rm -rf ~/outra`, env); got != hook.Ask {
		t.Errorf("~/outra está fora do projeto: esperava ask, obtive %q", got)
	}
}
