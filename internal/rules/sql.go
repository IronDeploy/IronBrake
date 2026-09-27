package rules

import (
	"slices"
	"strings"
	"unicode"

	"github.com/IronDeploy/IronBrake/internal/hook"
)

const sqlDestructiveDanger = "SQL destrutivo (DROP DATABASE, DROP TABLE, TRUNCATE ou DELETE sem WHERE)."

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
	for _, text := range sqlCandidates(commands) {
		if hasDestructiveSQL(text) {
			return decideByEnvironment(sqlDestructiveDanger, commands, env)
		}
	}
	return hook.Allow, ""
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
			if len(words) > 1 && (words[1] == "DATABASE" || words[1] == "TABLE") {
				return true
			}
		case "TRUNCATE":
			return true
		case "DELETE":
			if !slices.Contains(words, "WHERE") {
				return true
			}
		case "WITH": // WITH x AS (DELETE FROM users) SELECT ...
			if i := slices.Index(words, "DELETE"); i >= 0 && !slices.Contains(words[i:], "WHERE") {
				return true
			}
		}
	}
	return false
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
