package hook

// Event é uma chamada de ferramenta antes de executar, já traduzida do
// formato do agente. O resto do Iron Brake só conhece este tipo.
type Event struct {
	// Command é o comando de shell. Vazio se a ferramenta não for shell.
	Command string

	// Tool é o nome da ferramenta como o agente a chama.
	Tool string

	// Shell diz se a ferramenta executa comandos de shell. Cada Agent decide
	// o que conta como shell (Claude Code: "Bash").
	Shell bool

	SessionID string

	// Cwd é onde o comando vai rodar.
	Cwd string
}
