package rules

import (
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/IronDeploy/IronBrake/internal/hook"
)

const sqlDestructiveDanger = "SQL destrutivo (DROP DATABASE/SCHEMA/TABLE, TRUNCATE ou DELETE sem WHERE, ou com WHERE sempre verdadeiro, como 1=1)."

// Sem um cliente SQL na linha, SQL é só texto (grep, mensagem de commit).
var sqlClients = []string{
	"psql", "pgcli", "mysql", "mycli", "mariadb", "sqlite3", "litecli", "sqlcmd", "sqlplus",
	"clickhouse", "clickhouse-client", "cockroach", "duckdb", "usql", "snowsql",
}

// sqlRule olha a linha inteira: em echo "DROP TABLE x" | psql, o SQL e o
// cliente estão em comandos diferentes.
type sqlRule struct{}

var sqlDestructive Rule = sqlRule{}

func (sqlRule) Name() string { return "sql-destructive" }

func (sqlRule) Check(commands [][]string, env Env) (hook.Decision, string) {
	if !callsSQLClient(commands) {
		return hook.Allow, ""
	}
	// SQL escrito na própria linha (-c, heredoc, echo ... | psql).
	for _, text := range sqlCandidates(commands) {
		if hasDestructiveSQL(text) {
			return decideByEnvironment(sqlDestructiveDanger, commands, env)
		}
	}
	// SQL num arquivo: psql -f arquivo.sql, ou qualquer cliente com < arquivo.sql.
	unreadable := false
	for _, tokens := range commands {
		for _, path := range sqlFileTargets(tokens) {
			if isUnreadableTarget(path) {
				unreadable = true
				continue
			}
			data, ok := env.readTargetFile(path)
			if !ok {
				unreadable = true
				continue
			}
			if hasDestructiveSQL(string(data)) {
				return decideByEnvironment(sqlDestructiveDanger, commands, env)
			}
		}
	}
	if unreadable {
		return decideUnreadableTarget("um arquivo SQL (-f ou < remoto, stdin ou ilegível)", commands, env)
	}
	return hook.Allow, ""
}

// psqlFamily aceita -f/--file como caminho de script (no mysql, -f é --force).
var psqlFamily = set("psql", "pgcli")

// sqlFileTargets: arquivos que o comando manda o cliente executar. -f/--file só
// vale no psql; a redireção de entrada (< arquivo) vale para qualquer cliente.
func sqlFileTargets(tokens []string) []string {
	if len(tokens) == 0 {
		return nil
	}
	var targets []string
	psql := psqlFamily[programName(tokens[0])]
	for i := 1; i < len(tokens); i++ {
		t := tokens[i]
		switch {
		case psql && (t == "-f" || t == "--file"):
			if i+1 < len(tokens) {
				targets = append(targets, tokens[i+1])
				i++
			}
		case psql && strings.HasPrefix(t, "-f="):
			targets = append(targets, strings.TrimPrefix(t, "-f="))
		case psql && strings.HasPrefix(t, "--file="):
			targets = append(targets, strings.TrimPrefix(t, "--file="))
		default:
			if target, ok := inputRedirectTarget(t); ok {
				if target == "" && i+1 < len(tokens) {
					target = tokens[i+1]
					i++
				}
				targets = append(targets, target)
			}
		}
	}
	return targets
}

// inputRedirectTarget reconhece < arquivo e 0< arquivo (colado ou solto).
// Heredoc (<<EOF) e here-string (<<<) não são arquivo e ficam de fora.
func inputRedirectTarget(tok string) (target string, ok bool) {
	s := tok
	for len(s) > 0 && s[0] >= '0' && s[0] <= '9' { // descritor: 0<
		s = s[1:]
	}
	if !strings.HasPrefix(s, "<") || strings.HasPrefix(s, "<<") {
		return "", false
	}
	return strings.TrimPrefix(s, "<"), true
}

