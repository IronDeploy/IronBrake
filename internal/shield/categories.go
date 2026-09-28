// Package shield é a única fonte de verdade que liga o que o Iron Shield
// enxerga (internal/scan.Finding.Source) às regras que o escondem
// (internal/setup.HardenDenyRules). O harden e a interface interativa de
// "iron scan --manage" leem a mesma tabela, para nunca desalinhar.
package shield

// Category agrupa achados do iron scan por fonte e as regras Read(...) do
// harden que tiram essa fonte do caminho do agente.
type Category struct {
	// Name bate exatamente com scan.Finding.Source.
	Name string
	// Rules são as regras deny que esta categoria controla. Vazio = não dá
	// para ocultar por aqui (ver Why).
	Rules []string
	// Why explica a limitação quando Rules está vazio.
	Why string
}

// Categories cobre toda fonte que o iron scan produz. A ordem é a ordem de
// exibição no "iron scan --manage".
var Categories = []Category{
	{Name: "AWS", Rules: []string{"Read(~/.aws/**)"}},
	{Name: "SSH", Rules: []string{"Read(~/.ssh/**)"}},
	{Name: "Kubernetes", Rules: []string{"Read(~/.kube/**)"}},
	{Name: "GCP", Rules: []string{"Read(~/.config/gcloud/**)"}},
	{Name: "Azure", Rules: []string{"Read(~/.azure/**)"}},
	{Name: "Terraform", Rules: []string{
		"Read(~/.terraform.d/**)",
		"Read(**/terraform.tfstate)",
		"Read(**/terraform.tfstate.backup)",
	}},
	{Name: "npm", Rules: []string{"Read(~/.npmrc)"}},
	{Name: "PyPI", Rules: []string{"Read(~/.pypirc)", "Read(**/.pypirc)"}},
	{Name: "Docker", Rules: []string{"Read(~/.docker/config.json)"}},
	{Name: "GitHub CLI", Rules: []string{"Read(~/.config/gh/**)"}},
	{Name: ".netrc", Rules: []string{"Read(~/.netrc)"}},
	{Name: ".pgpass", Rules: []string{"Read(~/.pgpass)"}},
	{Name: ".env", Rules: []string{
		"Read(**/.env)",
		"Read(**/.env.local)",
		"Read(**/.env.development)",
		"Read(**/.env.production)",
		"Read(**/.env.prod)",
	}},
	{Name: "variável de ambiente", Why: "não é arquivo — a regra Read do Claude Code não alcança. Rode o agente num shell/sandbox sem essas variáveis."},
	{Name: "git", Why: "o segredo mora dentro da URL do remote, não dá para bloquear sem quebrar a leitura normal do .git/config."},
}

// AllRules devolve todas as regras deny de todas as categorias, na ordem de
// Categories — é a lista que "iron init --harden" e "iron shield lock" gravam
// quando travam tudo de uma vez.
func AllRules() []string {
	var all []string
	for _, c := range Categories {
		all = append(all, c.Rules...)
	}
	return all
}

// For devolve a categoria de um Finding pelo Source. ok=false para uma fonte
// que a tabela não conhece (não deveria acontecer; scan e shield evoluem
// juntos).
func For(source string) (Category, bool) {
	for _, c := range Categories {
		if c.Name == source {
			return c, true
		}
	}
	return Category{}, false
}
