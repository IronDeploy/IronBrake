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
		{`psql -c "TRUNCATE users, orders"`, hook.Ask, hook.Deny},
		{`mysql -e "DELETE FROM users"`, hook.Ask, hook.Deny},
		{`sqlite3 app.db "delete from sessions;"`, hook.Ask, hook.Deny},
		{`psql -c "BEGIN; DROP TABLE logs; COMMIT;"`, hook.Ask, hook.Deny},
		{`echo "DELETE FROM users;" | psql`, hook.Ask, hook.Deny},
		{`docker exec db psql -U app -c "DROP TABLE users"`, hook.Ask, hook.Deny},
		{"psql <<EOF\nDELETE FROM users;\nEOF", hook.Ask, hook.Deny},

		// Parecidos e inofensivos: allow em qualquer ambiente.
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
