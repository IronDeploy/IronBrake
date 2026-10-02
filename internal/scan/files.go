package scan

import (
	"path/filepath"
	"strings"
)

// Filesystem isola o disco para os testes. Read/List devolvem erro quando o
// arquivo/pasta não existe (o scan trata ausência como "nada a relatar").
type Filesystem struct {
	Home string
	Cwd  string
	Read func(path string) ([]byte, error)
	List func(dir string) ([]string, error)
}

// Scan roda o raio-X completo: ambiente + arquivos. É só leitura e sem rede.
func Scan(fs Filesystem, env map[string]string) []Finding {
	var findings []Finding
	findings = append(findings, ScanEnv(env)...)
	findings = append(findings, scanAWS(fs)...)
	findings = append(findings, scanAzure(fs)...)
	findings = append(findings, scanGCP(fs)...)
	findings = append(findings, scanEnvFiles(fs)...)
	findings = append(findings, scanSSH(fs)...)
	findings = append(findings, scanKube(fs)...)
	findings = append(findings, scanTerraform(fs)...)
	findings = append(findings, scanPackageTokens(fs)...)
	findings = append(findings, scanGitRemotes(fs)...)
	findings = append(findings, scanNetrc(fs)...)
	findings = append(findings, scanPgpass(fs)...)
	sortFindings(findings)
	return findings
}

// display encurta o caminho para ~/... na hora de mostrar (rótulo seguro).
func (fs Filesystem) display(path string) string {
	if fs.Home == "" {
		return path
	}
	// Home e path passam por Clean para o prefixo casar mesmo com separadores
	// diferentes (Windows). O rótulo sai sempre com "/": é o que as pessoas
	// leem e o que o shield casa.
	if home := filepath.Clean(fs.Home); strings.HasPrefix(path, home) {
		return "~" + filepath.ToSlash(strings.TrimPrefix(path, home))
	}
	return path
}

// --- AWS -------------------------------------------------------------------

func scanAWS(fs Filesystem) []Finding {
	var findings []Finding

	path := filepath.Join(fs.Home, ".aws", "credentials")
	if data, err := fs.Read(path); err == nil {
		where := fs.display(path)
		for _, p := range parseAWSProfiles(data) {
			label := where + " [perfil " + p.name + "]"
			switch {
			case p.hasStaticKey && !p.hasSessionToken:
				findings = append(findings, Finding{"AWS", label, Critical,
					"perfil AWS com chave de longa duração — não expira"})
			case isPrivilegedName(p.name):
				findings = append(findings, Finding{"AWS", label, Critical,
					"perfil AWS com nome de admin/produção"})
			}
		}
	}

	findings = append(findings, scanAWSSSOCache(fs)...)
	return findings
}

// scanAWSSSOCache olha ~/.aws/sso/cache: cada arquivo JSON de sessão SSO
// carrega um access token ativo até expirar. Só confere presença, nunca lê o
// conteúdo do token.
func scanAWSSSOCache(fs Filesystem) []Finding {
	dir := filepath.Join(fs.Home, ".aws", "sso", "cache")
	names, err := fs.List(dir)
	if err != nil {
		return nil
	}
	var findings []Finding
	for _, name := range names {
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		path := filepath.Join(dir, name)
		data, err := fs.Read(path)
		if err != nil || !strings.Contains(string(data), "accessToken") {
			continue
		}
		findings = append(findings, Finding{"AWS", fs.display(path), High,
			"token de sessão SSO da AWS em cache — vale até expirar, sem precisar de login de novo"})
	}
	return findings
}

type awsProfile struct {
	name            string
	hasStaticKey    bool
	hasSessionToken bool
}

func parseAWSProfiles(data []byte) []awsProfile {
	var profiles []awsProfile
	var cur *awsProfile
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			name := strings.TrimSpace(line[1 : len(line)-1])
			name = strings.TrimPrefix(name, "profile ")
			profiles = append(profiles, awsProfile{name: name})
			cur = &profiles[len(profiles)-1]
			continue
		}
		if cur == nil {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(strings.SplitN(line, "=", 2)[0]))
		switch key {
		case "aws_secret_access_key":
			cur.hasStaticKey = true
		case "aws_session_token", "aws_security_token":
			cur.hasSessionToken = true
		}
	}
	return profiles
}

var privilegedMarkers = []string{"admin", "root", "prod", "prd", "production", "owner"}

func isPrivilegedName(name string) bool {
	low := strings.ToLower(name)
	for _, m := range privilegedMarkers {
		if strings.Contains(low, m) {
			return true
		}
	}
	return false
}

// --- .env ------------------------------------------------------------------

var envFileNames = []string{".env", ".env.local", ".env.development", ".env.production", ".env.prod"}

