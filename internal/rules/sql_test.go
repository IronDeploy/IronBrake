package rules

import (
	"testing"

	"github.com/IronDeploy/IronBrake/internal/hook"
)

func TestSQLDestructive(t *testing.T) {
	runRuleCases(t, sqlDestructive, []ruleCase{
		// Destrutivos, mandados a um cliente SQL: ask fora de produção, deny em produção.
		{`psql -c "DROP TABLE users"`, hook.Ask, hook.Deny},
		{`psql -c "drop database shop"`, hook.Ask, hook.Deny},
		{`mysql -e "DROP DATABASE shop"`, hook.Ask, hook.Deny},
		{`psql -c "DROP SCHEMA public CASCADE"`, hook.Ask, hook.Deny},
		{`psql -c "TRUNCATE users, orders"`, hook.Ask, hook.Deny},
		{`mysql -e "DELETE FROM users"`, hook.Ask, hook.Deny},
		{`sqlite3 app.db "delete from sessions;"`, hook.Ask, hook.Deny},
		{`psql -c "BEGIN; DROP TABLE logs; COMMIT;"`, hook.Ask, hook.Deny},
		{`echo "DELETE FROM users;" | psql`, hook.Ask, hook.Deny},
		{`docker exec db psql -U app -c "DROP TABLE users"`, hook.Ask, hook.Deny},
		{"psql <<EOF\nDELETE FROM users;\nEOF", hook.Ask, hook.Deny},

		// WHERE que vale para todas as linhas apaga tudo do mesmo jeito.
		{`psql -c "DELETE FROM users WHERE 1=1"`, hook.Ask, hook.Deny},
		{`psql -c "delete from users where 1 = 1;"`, hook.Ask, hook.Deny},
		{`psql -c "DELETE FROM users WHERE TRUE"`, hook.Ask, hook.Deny},
		{`mysql -e "DELETE FROM users WHERE 1"`, hook.Ask, hook.Deny},
		{`psql -c "DELETE FROM users WHERE 'a'='a'"`, hook.Ask, hook.Deny},
		{`psql -c "DELETE FROM users WHERE id = id"`, hook.Ask, hook.Deny},
		{`psql -c "DELETE FROM users WHERE 1<>0"`, hook.Ask, hook.Deny},
		{`psql -c "DELETE FROM users WHERE (1=1)"`, hook.Ask, hook.Deny},
		{`psql -c "DELETE FROM users WHERE id = 1 OR 1=1"`, hook.Ask, hook.Deny},
		{`psql -c "DELETE FROM users WHERE 1=1 AND 2=2"`, hook.Ask, hook.Deny},
		{`psql -c "DELETE FROM users WHERE 1=1 RETURNING id"`, hook.Ask, hook.Deny},
		{`psql -c "WITH gone AS (DELETE FROM users WHERE 1=1 RETURNING id) SELECT * FROM gone"`, hook.Ask, hook.Deny},
		{`psql -c "DELETE FROM users /* só um */ WHERE 1=1 -- ok"`, hook.Ask, hook.Deny},

		// Sempre verdadeiro por conta de uma chave ou de uma função.
		{`psql -c "DELETE FROM users WHERE id >= 0"`, hook.Ask, hook.Deny},
		{`psql -c "DELETE FROM users WHERE id > 0"`, hook.Ask, hook.Deny},
		{`psql -c "DELETE FROM users WHERE id > -1"`, hook.Ask, hook.Deny},
		{`psql -c "DELETE FROM users WHERE users.id >= 1"`, hook.Ask, hook.Deny},
		{`psql -c "DELETE FROM users WHERE user_id IS NOT NULL"`, hook.Ask, hook.Deny},
		{`psql -c "DELETE FROM users WHERE id IS NOT NULL"`, hook.Ask, hook.Deny},
		{`psql -c "DELETE FROM users WHERE id < 2147483647"`, hook.Ask, hook.Deny},
		{`psql -c "DELETE FROM users WHERE id LIKE '%'"`, hook.Ask, hook.Deny},
		{`psql -c "DELETE FROM users WHERE length(name) >= 0"`, hook.Ask, hook.Deny},
		{`psql -c "DELETE FROM users WHERE ABS(id) >= 0"`, hook.Ask, hook.Deny},
		{`psql -c "DELETE FROM users WHERE id = 5 OR id >= 0"`, hook.Ask, hook.Deny},
		{`psql -c "DELETE FROM accounts WHERE balance >= 0"`, hook.Ask, hook.Deny},
		{`psql -c "DELETE FROM accounts WHERE balance > -1"`, hook.Ask, hook.Deny},

		// Parecidos e inofensivos: allow em qualquer ambiente.
		{`psql -c "DELETE FROM users WHERE id > 100"`, hook.Allow, hook.Allow},
		{`psql -c "DELETE FROM users WHERE id >= 10 AND id < 20"`, hook.Allow, hook.Allow},
		{`psql -c "DELETE FROM users WHERE id >= 0 AND email = 'a@b.c'"`, hook.Allow, hook.Allow},
		{`psql -c "DELETE FROM users WHERE id < 1000"`, hook.Allow, hook.Allow},
		{`psql -c "DELETE FROM accounts WHERE balance > 0"`, hook.Allow, hook.Allow},
		{`psql -c "DELETE FROM accounts WHERE balance >= 100"`, hook.Allow, hook.Allow},
		{`psql -c "DELETE FROM users WHERE deleted_at IS NOT NULL"`, hook.Allow, hook.Ask}, // filtro amplo: ask só em produção
		{`psql -c "DELETE FROM users WHERE status <> 'active'"`, hook.Allow, hook.Ask},
		{`psql -c "DELETE FROM users WHERE deleted_at IS NOT NULL OR status != 'x'"`, hook.Allow, hook.Ask},
		{`psql -c "DELETE FROM users WHERE deleted_at IS NOT NULL AND deleted_at < now() - interval '90 days'"`, hook.Allow, hook.Allow},
		{`psql -c "DELETE FROM users WHERE deleted_at IS NOT NULL AND id = 5"`, hook.Allow, hook.Allow},
		{`psql -c "DELETE FROM users WHERE email = 'a@b.c'"`, hook.Allow, hook.Allow},
		{`psql -c "DELETE FROM users WHERE name LIKE 'a%'"`, hook.Allow, hook.Allow},
		{`psql -c "DELETE FROM users WHERE length(name) > 50"`, hook.Allow, hook.Allow},
		{`psql -c "DELETE FROM users WHERE id = 1 AND 1=1"`, hook.Allow, hook.Allow},
		{`psql -c "DELETE FROM users WHERE 1=1 AND id = 5"`, hook.Allow, hook.Allow},
		{`psql -c "DELETE FROM users WHERE id = 1 OR id = 2"`, hook.Allow, hook.Allow},
		{`psql -c "DELETE FROM users WHERE id = 5 AND (a = 1 OR 1=1)"`, hook.Allow, hook.Allow},
		{`psql -c "DELETE FROM users WHERE email = 'a@b.c'"`, hook.Allow, hook.Allow},
		{`psql -c "DELETE FROM users WHERE created_at < now() - interval '1 year'"`, hook.Allow, hook.Allow},
		{`psql -c "DELETE FROM users WHERE id IN (SELECT id FROM banned)"`, hook.Allow, hook.Allow},
		{`psql -c "DELETE FROM users USING banned WHERE users.id = banned.id"`, hook.Allow, hook.Allow},
		{`psql -c "WITH gone AS (DELETE FROM users WHERE id = 1 RETURNING id) SELECT * FROM gone"`, hook.Allow, hook.Allow},
		{`psql -c "SELECT * FROM users WHERE 1=1"`, hook.Allow, hook.Allow},
		{`psql -c "SELECT * FROM users"`, hook.Allow, hook.Allow},
		{`psql -c "DELETE FROM users WHERE id = 1"`, hook.Allow, hook.Allow},
		{"psql <<EOF\nDELETE FROM users WHERE id = 1;\nEOF", hook.Allow, hook.Allow},
		{`mysql -e "SHOW TABLES"`, hook.Allow, hook.Allow},
		{`psql -c "CREATE TABLE t (id int)"`, hook.Allow, hook.Allow},
		{`psql -c "DROP INDEX idx_users_email"`, hook.Allow, hook.Allow},
		// Sem cliente SQL na linha, é só texto:
		{`grep -r "DROP TABLE" migrations/`, hook.Allow, hook.Allow},
		{`git commit -m "remove DROP TABLE do seed"`, hook.Allow, hook.Allow},
		{`echo "DROP TABLE users" > drop.sql`, hook.Allow, hook.Allow},
	})
}

