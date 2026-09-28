package scan

import (
	"path/filepath"
	"strings"
	"testing"
)

const fakeTok = "FAKE-token-do-not-use-000"

func TestScanTerraform(t *testing.T) {
	home := "/home/ana"
	cwd := "/home/ana/infra"
	fs := fakeFS(home, cwd, map[string]string{
		filepath.Join(home, ".terraform.d", "credentials.tfrc.json"): `{"credentials":{"app.terraform.io":{"token":"` + fakeTok + `"}}}`,
		filepath.Join(cwd, "terraform.tfstate"):                      `{"resources":[{"instances":[{"attributes":{"password":"` + fakeTok + `"}}]}]}`,
		filepath.Join(cwd, ".terraform", "terraform.tfstate"):        `{"resources":[]}`,
	})

	got := scanTerraform(fs)

	if f, ok := findingFor(got, "~/.terraform.d/credentials.tfrc.json"); !ok || f.Severity != High {
		t.Errorf("esperava token do TF Cloud (alto), obtive %+v (ok=%v)", f, ok)
	}
	if f, ok := findingFor(got, "~/infra/terraform.tfstate"); !ok || f.Severity != High {
		t.Errorf("state com password deveria ser alto, obtive %+v (ok=%v)", f, ok)
	}
	if f, ok := findingFor(got, "~/infra/.terraform/terraform.tfstate"); !ok || f.Severity != Medium {
		t.Errorf("state sem segredo deveria ser médio, obtive %+v (ok=%v)", f, ok)
	}
}

func TestScanPackageTokens(t *testing.T) {
	home := "/home/ana"
	cwd := "/home/ana/app"
	fs := fakeFS(home, cwd, map[string]string{
		filepath.Join(home, ".npmrc"):                     "//registry.npmjs.org/:_authToken=" + fakeTok + "\n",
		filepath.Join(home, ".docker", "config.json"):     `{"auths":{"https://index.docker.io/v1/":{"auth":"` + fakeTok + `"}}}`,
		filepath.Join(home, ".config", "gh", "hosts.yml"): "github.com:\n    oauth_token: " + fakeTok + "\n",
	})

	got := scanPackageTokens(fs)

	for _, where := range []string{"~/.npmrc", "~/.docker/config.json", "~/.config/gh/hosts.yml"} {
		if f, ok := findingFor(got, where); !ok || f.Severity != High {
			t.Errorf("esperava alto para %q, obtive %+v (ok=%v)", where, f, ok)
		}
	}
}

func TestScanPypirc(t *testing.T) {
	home := "/home/ana"
	cwd := "/home/ana/lib"
	fs := fakeFS(home, cwd, map[string]string{
		filepath.Join(home, ".pypirc"): "[pypi]\nusername = ana\npassword = " + fakeTok + "\n",
		filepath.Join(cwd, ".pypirc"):  "[pypi]\nusername = ana\n", // sem password: não conta
	})

	got := scanPackageTokens(fs)

	if f, ok := findingFor(got, "~/.pypirc"); !ok || f.Severity != High {
		t.Errorf("esperava alto para .pypirc com password, obtive %+v (ok=%v)", f, ok)
	}
	if _, ok := findingFor(got, "~/lib/.pypirc"); ok {
		t.Error(".pypirc sem password não deveria ser marcado")
	}
}

func TestScanGitRemotes(t *testing.T) {
	home := "/home/ana"
	cwd := "/home/ana/app/src" // .git está numa pasta pai
	cfg := "[remote \"origin\"]\n\turl = https://x-access-token:" + fakeTok + "@github.com/org/repo.git\n" +
		"[remote \"upstream\"]\n\turl = git@github.com:org/repo.git\n" +
		"[remote \"clean\"]\n\turl = https://github.com/org/repo.git\n"
	fs := fakeFS(home, cwd, map[string]string{
		filepath.Join(home, "app", ".git", "config"): cfg,
	})

	got := scanGitRemotes(fs)

	if f, ok := findingFor(got, "~/app/.git/config [remote origin]"); !ok || f.Severity != High {
		t.Errorf("remote origin com token deveria ser alto, obtive %+v (ok=%v)", f, ok)
	}
	if _, ok := findingFor(got, "~/app/.git/config [remote upstream]"); ok {
		t.Error("remote SSH (git@) não deveria ser marcado")
	}
	if _, ok := findingFor(got, "~/app/.git/config [remote clean]"); ok {
		t.Error("remote https sem token não deveria ser marcado")
	}
}

func TestTokensNeverLeakValues(t *testing.T) {
	home := "/home/ana"
	cwd := "/home/ana/app"
	fs := fakeFS(home, cwd, map[string]string{
		filepath.Join(home, ".npmrc"):                 "//r/:_authToken=" + fakeTok + "\n",
		filepath.Join(cwd, "terraform.tfstate"):       `{"password":"` + fakeTok + `"}`,
		filepath.Join(cwd, ".git", "config"):          "[remote \"origin\"]\n\turl = https://tok:" + fakeTok + "@h/r.git\n",
		filepath.Join(home, ".docker", "config.json"): `{"auths":{"h":{"auth":"` + fakeTok + `"}}}`,
	})
	for _, f := range Scan(fs, nil) {
		blob := f.Source + " " + f.Where + " " + f.Note
		if strings.Contains(blob, fakeTok) {
			t.Errorf("vazou o segredo: %+v", f)
		}
	}
}