// scanEnvFiles procura arquivos .env na pasta do comando e nas pais (até o
// home ou 5 níveis) com chaves que parecem token.
func scanEnvFiles(fs Filesystem) []Finding {
	var findings []Finding
	dir := fs.Cwd
	for depth := 0; depth < 5 && dir != "" && dir != "/"; depth++ {
		for _, name := range envFileNames {
			path := filepath.Join(dir, name)
			data, err := fs.Read(path)
			if err != nil {
				continue
			}
			if keys := secretKeysIn(data); len(keys) > 0 {
				findings = append(findings, Finding{".env", fs.display(path), High,
					"segredo em texto puro no arquivo (ex.: " + keys[0] + ")"})
			}
		}
		if dir == fs.Home {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return findings
}

// secretKeysIn devolve os NOMES de chave que cheiram a segredo (nunca o valor).
func secretKeysIn(data []byte) []string {
	var keys []string
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, _, found := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !found || key == "" {
			continue
		}
		if _, ok := classifyEnv(key, false); ok {
			keys = append(keys, key)
		}
	}
	return keys
}

// --- SSH -------------------------------------------------------------------

func scanSSH(fs Filesystem) []Finding {
	dir := filepath.Join(fs.Home, ".ssh")
	names, err := fs.List(dir)
	if err != nil {
		return nil
	}
	var findings []Finding
	for _, name := range names {
		path := filepath.Join(dir, name)
		data, err := fs.Read(path)
		if err != nil || !isPrivateKey(data) {
			continue
		}
		if keyIsEncrypted(data) {
			continue // com senha: risco menor, não relata no v0
		}
		findings = append(findings, Finding{"SSH", fs.display(path), Medium,
			"chave SSH privada sem senha — quem lê o arquivo usa a chave"})
	}
	return findings
}

func isPrivateKey(data []byte) bool {
	return strings.Contains(string(data), "PRIVATE KEY")
}

// keyIsEncrypted: marcadores de chave protegida por senha (PEM antigo ou OpenSSH).
func keyIsEncrypted(data []byte) bool {
	s := string(data)
	return strings.Contains(s, "ENCRYPTED") || strings.Contains(s, "DEK-Info") || strings.Contains(s, "bcrypt")
}

// --- kubeconfig ------------------------------------------------------------

func scanKube(fs Filesystem) []Finding {
	path := filepath.Join(fs.Home, ".kube", "config")
	data, err := fs.Read(path)
	if err != nil {
		return nil
	}
	where := fs.display(path)

	var findings []Finding
	if kubeHasProdContext(data) {
		findings = append(findings, Finding{"Kubernetes", where, Medium,
			"contexto de produção no kubeconfig ao alcance do agente"})
	}
	if kubeHasEmbeddedCredential(data) {
		findings = append(findings, Finding{"Kubernetes", where, High,
			"credencial embutida no kubeconfig (token/chave em texto no arquivo)"})
	}
	return findings
}

func kubeHasProdContext(data []byte) bool {
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if name, ok := strings.CutPrefix(line, "name:"); ok && isPrivilegedName(strings.TrimSpace(name)) {
			return true
		}
		if cur, ok := strings.CutPrefix(line, "current-context:"); ok && isPrivilegedName(strings.TrimSpace(cur)) {
			return true
		}
	}
	return false
}

func kubeHasEmbeddedCredential(data []byte) bool {
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		for _, marker := range []string{"client-key-data:", "token:", "password:"} {
			if strings.HasPrefix(line, marker) && strings.TrimSpace(strings.TrimPrefix(line, marker)) != "" {
				return true
			}
		}
	}
	return false
}

// --- Azure -------------------------------------------------------------

// scanAzure olha ~/.azure: cache de tokens da CLI e credenciais de service
// principal salvas em disco. Só confere presença/nome de arquivo, nunca o
// conteúdo do token.
func scanAzure(fs Filesystem) []Finding {
	dir := filepath.Join(fs.Home, ".azure")
	names, err := fs.List(dir)
	if err != nil {
		return nil
	}
	nameSet := make(map[string]bool, len(names))
	for _, n := range names {
		nameSet[n] = true
	}

	var findings []Finding
	if nameSet["accessTokens.json"] {
		path := filepath.Join(dir, "accessTokens.json")
		findings = append(findings, Finding{"Azure", fs.display(path), High,
			"cache de tokens de acesso do Azure CLI em texto puro (formato antigo)"})
	}
	if nameSet["msal_token_cache.json"] {
		path := filepath.Join(dir, "msal_token_cache.json")
		findings = append(findings, Finding{"Azure", fs.display(path), Medium,
			"cache de tokens MSAL do Azure CLI"})
	}
	if nameSet["service_principal_entries.json"] {
		path := filepath.Join(dir, "service_principal_entries.json")
		findings = append(findings, Finding{"Azure", fs.display(path), High,
			"credenciais de service principal do Azure (client secret) salvas em disco"})
	}
	if nameSet["azureProfile.json"] {
		path := filepath.Join(dir, "azureProfile.json")
		if data, err := fs.Read(path); err == nil && azureProfileLooksPrivileged(data) {
			findings = append(findings, Finding{"Azure", fs.display(path), Medium,
				"assinatura/conta ativa do Azure CLI parece produção"})
		}
	}
	return findings
}

