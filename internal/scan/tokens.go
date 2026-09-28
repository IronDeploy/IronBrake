package scan

import (
	"path/filepath"
	"strings"
)

// --- Terraform -------------------------------------------------------------

// tfStatePaths são locais comuns de state relativos à pasta do comando. State
// pode guardar segredos em texto puro e é alcançável pelo agente.
var tfStatePaths = []string{
	"terraform.tfstate",
	"terraform.tfstate.backup",
	filepath.Join(".terraform", "terraform.tfstate"),
}

func scanTerraform(fs Filesystem) []Finding {
	var findings []Finding

	// Token do Terraform Cloud/HCP no home.
	tfCreds := filepath.Join(fs.Home, ".terraform.d", "credentials.tfrc.json")
	if _, err := fs.Read(tfCreds); err == nil {
		findings = append(findings, Finding{"Terraform", fs.display(tfCreds), High,
			"token do Terraform Cloud/HCP"})
	}

	// Arquivos de state na pasta do comando.
	for _, rel := range tfStatePaths {
		path := filepath.Join(fs.Cwd, rel)
		data, err := fs.Read(path)
		if err != nil {
			continue
		}
		sev, note := Medium, "arquivo de state do Terraform ao alcance"
		if stateHasSecrets(data) {
			sev, note = High, "arquivo de state do Terraform com segredos em texto puro"
		}
		findings = append(findings, Finding{"Terraform", fs.display(path), sev, note})
	}
	return findings
}

// stateHasSecrets procura marcadores de segredo dentro do JSON do state, sem
// olhar o valor. É heurística: nomes de atributo sensíveis ou a marca de
// "sensitive_values" com conteúdo.
func stateHasSecrets(data []byte) bool {
	s := strings.ToLower(string(data))
	for _, marker := range []string{
		`"password"`, `"secret"`, `"private_key"`, `"token"`, `"access_key"`,
		`"secret_access_key"`, `"client_secret"`, `"connection_string"`,
	} {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}

// --- Tokens de pacote (npm / Docker / GitHub CLI) --------------------------

func scanPackageTokens(fs Filesystem) []Finding {
	var findings []Finding

	// .npmrc: no home e na pasta do comando.
	for _, path := range []string{filepath.Join(fs.Home, ".npmrc"), filepath.Join(fs.Cwd, ".npmrc")} {
		data, err := fs.Read(path)
		if err != nil {
			continue
		}
		if npmrcHasToken(data) {
			findings = append(findings, Finding{"npm", fs.display(path), High,
				"token de registro npm em texto puro"})
		}
	}

	// ~/.docker/config.json: credencial de registro.
	dockerCfg := filepath.Join(fs.Home, ".docker", "config.json")
	if data, err := fs.Read(dockerCfg); err == nil && dockerHasAuth(data) {
		findings = append(findings, Finding{"Docker", fs.display(dockerCfg), High,
			"credencial de registro Docker no config"})
	}

	// ~/.config/gh/hosts.yml: token da CLI do GitHub.
	ghHosts := filepath.Join(fs.Home, ".config", "gh", "hosts.yml")
	if data, err := fs.Read(ghHosts); err == nil && strings.Contains(string(data), "oauth_token:") {
		findings = append(findings, Finding{"GitHub CLI", fs.display(ghHosts), High,
			"token da CLI do GitHub (gh)"})
	}

	// .pypirc: no home e na pasta do comando.
	for _, path := range []string{filepath.Join(fs.Home, ".pypirc"), filepath.Join(fs.Cwd, ".pypirc")} {
		data, err := fs.Read(path)
		if err != nil {
			continue
		}
		if pypircHasPassword(data) {
			findings = append(findings, Finding{"PyPI", fs.display(path), High,
				"senha/token do PyPI em texto puro"})
		}
	}

	return findings
}

func pypircHasPassword(data []byte) bool {
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		if strings.ToLower(strings.TrimSpace(key)) == "password" && strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func npmrcHasToken(data []byte) bool {
	s := strings.ToLower(string(data))
	return strings.Contains(s, "_authtoken") || strings.Contains(s, ":_password")
}

func dockerHasAuth(data []byte) bool {
	s := string(data)
	// "auth": "<base64>" dentro de "auths" indica credencial salva.
	return strings.Contains(s, `"auth"`) && strings.Contains(s, `"auths"`)
}

// --- Remote git com token na URL -------------------------------------------

// scanGitRemotes procura .git/config na pasta do comando e nas pais, e marca
// remotes https com credencial embutida na URL. A URL NUNCA entra no achado
// (ela contém o token); só o caminho do config e o nome do remote.
func scanGitRemotes(fs Filesystem) []Finding {
	dir := fs.Cwd
	for depth := 0; depth < 6 && dir != "" && dir != "/"; depth++ {
		path := filepath.Join(dir, ".git", "config")
		if data, err := fs.Read(path); err == nil {
			var findings []Finding
			for _, remote := range remotesWithEmbeddedToken(data) {
				findings = append(findings, Finding{"git", fs.display(path) + " [remote " + remote + "]", High,
					"token embutido na URL do remote git"})
			}
			return findings // achou o repo; não sobe mais
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return nil
}

// remotesWithEmbeddedToken devolve os NOMES dos remotes cuja URL https carrega
// userinfo (https://TOKEN@host/...). SSH (git@host) é normal e não conta.
func remotesWithEmbeddedToken(data []byte) []string {
	var remotes []string
	current := ""
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if name, ok := remoteSectionName(line); ok {
			current = name
			continue
		}
		if current == "" {
			continue
		}
		if value, ok := strings.CutPrefix(line, "url"); ok {
			url := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(value), "="))
			if httpsURLHasUserinfo(url) {
				remotes = append(remotes, current)
			}
			current = "" // uma url por seção basta
		}
	}
	return remotes
}

// remoteSectionName: [remote "origin"] -> "origin".
func remoteSectionName(line string) (string, bool) {
	if !strings.HasPrefix(line, "[remote ") || !strings.HasSuffix(line, "]") {
		return "", false
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(line, "[remote "), "]")
	return strings.Trim(inner, `"`), true
}

// httpsURLHasUserinfo: https://algo@host/... (o "algo@" é credencial embutida).
func httpsURLHasUserinfo(url string) bool {
	rest, ok := strings.CutPrefix(url, "https://")
	if !ok {
		return false
	}
	authority := rest
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		authority = rest[:i]
	}
	at := strings.IndexByte(authority, '@')
	return at > 0 // tem userinfo antes do host
}
