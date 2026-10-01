package watch

// Format é um formato de transcript: onde estão os arquivos e como ler uma
// linha deles.
type Format struct {
	// Files lista os arquivos de transcript de uma pasta.
	Files func(dir string) []string

	// NewParser devolve o leitor de linhas de um arquivo (alguns formatos tiram
	// a sessão do caminho).
	NewParser func(path string) parser
}

// ClaudeFormat: ~/.claude/projects/<projeto>/<sessão>.jsonl.
var ClaudeFormat = Format{
	Files:     transcripts,
	NewParser: func(string) parser { return parseLine },
}
