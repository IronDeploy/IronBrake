package scan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeFS monta um filesystem em memória para as fixtures (tudo falso).
func fakeFS(home, cwd string, files map[string]string) Filesystem {
	return Filesystem{
		Home: home,
		Cwd:  cwd,
		Read: func(path string) ([]byte, error) {
			if content, ok := files[path]; ok {
				return []byte(content), nil
			}
			return nil, os.ErrNotExist
		},
		List: func(dir string) ([]string, error) {
			// Como os.ReadDir de verdade: devolve o primeiro segmento de cada
			// caminho dentro de dir, seja ele um arquivo ou uma "pasta"
			// sintetizada a partir de um arquivo mais fundo.
			var names []string
			seen := map[string]bool{}
			prefix := dir + "/"
			for p := range files {
				rest, ok := strings.CutPrefix(p, prefix)
				if !ok {
					continue
				}
				first, _, _ := strings.Cut(rest, "/")
				if !seen[first] {
					names = append(names, first)
					seen[first] = true
				}
			}
			if len(names) == 0 {
				return nil, os.ErrNotExist
			}
			return names, nil
		},
	}
}

func TestScanAWS(t *testing.T) {
	home := "/home/ana"
	creds := "[default]\naws_access_key_id = AKIAFAKE\naws_secret_access_key = FAKE000\n\n" +
		"[temp]\naws_access_key_id = ASIAFAKE\naws_secret_access_key = FAKE000\naws_session_token = FAKE000\n\n" +
		"[prod-admin]\nrole_arn = arn:aws:iam::1:role/x\nsource_profile = default\n"
	fs := fakeFS(home, home, map[string]string{
		filepath.Join(home, ".aws", "credentials"): creds,
	})

	got := scanAWS(fs)

	// default: chave de longa duração -> crítico
	if f, ok := findingFor(got, "~/.aws/credentials [perfil default]"); !ok || f.Severity != Critical {
		t.Errorf("perfil default deveria ser crítico, obtive %+v (ok=%v)", f, ok)
	}
	// temp: tem session token -> não é chave de longa duração; mas o nome não é
	// privilegiado, então não deve virar achado.
	if _, ok := findingFor(got, "~/.aws/credentials [perfil temp]"); ok {
		t.Error("perfil temp (sessão, nome comum) não deveria ser marcado")
	}
	// prod-admin: sem chave estática, mas nome privilegiado -> crítico
	if f, ok := findingFor(got, "~/.aws/credentials [perfil prod-admin]"); !ok || f.Severity != Critical {
		t.Errorf("perfil prod-admin deveria ser crítico pelo nome, obtive %+v (ok=%v)", f, ok)
	}
}

func TestScanEnvFiles(t *testing.T) {
	home := "/home/ana"
	cwd := "/home/ana/projeto/api"
	fs := fakeFS(home, cwd, map[string]string{
		filepath.Join(cwd, ".env"):                  "PORT=3000\nGITHUB_TOKEN=ghp_FAKE\n",
		filepath.Join(home, "projeto", ".env"):      "DEBUG=true\n", // sem segredo, não conta
		filepath.Join(home, "projeto", ".env.prod"): "DATABASE_URL=postgres://u:FAKE@h/db\n",
	})

	got := scanEnvFiles(fs)

	if f, ok := findingFor(got, "~/projeto/api/.env"); !ok || f.Severity != High {
		t.Errorf("esperava alto para o .env com GITHUB_TOKEN, obtive %+v (ok=%v)", f, ok)
	}
	if _, ok := findingFor(got, "~/projeto/.env"); ok {
		t.Error(".env só com DEBUG não deveria ser marcado")
	}
	if _, ok := findingFor(got, "~/projeto/.env.prod"); !ok {
		t.Error("esperava marcar o .env.prod com DATABASE_URL")
	}
}

