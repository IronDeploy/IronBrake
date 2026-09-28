package shield

import "testing"

// TestAllRulesHasNoDuplicates garante que nenhuma regra deny aparece em duas
// categorias — se aparecesse, travar uma categoria destravaria a leitura que
// a outra também deveria proteger de forma inconsistente no status.
func TestAllRulesHasNoDuplicates(t *testing.T) {
	seen := map[string]string{}
	for _, c := range Categories {
		for _, rule := range c.Rules {
			if owner, dup := seen[rule]; dup {
				t.Errorf("regra %q pertence a duas categorias: %q e %q", rule, owner, c.Name)
			}
			seen[rule] = c.Name
		}
	}
}

func TestCategoryWithoutRulesHasWhy(t *testing.T) {
	for _, c := range Categories {
		if len(c.Rules) == 0 && c.Why == "" {
			t.Errorf("categoria %q sem regras precisa explicar por quê (Why)", c.Name)
		}
		if len(c.Rules) > 0 && c.Why != "" {
			t.Errorf("categoria %q tem regras E Why — só devia ter um dos dois", c.Name)
		}
	}
}

func TestForFindsKnownCategory(t *testing.T) {
	c, ok := For("AWS")
	if !ok {
		t.Fatal("esperava achar a categoria AWS")
	}
	if len(c.Rules) == 0 {
		t.Error("AWS deveria ter regras")
	}
}

func TestForUnknownSource(t *testing.T) {
	if _, ok := For("não existe"); ok {
		t.Error("fonte desconhecida não deveria achar categoria")
	}
}

func TestAllRulesNotEmpty(t *testing.T) {
	if len(AllRules()) == 0 {
		t.Fatal("AllRules não deveria vir vazio")
	}
}