// callsSQLClient olha todos os tokens, para pegar docker exec db psql.
func callsSQLClient(commands [][]string) bool {
	for _, tokens := range commands {
		for _, token := range tokens {
			if slices.Contains(sqlClients, programName(token)) {
				return true
			}
		}
	}
	return false
}

// sqlCandidates: tokens com espaço (vieram entre aspas) e cada comando
// inteiro (linhas de um heredoc viram comandos separados).
func sqlCandidates(commands [][]string) []string {
	var texts []string
	for _, tokens := range commands {
		texts = append(texts, strings.Join(tokens, " "))
		for _, token := range tokens {
			if strings.ContainsAny(token, " \t\n") {
				texts = append(texts, token)
			}
		}
	}
	return texts
}

// hasDestructiveSQL procura DROP DATABASE, DROP TABLE, TRUNCATE ou DELETE
// sem WHERE. Os comentários saem antes: "DELETE FROM t -- WHERE id = 1"
// apaga tudo.
func hasDestructiveSQL(text string) bool {
	for _, statement := range strings.Split(stripSQLComments(text), ";") {
		words := strings.FieldsFunc(strings.ToUpper(statement), func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
		})
		// EXPLAIN ANALYZE executa a instrução.
		for len(words) > 0 && (words[0] == "EXPLAIN" || words[0] == "ANALYZE" || words[0] == "VERBOSE") {
			words = words[1:]
		}
		if len(words) == 0 {
			continue
		}
		switch words[0] {
		case "DROP":
			if len(words) > 1 && (words[1] == "DATABASE" || words[1] == "SCHEMA" || words[1] == "TABLE") {
				return true
			}
		case "TRUNCATE":
			return true
		case "DELETE":
			if deletesEverything(statement) {
				return true
			}
		case "WITH": // WITH x AS (DELETE FROM users) SELECT ...
			if loc := deleteWord.FindStringIndex(strings.ToUpper(statement)); loc != nil && deletesEverything(statement[loc[0]:]) {
				return true
			}
		}
	}
	return false
}

var (
	deleteWord = regexp.MustCompile(`\bDELETE\b`)
	whereWord  = regexp.MustCompile(`\bWHERE\b`)
	// depois do filtro, o DELETE ainda pode ter RETURNING, ORDER BY ou LIMIT.
	afterFilter = regexp.MustCompile(`\b(?:RETURNING|ORDER\s+BY|LIMIT)\b`)
)

// deletesEverything: o DELETE não tem WHERE, ou o WHERE vale para todas as
// linhas (WHERE 1=1, WHERE TRUE, WHERE id=1 OR 1=1).
func deletesEverything(statement string) bool {
	upper := strings.ToUpper(statement)
	loc := whereWord.FindStringIndex(upper)
	if loc == nil {
		return true
	}
	cond := upper[loc[1]:]
	if end := afterFilter.FindStringIndex(cond); end != nil {
		cond = cond[:end[0]]
	}
	return alwaysTrue(cutAtUnmatchedParen(cond))
}

// cutAtUnmatchedParen corta no ")" que fecha um WITH x AS (DELETE ...).
func cutAtUnmatchedParen(s string) string {
	depth := 0
	for i, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			if depth == 0 {
				return s[:i]
			}
			depth--
		}
	}
	return s
}

// alwaysTrue reconhece condições que valem para toda linha: literais
// (TRUE, 1), igualdades de um valor com ele mesmo (1=1, 'a'='a', id=id),
// desigualdades óbvias (1<>0), NOT FALSE, e OR com qualquer termo assim.
// AND só vale se todos os termos valem. Na dúvida, diz que não.
func alwaysTrue(cond string) bool {
	cond = strings.TrimSpace(cond)
	for len(cond) > 1 && cond[0] == '(' && matchingParen(cond) == len(cond)-1 {
		cond = strings.TrimSpace(cond[1 : len(cond)-1])
	}
	if cond == "" {
		return false
	}
	if parts := splitTopLevel(cond, "OR"); len(parts) > 1 {
		return slices.ContainsFunc(parts, alwaysTrue)
	}
	if parts := splitTopLevel(cond, "AND"); len(parts) > 1 {
		return !slices.ContainsFunc(parts, func(p string) bool { return !alwaysTrue(p) })
	}
	return atomAlwaysTrue(cond)
}