func TestScanSSH(t *testing.T) {
	home := "/home/ana"
	sshDir := filepath.Join(home, ".ssh")
	fs := fakeFS(home, home, map[string]string{
		filepath.Join(sshDir, "id_ed25519"):     "-----BEGIN OPENSSH PRIVATE KEY-----\nsemssenha\n-----END OPENSSH PRIVATE KEY-----\n",
		filepath.Join(sshDir, "id_rsa"):         "-----BEGIN RSA PRIVATE KEY-----\nProc-Type: 4,ENCRYPTED\nDEK-Info: AES\ncomsenha\n-----END RSA PRIVATE KEY-----\n",
		filepath.Join(sshDir, "id_ed25519.pub"): "ssh-ed25519 AAAA...",
		filepath.Join(sshDir, "known_hosts"):    "github.com ssh-rsa AAAA...",
	})

	got := scanSSH(fs)

	if f, ok := findingFor(got, "~/.ssh/id_ed25519"); !ok || f.Severity != Medium {
		t.Errorf("chave sem senha deveria ser médio, obtive %+v (ok=%v)", f, ok)
	}
	if _, ok := findingFor(got, "~/.ssh/id_rsa"); ok {
		t.Error("chave com senha não deveria ser marcada")
	}
	if _, ok := findingFor(got, "~/.ssh/id_ed25519.pub"); ok {
		t.Error("chave pública não deveria ser marcada")
	}
}

func TestScanKube(t *testing.T) {
	home := "/home/ana"
	cfg := "apiVersion: v1\ncurrent-context: prod-cluster\ncontexts:\n- name: prod-cluster\nusers:\n- name: admin\n  user:\n    token: FAKE-token-value\n"
	fs := fakeFS(home, home, map[string]string{
		filepath.Join(home, ".kube", "config"): cfg,
	})

	got := scanKube(fs)

	if _, ok := findingFor(got, "~/.kube/config"); !ok {
		t.Fatal("esperava achados no kubeconfig")
	}
	// deve ter tanto o contexto de produção (médio) quanto a credencial embutida (alto)
	var hasProd, hasEmbedded bool
	for _, f := range got {
		if strings.Contains(f.Note, "produção") {
			hasProd = true
		}
		if strings.Contains(f.Note, "embutida") && f.Severity == High {
			hasEmbedded = true
		}
	}
	if !hasProd || !hasEmbedded {
		t.Errorf("esperava contexto de produção e credencial embutida; prod=%v embedded=%v", hasProd, hasEmbedded)
	}
}

func TestScanNeverLeaksValuesFromFiles(t *testing.T) {
	home := "/home/ana"
	secret := "SUPER-FAKE-SECRET-123"
	fs := fakeFS(home, home, map[string]string{
		filepath.Join(home, ".aws", "credentials"):                                       "[default]\naws_secret_access_key = " + secret + "\n",
		filepath.Join(home, ".kube", "config"):                                           "users:\n- user:\n    token: " + secret + "\n",
		filepath.Join(home, ".env"):                                                      "API_KEY=" + secret + "\n",
		filepath.Join(home, ".aws", "sso", "cache", "abc123.json"):                       `{"accessToken":"` + secret + `"}`,
		filepath.Join(home, ".azure", "accessTokens.json"):                               `[{"accessToken":"` + secret + `"}]`,
		filepath.Join(home, ".config", "gcloud", "application_default_credentials.json"): `{"refresh_token":"` + secret + `"}`,
		filepath.Join(home, ".netrc"):                                                    "machine api.example.com\nlogin ana\npassword " + secret + "\n",
		filepath.Join(home, ".pgpass"):                                                   "db.example.com:5432:app:ana:" + secret + "\n",
		filepath.Join(home, ".pypirc"):                                                   "[pypi]\nusername = ana\npassword = " + secret + "\n",
	})
	for _, f := range Scan(fs, nil) {
		blob := f.Source + " " + f.Where + " " + f.Note
		if strings.Contains(blob, secret) {
			t.Errorf("vazou o segredo no achado: %+v", f)
		}
	}
}

func TestScanAWSSSOCache(t *testing.T) {
	home := "/home/ana"
	fs := fakeFS(home, home, map[string]string{
		filepath.Join(home, ".aws", "sso", "cache", "abc.json"):  `{"accessToken":"FAKE","expiresAt":"2030-01-01"}`,
		filepath.Join(home, ".aws", "sso", "cache", "notes.txt"): "isso não é sessão",
	})

	got := scanAWSSSOCache(fs)

	if f, ok := findingFor(got, "~/.aws/sso/cache/abc.json"); !ok || f.Severity != High {
		t.Errorf("esperava alto para sessão SSO em cache, obtive %+v (ok=%v)", f, ok)
	}
	if _, ok := findingFor(got, "~/.aws/sso/cache/notes.txt"); ok {
		t.Error("arquivo não-json não deveria ser marcado")
	}
}

