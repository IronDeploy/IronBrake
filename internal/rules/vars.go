package rules

import (
	"regexp"
	"strings"
)

var varRef = regexp.MustCompile(`\$(?:([A-Za-z_][A-Za-z0-9_]*)|\{([A-Za-z_][A-Za-z0-9_]*)\})`)

// recordAssignments guarda os VAR=valor de um comando que só atribui
// (F=--force) ou usa export/declare/readonly/local (export F=--force). Valor
// com $ ou crase depende do shell e não é guardado. Atribuição na frente de um
// comando (F=x cmd) vale só para ele e não entra.
func recordAssignments(tokens []string, vars map[string]string) {
	i := 0
	if len(tokens) > 0 {
		switch tokens[0] {
		case "export", "declare", "readonly", "local", "typeset":
			i = 1
			for i < len(tokens) && strings.HasPrefix(tokens[i], "-") {
				i++
			}
		}
	}
	rest := tokens[i:]
	for _, t := range rest {
		if !isAssignment(t) {
			return // tem comando depois: não é só atribuição
		}
	}
	for _, t := range rest {
		name, value, _ := strings.Cut(t, "=")
		if strings.ContainsAny(value, "$`") {
			delete(vars, name)
			continue
		}
		vars[name] = value
	}
}

// substituteVars troca $VAR e ${VAR} pelos valores conhecidos. Um token que é
// só a variável e vale várias palavras (F="--force x") vira várias palavras,
// como o shell faz sem aspas. Variável desconhecida fica como está.
func substituteVars(tokens []string, vars map[string]string) []string {
	if len(vars) == 0 {
		return tokens
	}
	var out []string
	for _, t := range tokens {
		if !strings.Contains(t, "$") {
			out = append(out, t)
			continue
		}
		if m := varRef.FindStringSubmatch(t); m != nil && m[0] == t {
			if value, ok := vars[m[1]+m[2]]; ok {
				out = append(out, strings.Fields(value)...)
				continue
			}
		}
		out = append(out, varRef.ReplaceAllStringFunc(t, func(ref string) string {
			m := varRef.FindStringSubmatch(ref)
			if value, ok := vars[m[1]+m[2]]; ok {
				return value
			}
			return ref
		}))
	}
	return out
}

// hasUnresolvedVar: sobrou $ ou crase que o Iron Brake não sabe resolver.
func hasUnresolvedVar(token string) bool {
	return strings.ContainsAny(token, "$`")
}