var (
	sqlTrueLiteral = regexp.MustCompile(`^(?:TRUE|[1-9][0-9]*|NOT\s+(?:FALSE|0))$`)
	sqlComparison  = regexp.MustCompile(`^(.+?)\s*(==|<>|!=|=)\s*(.+)$`)
)

func atomAlwaysTrue(atom string) bool {
	if sqlTrueLiteral.MatchString(atom) {
		return true
	}
	m := sqlComparison.FindStringSubmatch(atom)
	if m == nil {
		return false
	}
	left, op, right := strings.TrimSpace(m[1]), m[2], strings.TrimSpace(m[3])
	if !isSQLValue(left) || !isSQLValue(right) {
		return false
	}
	if op == "=" || op == "==" {
		return left == right
	}
	return left != right && isSQLLiteral(left) && isSQLLiteral(right)
}

var (
	sqlLiteral    = regexp.MustCompile(`^(?:[0-9]+(?:\.[0-9]+)?|'[^']*')$`)
	sqlIdentifier = regexp.MustCompile(`^[A-Z_][A-Z0-9_$]*(?:\.[A-Z_][A-Z0-9_$]*)?$`)
)

func isSQLLiteral(v string) bool { return sqlLiteral.MatchString(v) }

// isSQLValue: literal ou nome de coluna, sem chamada de função nem operação.
func isSQLValue(v string) bool { return isSQLLiteral(v) || sqlIdentifier.MatchString(v) }

// matchingParen devolve a posição do ")" que fecha o "(" de s[0], ou -1.
func matchingParen(s string) int {
	depth := 0
	inString := false
	for i, r := range s {
		switch {
		case r == '\'':
			inString = !inString
		case inString:
		case r == '(':
			depth++
		case r == ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// splitTopLevel divide por uma palavra (AND, OR) fora de parênteses e de
// texto entre aspas simples.
func splitTopLevel(cond, word string) []string {
	var parts []string
	depth, start := 0, 0
	inString := false
	for i := 0; i < len(cond); i++ {
		switch c := cond[i]; {
		case c == '\'':
			inString = !inString
		case inString:
		case c == '(':
			depth++
		case c == ')':
			depth--
		case depth == 0 && isWordAt(cond, i, word):
			parts = append(parts, cond[start:i])
			start = i + len(word)
			i += len(word) - 1
		}
	}
	return append(parts, cond[start:])
}

// isWordAt: word começa em i e está cercada por não-letras.
func isWordAt(s string, i int, word string) bool {
	if !strings.HasPrefix(s[i:], word) {
		return false
	}
	isName := func(b byte) bool {
		return b == '_' || b >= '0' && b <= '9' || b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z'
	}
	if i > 0 && isName(s[i-1]) {
		return false
	}
	end := i + len(word)
	return end == len(s) || !isName(s[end])
}

// stripSQLComments tira -- e /* */, sem mexer em texto entre aspas simples.
func stripSQLComments(text string) string {
	var out strings.Builder
	for i := 0; i < len(text); i++ {
		switch {
		case text[i] == '\'':
			end := strings.IndexByte(text[i+1:], '\'')
			if end < 0 {
				out.WriteString(text[i:])
				return out.String()
			}
			out.WriteString(text[i : i+end+2])
			i += end + 1
		case strings.HasPrefix(text[i:], "--"):
			end := strings.IndexByte(text[i:], '\n')
			if end < 0 {
				return out.String()
			}
			i += end - 1
			out.WriteByte(' ')
		case strings.HasPrefix(text[i:], "/*"):
			end := strings.Index(text[i+2:], "*/")
			if end < 0 {
				return out.String()
			}
			i += end + 3
			out.WriteByte(' ')
		default:
			out.WriteByte(text[i])
		}
	}
	return out.String()
}