func TestSQLFile(t *testing.T) {
	files := map[string]string{
		"/work/drop.sql":  "DROP TABLE users;\n",
		"/work/clean.sql": "DELETE FROM sessions;\n",
		"/work/seed.sql":  "INSERT INTO users VALUES (1);\nSELECT * FROM users;\n",
		"/work/safe.sql":  "DELETE FROM sessions WHERE expired = true;\n",
	}
	dev := withFiles(devEnv, files)
	prod := withFiles(prodEnv, files)

	cases := []struct {
		command   string
		dev, prod hook.Decision
	}{
		// psql -f: lê o arquivo e acha o SQL destrutivo.
		{`psql -f drop.sql`, hook.Ask, hook.Deny},
		{`psql --file=clean.sql`, hook.Ask, hook.Deny},
		{`psql -f drop.sql -d loja`, hook.Ask, hook.Deny},

		// Redireção de entrada vale para qualquer cliente.
		{`mysql loja < drop.sql`, hook.Ask, hook.Deny},
		{`sqlite3 app.db < clean.sql`, hook.Ask, hook.Deny},

		// Arquivo inofensivo.
		{`psql -f seed.sql`, hook.Allow, hook.Allow},
		{`psql -f safe.sql`, hook.Allow, hook.Allow},

		// Alvo ilegível: allow fora de produção, ask em produção.
		{`psql -f nao-existe.sql`, hook.Allow, hook.Ask},
		{`mysql < -`, hook.Allow, hook.Ask},

		// mysql -f é --force, não arquivo: não deve tentar ler "drop.sql".
		{`mysql -f -e "SELECT 1"`, hook.Allow, hook.Allow},
	}

	for _, c := range cases {
		t.Run(c.command, func(t *testing.T) {
			if got, r := checkRule(sqlDestructive, c.command, dev); got != c.dev {
				t.Errorf("fora de produção: esperava %q, obtive %q (%q)", c.dev, got, r)
			}
			if got, r := checkRule(sqlDestructive, c.command, prod); got != c.prod {
				t.Errorf("produção: esperava %q, obtive %q (%q)", c.prod, got, r)
			}
		})
	}
}