// azureProfileLooksPrivileged procura nome de assinatura/tenant que bata com
// os marcadores de produção, sem olhar valores de credencial (não há nenhum
// nesse arquivo — ele só lista contas).
func azureProfileLooksPrivileged(data []byte) bool {
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if strings.Contains(line, `"name"`) || strings.Contains(line, `"subscriptionId"`) {
			if isPrivilegedName(line) {
				return true
			}
		}
	}
	return false
}

// --- GCP -----------------------------------------------------------------

// scanGCP olha ~/.config/gcloud: credenciais padrão de aplicação (ADC), o
// banco local de contas e credenciais legadas por conta.
func scanGCP(fs Filesystem) []Finding {
	dir := filepath.Join(fs.Home, ".config", "gcloud")
	var findings []Finding

	adc := filepath.Join(dir, "application_default_credentials.json")
	if _, err := fs.Read(adc); err == nil {
		findings = append(findings, Finding{"GCP", fs.display(adc), High,
			"credenciais padrão de aplicação (ADC) do gcloud — refresh token ou chave de service account"})
	}

	credsDB := filepath.Join(dir, "credentials.db")
	if _, err := fs.Read(credsDB); err == nil {
		findings = append(findings, Finding{"GCP", fs.display(credsDB), Medium,
			"banco local de credenciais do gcloud — pode ter mais de uma conta salva"})
	}

	legacyDir := filepath.Join(dir, "legacy_credentials")
	if accounts, err := fs.List(legacyDir); err == nil {
		for _, account := range accounts {
			path := filepath.Join(legacyDir, account, "adc.json")
			if _, err := fs.Read(path); err == nil {
				label := fs.display(path)
				sev := Medium
				if isPrivilegedName(account) {
					sev = High
				}
				findings = append(findings, Finding{"GCP", label, sev,
					"credencial legada da conta " + account + " salva no gcloud"})
			}
		}
	}

	activeConfig := filepath.Join(dir, "configurations", "config_default")
	if data, err := fs.Read(activeConfig); err == nil {
		if project, ok := gcloudActiveProject(data); ok && isPrivilegedName(project) {
			findings = append(findings, Finding{"GCP", fs.display(activeConfig), Medium,
				"projeto ativo do gcloud (" + project + ") parece produção"})
		}
	}

	return findings
}

// gcloudActiveProject lê "project = x" da seção [core] do config_default.
func gcloudActiveProject(data []byte) (string, bool) {
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		key, value, found := strings.Cut(line, "=")
		if found && strings.TrimSpace(key) == "project" {
			return strings.TrimSpace(value), true
		}
	}
	return "", false
}

// --- .netrc ----------------------------------------------------------------

// scanNetrc olha ~/.netrc: credenciais em texto puro que curl, git e wget
// usam automaticamente por host. Nunca lê o valor da senha, só o host.
func scanNetrc(fs Filesystem) []Finding {
	path := filepath.Join(fs.Home, ".netrc")
	data, err := fs.Read(path)
	if err != nil {
		return nil
	}
	machines := netrcMachinesWithPassword(data)
	if len(machines) == 0 {
		return nil
	}
	var findings []Finding
	for _, m := range machines {
		findings = append(findings, Finding{".netrc", fs.display(path) + " [machine " + m + "]", High,
			"credencial em texto puro para este host, usada automaticamente por curl/git/wget"})
	}
	return findings
}

// netrcMachinesWithPassword devolve os nomes de "machine" (ou "default") que
// têm uma entrada "password" associada. Formato .netrc é por token, não por
// linha, então tokeniza o arquivo inteiro.
func netrcMachinesWithPassword(data []byte) []string {
	fields := strings.Fields(string(data))
	var machines []string
	seen := map[string]bool{}
	current := ""
	for i, tok := range fields {
		switch tok {
		case "machine":
			if i+1 < len(fields) {
				current = fields[i+1]
			}
		case "default":
			current = "default"
		case "password":
			if current != "" && i+1 < len(fields) && !seen[current] {
				machines = append(machines, current)
				seen[current] = true
			}
		}
	}
	return machines
}

// --- .pgpass -----------------------------------------------------------

// scanPgpass olha ~/.pgpass: senhas de Postgres em texto puro, uma por linha
// (host:port:database:user:password). Só conta linhas, nunca lê a senha.
func scanPgpass(fs Filesystem) []Finding {
	path := filepath.Join(fs.Home, ".pgpass")
	data, err := fs.Read(path)
	if err != nil {
		return nil
	}
	n := 0
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		n++
	}
	if n == 0 {
		return nil
	}
	return []Finding{{".pgpass", fs.display(path), High,
		"senha(s) de Postgres em texto puro, usadas automaticamente por psql"}}
}
