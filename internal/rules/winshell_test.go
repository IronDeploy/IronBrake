package rules

import (
	"encoding/base64"
	"testing"
	"unicode/utf16"

	"github.com/IronDeploy/IronBrake/internal/hook"
)

// encodePowershell monta o valor do -EncodedCommand: base64 de UTF-16LE.
func encodePowershell(text string) string {
	var raw []byte
	for _, u := range utf16.Encode([]rune(text)) {
		raw = append(raw, byte(u), byte(u>>8))
	}
	return base64.StdEncoding.EncodeToString(raw)
}

func TestWindowsShellWrappers(t *testing.T) {
	cases := []struct {
		command string
		want    hook.Decision
	}{
		// O invólucro não esconde o comando: é julgado como se estivesse solto.
		{`cmd /c git push --force origin main`, hook.Deny},
		{`cmd.exe /c "git push --force origin main"`, hook.Deny},
		{`CMD /C "git push --force origin main"`, hook.Deny},
		{`cmd /s /c "git push --force origin main"`, hook.Deny},
		{`C:/Windows/System32/cmd.exe /c "git push --force origin main"`, hook.Deny},
		{`powershell -Command "git push --force origin main"`, hook.Deny},
		{`powershell.exe -NoProfile -ExecutionPolicy Bypass -Command "git push --force origin main"`, hook.Deny},
		{`pwsh -c "git push --force origin main"`, hook.Deny},
		{`pwsh -NoProfile -Command git push --force origin main`, hook.Deny},
		{`powershell "git push --force origin main"`, hook.Deny},
		{`powershell -EncodedCommand ` + encodePowershell("git push --force origin main"), hook.Deny},
		{`pwsh -e ` + encodePowershell("git push --force origin main"), hook.Deny},
		{`cmd /c "echo ok && git push --force origin main"`, hook.Deny},
		{`powershell -Command "echo ok; git push --force origin main"`, hook.Deny},

		// Comandos de produção dentro do invólucro valem igual.
		{`cmd /c "kubectl delete namespace prod"`, hook.Deny},

		// Inofensivos continuam livres.
		{`cmd /c dir`, hook.Allow},
		{`cmd /c "git status"`, hook.Allow},
		{`powershell -Command "Get-ChildItem"`, hook.Allow},
		{`powershell -File build.ps1`, hook.Allow},
		{`powershell -EncodedCommand ` + encodePowershell("git status"), hook.Allow},
		{`powershell -EncodedCommand isso-nao-e-base64!`, hook.Allow},
		{`cmd`, hook.Allow},
		{`powershell`, hook.Allow},
	}
	for _, c := range cases {
		t.Run(c.command, func(t *testing.T) {
			got, reason := CheckAll(c.command, withFiles(devEnv, nil))
			if got != c.want {
				t.Errorf("esperava %q, obtive %q (%s)", c.want, got, reason)
			}
		})
	}
}

func TestWindowsNativeRecursiveDelete(t *testing.T) {
	cases := []struct {
		command string
		dev     hook.Decision
		prod    hook.Decision
	}{
		// Fora da pasta do projeto: ask fora de produção, deny em produção.
		{`cmd /c "rmdir /s /q C:\dados\staging"`, hook.Ask, hook.Deny},
		{`cmd /c "rd /s /q C:\dados\staging"`, hook.Ask, hook.Deny},
		{`cmd /c "RMDIR /S C:\dados\staging"`, hook.Ask, hook.Deny},
		{`cmd /c "del /s /q C:\dados\staging\*"`, hook.Ask, hook.Deny},
		{`powershell -Command "Remove-Item -Recurse -Force C:\dados\staging"`, hook.Ask, hook.Deny},
		{`powershell -Command "Remove-Item C:\dados\staging -Recurse"`, hook.Ask, hook.Deny},
		{`powershell -Command "remove-item -rec -Path C:\dados\staging"`, hook.Ask, hook.Deny},
		{`powershell -Command "Remove-Item -LiteralPath C:\dados\staging -Recurse"`, hook.Ask, hook.Deny},
		{`pwsh -c "ri C:/dados/staging -r"`, hook.Ask, hook.Deny},
		{`pwsh -c "Remove-Item -Recurse /data/staging"`, hook.Ask, hook.Deny},

		// Dentro do projeto ou sem recursão: livre.
		{`cmd /c "rmdir /s /q build"`, hook.Allow, hook.Allow},
		{`cmd /c "rmdir build"`, hook.Allow, hook.Allow},
		{`cmd /c "del /q C:\dados\staging\arquivo.txt"`, hook.Allow, hook.Allow},
		{`powershell -Command "Remove-Item build -Recurse"`, hook.Allow, hook.Allow},
		{`powershell -Command "Remove-Item C:\dados\arquivo.txt"`, hook.Allow, hook.Allow},
		{`powershell -Command "Remove-Item -Path /work/build -Recurse"`, hook.Allow, hook.Allow},

		// rmdir e del do Unix não são recursivos sem o /s.
		{`rmdir vazio`, hook.Allow, hook.Allow},
	}
	for _, c := range cases {
		for _, e := range []struct {
			name string
			env  Env
			want hook.Decision
		}{{"fora de produção", devEnv, c.dev}, {"produção", prodEnv, c.prod}} {
			t.Run(c.command+"/"+e.name, func(t *testing.T) {
				got, reason := CheckAll(c.command, withFiles(e.env, nil))
				if got != e.want {
					t.Errorf("esperava %q, obtive %q (%s)", e.want, got, reason)
				}
			})
		}
	}
}

func TestProgramNameAcceptsBothSeparators(t *testing.T) {
	for in, want := range map[string]string{
		"git":                              "git",
		"/usr/bin/git":                     "git",
		`C:\Program Files\Git\bin\git.exe`: "git",
		"C:/Windows/System32/CMD.EXE":      "cmd",
		"terraform.exe":                    "terraform",
	} {
		if got := programName(in); got != want {
			t.Errorf("programName(%q) = %q, quero %q", in, got, want)
		}
	}
}
