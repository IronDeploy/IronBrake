// Package scan é o Iron Shield v0: um raio-X, só de leitura, das credenciais
// que um agente rodando nesta máquina alcançaria. Regra de ouro: NUNCA imprime
// nem guarda o valor de um segredo — só tipo, local e gravidade.
package scan

import (
	"sort"
	"strings"
)

// Severity ordena os achados: quanto maior, mais perigoso ao alcance do agente.
type Severity int

const (
	Low Severity = iota
	Medium
	High
	Critical
)

func (s Severity) String() string {
	switch s {
	case Critical:
		return "crítico"
	case High:
		return "alto"
	case Medium:
		return "médio"
	default:
		return "baixo"
	}
}

// Finding descreve UMA credencial ao alcance. Nunca carrega o valor do segredo:
// só de onde veio (Source), um rótulo seguro (Where, ex.: o NOME da variável ou
// o caminho do arquivo) e por que importa (Note).
type Finding struct {
	Source   string
	Where    string
	Severity Severity
	Note     string
}

// sortFindings deixa os mais graves no topo; empate, ordem alfabética estável.
func sortFindings(f []Finding) {
	sort.SliceStable(f, func(i, j int) bool {
		if f[i].Severity != f[j].Severity {
			return f[i].Severity > f[j].Severity
		}
		return f[i].Where < f[j].Where
	})
}

const envSource = "variável de ambiente"

// ScanEnv classifica as variáveis de ambiente que parecem credencial. Recebe o
// mapa nome→valor só para saber presença e distinguir chave de longa duração de
// sessão temporária; o valor NUNCA entra no resultado.
func ScanEnv(env map[string]string) []Finding {
	awsTemporary := env["AWS_SESSION_TOKEN"] != "" || env["AWS_SECURITY_TOKEN"] != ""

	var findings []Finding
	for name, value := range env {
		if value == "" {
			continue
		}
		if f, ok := classifyEnv(name, awsTemporary); ok {
			findings = append(findings, f)
		}
	}
	sortFindings(findings)
	return findings
}

// platformTokens são tokens de plataforma/deploy/registro: dão poder amplo e
// costumam ser de longa duração.
var platformTokens = map[string]bool{
	"GITHUB_TOKEN": true, "GH_TOKEN": true, "GITLAB_TOKEN": true, "GITLAB_CI_TOKEN": true,
	"NPM_TOKEN": true, "PYPI_TOKEN": true, "TWINE_PASSWORD": true, "CARGO_REGISTRY_TOKEN": true,
	"VERCEL_TOKEN": true, "NETLIFY_AUTH_TOKEN": true, "HEROKU_API_KEY": true,
	"FLY_API_TOKEN": true, "RAILWAY_TOKEN": true, "RENDER_API_KEY": true,
	"DIGITALOCEAN_ACCESS_TOKEN": true, "DO_API_TOKEN": true,
	"CLOUDFLARE_API_TOKEN": true, "CLOUDFLARE_API_KEY": true, "CF_API_TOKEN": true,
	"DOCKERHUB_TOKEN": true, "DOCKER_PASSWORD": true,
	"AZURE_CLIENT_SECRET": true, "STRIPE_SECRET_KEY": true, "SLACK_TOKEN": true,
	"SLACK_BOT_TOKEN": true, "HF_TOKEN": true, "HUGGINGFACE_TOKEN": true,
	"OPENAI_API_KEY": true, "ANTHROPIC_API_KEY": true,
}

// dbCredentials são strings de conexão ou senhas de banco.
var dbCredentials = map[string]bool{
	"DATABASE_URL": true, "POSTGRES_PASSWORD": true, "PGPASSWORD": true,
	"MYSQL_PWD": true, "MYSQL_PASSWORD": true, "MONGODB_URI": true, "MONGO_URL": true,
	"REDIS_URL": true, "REDIS_PASSWORD": true,
}

// genericSecretMarkers: qualquer nome que contenha um destes cheira a segredo.
var genericSecretMarkers = []string{
	"SECRET", "TOKEN", "PASSWORD", "PASSWD", "APIKEY", "API_KEY",
	"ACCESS_KEY", "PRIVATE_KEY", "CREDENTIAL", "PASSPHRASE",
}

func classifyEnv(name string, awsTemporary bool) (Finding, bool) {
	upper := strings.ToUpper(name)

	switch upper {
	case "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY":
		if awsTemporary {
			return Finding{envSource, name, Medium, "credencial AWS de sessão (tem session token, expira)"}, true
		}
		return Finding{envSource, name, Critical, "chave AWS de longa duração — não expira"}, true
	case "GOOGLE_APPLICATION_CREDENTIALS":
		return Finding{envSource, name, High, "aponta para um arquivo de chave de service account do GCP"}, true
	}

	switch {
	case platformTokens[upper]:
		return Finding{envSource, name, High, "token de plataforma/deploy de longa duração"}, true
	case dbCredentials[upper]:
		return Finding{envSource, name, High, "credencial de banco de dados"}, true
	}

	for _, marker := range genericSecretMarkers {
		if strings.Contains(upper, marker) {
			return Finding{envSource, name, Medium, "parece um segredo pelo nome"}, true
		}
	}
	return Finding{}, false
}