func TestScanAzure(t *testing.T) {
	home := "/home/ana"
	fs := fakeFS(home, home, map[string]string{
		filepath.Join(home, ".azure", "accessTokens.json"):              `[{"accessToken":"FAKE"}]`,
		filepath.Join(home, ".azure", "service_principal_entries.json"): `[{"clientSecret":"FAKE"}]`,
		filepath.Join(home, ".azure", "azureProfile.json"):              `{"subscriptions":[{"name":"prod-subscription"}]}`,
	})

	got := scanAzure(fs)

	if f, ok := findingFor(got, "~/.azure/accessTokens.json"); !ok || f.Severity != High {
		t.Errorf("esperava alto para accessTokens.json, obtive %+v (ok=%v)", f, ok)
	}
	if f, ok := findingFor(got, "~/.azure/service_principal_entries.json"); !ok || f.Severity != High {
		t.Errorf("esperava alto para service principal, obtive %+v (ok=%v)", f, ok)
	}
	if f, ok := findingFor(got, "~/.azure/azureProfile.json"); !ok || f.Severity != Medium {
		t.Errorf("esperava médio para assinatura de produção, obtive %+v (ok=%v)", f, ok)
	}
}

func TestScanAzureNoDir(t *testing.T) {
	home := "/home/ana"
	fs := fakeFS(home, home, map[string]string{})
	if got := scanAzure(fs); len(got) != 0 {
		t.Errorf("sem ~/.azure não deveria marcar nada, obtive %+v", got)
	}
}

func TestScanGCP(t *testing.T) {
	home := "/home/ana"
	gcloud := filepath.Join(home, ".config", "gcloud")
	fs := fakeFS(home, home, map[string]string{
		filepath.Join(gcloud, "application_default_credentials.json"):         `{"refresh_token":"FAKE"}`,
		filepath.Join(gcloud, "credentials.db"):                               "sqlite-fake-bytes",
		filepath.Join(gcloud, "legacy_credentials", "prod@x.com", "adc.json"): `{"refresh_token":"FAKE"}`,
		filepath.Join(gcloud, "configurations", "config_default"):             "[core]\nproject = prod-cluster\n",
	})

	got := scanGCP(fs)

	if f, ok := findingFor(got, "~/.config/gcloud/application_default_credentials.json"); !ok || f.Severity != High {
		t.Errorf("esperava alto para ADC, obtive %+v (ok=%v)", f, ok)
	}
	if _, ok := findingFor(got, "~/.config/gcloud/credentials.db"); !ok {
		t.Error("esperava marcar o credentials.db")
	}
	if f, ok := findingFor(got, "~/.config/gcloud/legacy_credentials/prod@x.com/adc.json"); !ok || f.Severity != High {
		t.Errorf("conta com nome privilegiado deveria ser alto, obtive %+v (ok=%v)", f, ok)
	}
	if _, ok := findingFor(got, "~/.config/gcloud/configurations/config_default"); !ok {
		t.Error("esperava marcar o projeto ativo de produção")
	}
}

func TestScanNetrc(t *testing.T) {
	home := "/home/ana"
	fs := fakeFS(home, home, map[string]string{
		filepath.Join(home, ".netrc"): "machine api.example.com\nlogin ana\npassword segredo\n\n" +
			"machine ci.example.com\nlogin bot\n", // sem password: não conta
	})

	got := scanNetrc(fs)

	if f, ok := findingFor(got, "~/.netrc [machine api.example.com]"); !ok || f.Severity != High {
		t.Errorf("esperava alto para machine com password, obtive %+v (ok=%v)", f, ok)
	}
	if _, ok := findingFor(got, "~/.netrc [machine ci.example.com]"); ok {
		t.Error("machine sem password não deveria ser marcada")
	}
}

func TestScanPgpass(t *testing.T) {
	home := "/home/ana"
	fs := fakeFS(home, home, map[string]string{
		filepath.Join(home, ".pgpass"): "# comentário\ndb.example.com:5432:app:ana:segredo\n",
	})

	got := scanPgpass(fs)

	if f, ok := findingFor(got, "~/.pgpass"); !ok || f.Severity != High {
		t.Errorf("esperava alto para .pgpass com entrada, obtive %+v (ok=%v)", f, ok)
	}
}

func TestScanPgpassEmpty(t *testing.T) {
	home := "/home/ana"
	fs := fakeFS(home, home, map[string]string{
		filepath.Join(home, ".pgpass"): "# só comentário\n\n",
	})
	if got := scanPgpass(fs); len(got) != 0 {
		t.Errorf(".pgpass só com comentário não deveria marcar, obtive %+v", got)
	}
}
