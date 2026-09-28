package scan

import (
	"path/filepath"
	"testing"

	"github.com/IronDeploy/IronBrake/internal/shield"
)

// TestEveryFindingSourceIsKnownToShield garante que internal/shield.Categories
// (a tabela que liga achado do scan às regras deny) conhece toda fonte que o
// scan de fato produz. Sem isso, "iron scan --manage" mostraria um achado sem
// saber travá-lo nem explicar por quê.
func TestEveryFindingSourceIsKnownToShield(t *testing.T) {
	home := "/home/ana"
	cwd := "/home/ana/projeto"
	fs := fakeFS(home, cwd, map[string]string{
		filepath.Join(home, ".aws", "credentials"):                                       "[default]\naws_secret_access_key = FAKE\n",
		filepath.Join(home, ".azure", "accessTokens.json"):                               `[{"accessToken":"FAKE"}]`,
		filepath.Join(home, ".config", "gcloud", "application_default_credentials.json"): `{"refresh_token":"FAKE"}`,
		filepath.Join(cwd, ".env"):                                                       "GITHUB_TOKEN=ghp_FAKE\n",
		filepath.Join(home, ".ssh", "id_ed25519"):                                        "-----BEGIN OPENSSH PRIVATE KEY-----\nx\n-----END OPENSSH PRIVATE KEY-----\n",
		filepath.Join(home, ".kube", "config"):                                           "current-context: prod\n",
		filepath.Join(home, ".terraform.d", "credentials.tfrc.json"):                     `{"credentials":{}}`,
		filepath.Join(home, ".npmrc"):                                                    "//r/:_authToken=FAKE\n",
		filepath.Join(home, ".pypirc"):                                                   "[pypi]\npassword = FAKE\n",
		filepath.Join(home, ".docker", "config.json"):                                    `{"auths":{"h":{"auth":"FAKE"}}}`,
		filepath.Join(home, ".config", "gh", "hosts.yml"):                                "github.com:\n    oauth_token: FAKE\n",
		filepath.Join(home, ".netrc"):                                                    "machine h\nlogin u\npassword FAKE\n",
		filepath.Join(home, ".pgpass"):                                                   "h:5432:d:u:FAKE\n",
		filepath.Join(cwd, ".git", "config"):                                             "[remote \"origin\"]\n\turl = https://x-access-token:FAKE@github.com/o/r.git\n",
	})
	env := map[string]string{"AWS_ACCESS_KEY_ID": "AKIAFAKE", "AWS_SECRET_ACCESS_KEY": "FAKE"}

	findings := Scan(fs, env)
	if len(findings) == 0 {
		t.Fatal("fixture deveria gerar achados em toda fonte")
	}

	seen := map[string]bool{}
	for _, f := range findings {
		seen[f.Source] = true
		if _, ok := shield.For(f.Source); !ok {
			t.Errorf("fonte %q não está em shield.Categories", f.Source)
		}
	}

	// E o inverso: nenhuma categoria "sobrando" que o scan nunca produz de
	// verdade (exceto as documentadas como não-ocultáveis).
	for _, c := range shield.Categories {
		if len(c.Rules) == 0 {
			continue // "variável de ambiente" e "git" não precisam de fixture aqui
		}
		if !seen[c.Name] {
			t.Errorf("categoria %q não foi exercitada por nenhum achado da fixture", c.Name)
		}
	}
}
