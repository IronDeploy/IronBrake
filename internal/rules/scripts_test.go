package rules

import (
	"strings"
	"testing"

	"github.com/IronDeploy/IronBrake/internal/hook"
)

func scriptEnv(base Env, files map[string]string) Env {
	return withFiles(base, files)
}

func TestScriptContent(t *testing.T) {
	files := map[string]string{
		"/work/deploy.sh":      "#!/bin/sh\nset -e\nkubectl delete namespace app\n",
		"/work/ok.sh":          "#!/bin/sh\n# never run git push --force here\nset -e\ngo build ./...\nkubectl get pods\n",
		"/work/force.sh":       "git push --force origin main\n",
		"/work/nested.sh":      "./force.sh\n",
		"/work/deep1.sh":       "./deep2.sh\n",
		"/work/deep2.sh":       "./deep3.sh\n",
		"/work/deep3.sh":       "git push --force\n",
		"/work/vars.sh":        "DIR=$1\nrm -rf $DIR/build\n$TOOL run\n",
		"/work/wipe.sh":        "#!/bin/sh\nrm -rf $1\n",
		"/work/wipe2.sh":       "#!/bin/sh\nrm -rf \"$TARGET_DIR\"/*\n",
		"/work/guarded.sh":     "#!/bin/sh\nset -eu\nrm -rf \"$TARGET_DIR\"/*\n",
		"/work/guarded2.sh":    "#!/bin/sh\nrm -rf \"${TARGET_DIR:?}\"/*\n",
		"/work/mktemp.sh":      "#!/bin/sh\nTMP_X=$(mktemp -d)\nrm -rf \"$TMP_X\"\nfor f in a b; do rm -rf $f; done\n",
		"/work/sub.sh":         "#!/bin/sh\nrm -rf $TARGET_DIR/build\n",
		"/work/deploy.py":      "import boto3\nboto3.client('rds').delete_db_instance(DBInstanceIdentifier='x')\n",
		"/work/shell.py":       "import os\nos.system('git push --force')\n",
		"/work/safe.py":        "print('hello')\nprint('git push --force')\n",
		"/work/clean.js":       "require('fs').rmSync('/data', {recursive: true})\n",
		"/work/deploy-prod.sh": "kubectl delete pvc data\n",
		"/work/Makefile": ".PHONY: build deploy nuke\n" +
			"CC := gcc\n" +
			"build:\n\tgo build ./...\n" +
			"deploy: build\n\t@kubectl delete namespace app\n" +
			"nuke:\n\t-git push --force\n" +
			"clean:\n\trm -rf $$BUILD\n",
		"/work/other/Makefile": "all:\n\tkubectl delete namespace app\n",
	}
	cases := []struct {
		command string
		outside hook.Decision
		inProd  hook.Decision
	}{
		{`./deploy.sh`, hook.Ask, hook.Deny},
		{`bash deploy.sh`, hook.Ask, hook.Deny},
		{`sh -e deploy.sh`, hook.Ask, hook.Deny},
		{`source deploy.sh`, hook.Ask, hook.Deny},
		{`. ./deploy.sh`, hook.Ask, hook.Deny},
		{`sudo ./deploy.sh`, hook.Ask, hook.Deny},
		{`python3 deploy.py`, hook.Ask, hook.Deny},
		{`python3 shell.py`, hook.Deny, hook.Deny}, // git push --force é deny sempre
		{`node clean.js`, hook.Ask, hook.Deny},
		{`./force.sh`, hook.Deny, hook.Deny},
		{`./nested.sh`, hook.Deny, hook.Deny},
		{`make deploy`, hook.Ask, hook.Deny},
		{`make`, hook.Allow, hook.Allow}, // o primeiro alvo (build) é inofensivo
		{`make nuke`, hook.Deny, hook.Deny},
		{`make -C other`, hook.Ask, hook.Deny},
		{`make -f other/Makefile`, hook.Ask, hook.Deny},
		{`./deploy-prod.sh`, hook.Deny, hook.Deny}, // "prod" no nome do arquivo já é produção

		{`./ok.sh`, hook.Allow, hook.Allow},
		{`./vars.sh`, hook.Allow, hook.Allow}, // variável dentro de script é normal
		{`python3 safe.py`, hook.Allow, hook.Allow},
		{`python3 -m pytest`, hook.Allow, hook.Allow},
		{`make build`, hook.Allow, hook.Allow},
		{`make clean`, hook.Ask, hook.Ask}, // rm -rf $$BUILD: BUILD não é definida em lugar nenhum

		// Variável sozinha no rm de um script: o clássico "DIR vazio".
		{`./wipe.sh`, hook.Ask, hook.Ask},
		{`./wipe2.sh`, hook.Ask, hook.Ask},
		{`./guarded.sh`, hook.Allow, hook.Allow},  // set -u
		{`./guarded2.sh`, hook.Allow, hook.Allow}, // ${VAR:?}
		{`./mktemp.sh`, hook.Allow, hook.Allow},   // definida no próprio script
		{`./sub.sh`, hook.Allow, hook.Allow},      // $DIR/build: mais texto que só a variável
		{`./missing.sh`, hook.Allow, hook.Allow},
		{`./tool`, hook.Allow, hook.Allow}, // sem extensão de script
		{`cat deploy.sh`, hook.Allow, hook.Allow},
		{`./deep1.sh`, hook.Allow, hook.Allow}, // 3 scripts fundo: além do limite
	}
	for _, c := range cases {
		for _, e := range []struct {
			name string
			env  Env
			want hook.Decision
		}{{"fora de produção", devEnv, c.outside}, {"produção", prodEnv, c.inProd}} {
			t.Run(c.command+"/"+e.name, func(t *testing.T) {
				got, reason := CheckAll(c.command, scriptEnv(e.env, files))
				if got != e.want {
					t.Errorf("esperava %q, obtive %q (%s)", e.want, got, reason)
				}
				if got != hook.Allow && reason == "" {
					t.Error("deny e ask precisam de um motivo")
				}
			})
		}
	}
}

func TestScriptReasonNamesFileNotCommand(t *testing.T) {
	env := scriptEnv(devEnv, map[string]string{"/work/deploy.sh": "kubectl delete namespace app\n"})
	d, reason := CheckAll(`./deploy.sh`, env)
	if d != hook.Ask {
		t.Fatalf("esperava ask, obtive %q", d)
	}
	if !strings.Contains(reason, "deploy.sh") {
		t.Errorf("o motivo devia citar o arquivo: %q", reason)
	}
	if strings.Contains(reason, "kubectl delete namespace app") {
		t.Errorf("o motivo não deve repetir o comando: %q", reason)
	}
}

func TestScriptWithoutReader(t *testing.T) {
	if d, _ := CheckAll(`./deploy.sh`, devEnv); d != hook.Allow {
		t.Errorf("sem leitor de arquivos, esperava allow, obtive %q", d)
	}
}

func TestMakeRecipes(t *testing.T) {
	makefile := "VAR := 1\n.PHONY: a\na: b c\n\t@echo a\nb:\n\techo b\nc: ; echo c\n"
	got := makeRecipes(makefile, nil)
	for _, want := range []string{"echo a", "echo b", "echo c"} {
		if !strings.Contains(got, want) {
			t.Errorf("esperava %q em %q", want, got)
		}
	}
	if strings.Contains(makeRecipes(makefile, []string{"b"}), "echo a") {
		t.Error("pedir só b não devia incluir a")
	}
}
