package tfplan

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

type Summary struct {
	Create  int
	Update  int
	Delete  int
	Replace int

	Destructive []Resource // apagados ou substituídos
	Changes     []Resource // todos os alterados
}

func (s Summary) Changed() int {
	return s.Create + s.Update + s.Delete + s.Replace
}

// Resource não carrega atributos: eles podem conter segredos.
type Resource struct {
	Address string
	Type    string
	Action  string // "create", "update", "delete" ou "replace"
}

type plan struct {
	FormatVersion   string           `json:"format_version"`
	ResourceChanges []resourceChange `json:"resource_changes"`
}

type resourceChange struct {
	Address string `json:"address"`
	Type    string `json:"type"`
	Change  struct {
		Actions []string `json:"actions"`
	} `json:"change"`
}

// Summarize conta as ações do plano. O que não entende vira erro, nunca um
// resumo vazio.
func Summarize(data []byte) (Summary, error) {
	var p plan
	if err := json.Unmarshal(data, &p); err != nil {
		return Summary{}, fmt.Errorf("JSON do plano inválido: %w", err)
	}
	if p.FormatVersion == "" {
		return Summary{}, errors.New("não parece um plano do terraform: falta format_version")
	}

	var s Summary
	for _, rc := range p.ResourceChanges {
		actions := rc.Change.Actions
		switch {
		case slices.Equal(actions, []string{"no-op"}), slices.Equal(actions, []string{"read"}):
		case slices.Equal(actions, []string{"create"}):
			s.Create++
			s.Changes = append(s.Changes, Resource{sanitizeAddress(rc.Address), rc.Type, "create"})
		case slices.Equal(actions, []string{"update"}):
			s.Update++
			s.Changes = append(s.Changes, Resource{sanitizeAddress(rc.Address), rc.Type, "update"})
		case slices.Equal(actions, []string{"delete"}):
			s.Delete++
			r := Resource{sanitizeAddress(rc.Address), rc.Type, "delete"}
			s.Destructive = append(s.Destructive, r)
			s.Changes = append(s.Changes, r)
		case slices.Equal(actions, []string{"delete", "create"}), slices.Equal(actions, []string{"create", "delete"}):
			s.Replace++
			r := Resource{sanitizeAddress(rc.Address), rc.Type, "replace"}
			s.Destructive = append(s.Destructive, r)
			s.Changes = append(s.Changes, r)
		default:
			return Summary{}, fmt.Errorf("ações desconhecidas em %s: %v", rc.Address, actions)
		}
	}

	return s, nil
}

const (
	maxAddressLength = 200
	maxKeyLength     = 40
)

var addressKey = regexp.MustCompile(`\["((?:[^"\\]|\\.)*)"\]`)

// sanitizeAddress limpa o endereço antes que ele chegue ao Claude, à janela
// ou ao log: chaves de for_each são texto livre e poderiam levar instruções
// (prompt injection) ou caracteres de controle.
func sanitizeAddress(address string) string {
	address = addressKey.ReplaceAllStringFunc(address, func(match string) string {
		key := match[2 : len(match)-2]
		if len(key) > maxKeyLength || strings.IndexFunc(key, func(r rune) bool { return !isKeyChar(r) }) >= 0 {
			return `["…"]`
		}
		return match
	})
	address = strings.Map(func(r rune) rune {
		if isKeyChar(r) || strings.ContainsRune(`[]"…`, r) {
			return r
		}
		return '?'
	}, address)
	if runes := []rune(address); len(runes) > maxAddressLength {
		return string(runes[:maxAddressLength-1]) + "…"
	}
	return address
}

func isKeyChar(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_.:/@+-", r)
}
