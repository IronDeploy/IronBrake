package policy

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"go.yaml.in/yaml/v3"

	"github.com/IronDeploy/IronBrake/internal/safefile"
)

// Policy é o conteúdo do .iron/policy.yaml.
type Policy struct {
	ProductionPatterns    []string `yaml:"production_patterns"`
	CriticalResourceTypes []string `yaml:"critical_resource_types"`
	AuditLog              AuditLog `yaml:"audit_log"`
}

// AuditLog personaliza a rotação do log de auditoria. Zero (ou ausente) é o
// padrão. Como o resto do arquivo, só soma: o arquivo pode guardar mais que o
// padrão, nunca menos, senão um projeto de terceiros (ou um agente que edita o
// arquivo) encolheria o rastro de auditoria.
type AuditLog struct {
	MaxSizeMB int `yaml:"max_size_mb"` // tamanho de cada arquivo antes de girar
	Keep      int `yaml:"keep"`        // arquivos antigos guardados
}

const (
	AuditDefaultMaxSizeMB = 5
	AuditDefaultKeep      = 5
	AuditLimit            = 100 // teto de cada campo: o disco não enche por engano
)

// Default é a política embutida, à qual o arquivo sempre soma.
func Default() Policy {
	return Policy{
		ProductionPatterns:    []string{"prod", "production", "prd"},
		CriticalResourceTypes: []string{"aws_db_instance", "aws_rds_cluster", "aws_s3_bucket", "aws_eks_cluster"},
	}
}

const maxPolicySize = 64 << 10

func Path(dir string) string {
	return filepath.Join(dir, ".iron", "policy.yaml")
}

// Load soma a política do projeto em dir à padrão. Arquivo ilegível ou
// inválido devolve a padrão e um erro, que o hook trata como produção.
func Load(dir string) (Policy, error) {
	data, err := safefile.Read(Path(dir), maxPolicySize)
	if errors.Is(err, fs.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return Default(), fmt.Errorf("não consegui ler %s", Path(dir))
	}

	extra, err := parse(data)
	if err != nil {
		return Default(), fmt.Errorf("%s: %w", Path(dir), err)
	}

	p := Default()
	p.ProductionPatterns = append(p.ProductionPatterns, extra.ProductionPatterns...)
	p.CriticalResourceTypes = append(p.CriticalResourceTypes, extra.CriticalResourceTypes...)
	p.AuditLog = extra.AuditLog
	return p, nil
}

// parse é estrito: chave desconhecida (erro de digitação) é erro.
func parse(data []byte) (Policy, error) {
	var p Policy
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&p); err != nil && !errors.Is(err, io.EOF) {
		return Policy{}, errors.New("YAML inválido ou com chave desconhecida")
	}

	for _, pattern := range p.ProductionPatterns {
		if pattern == "" || strings.IndexFunc(pattern, isNotLetter) != -1 {
			return Policy{}, errors.New("production_patterns aceita só palavras de letras (ex.: live)")
		}
	}
	if err := validateAuditLog(p.AuditLog); err != nil {
		return Policy{}, err
	}
	return p, nil
}

func validateAuditLog(a AuditLog) error {
	check := func(name string, value, minimum int) error {
		if value != 0 && (value < minimum || value > AuditLimit) {
			return fmt.Errorf("audit_log.%s vai de %d a %d: o arquivo só soma à política padrão e não reduz o que o log guarda", name, minimum, AuditLimit)
		}
		return nil
	}
	if err := check("max_size_mb", a.MaxSizeMB, AuditDefaultMaxSizeMB); err != nil {
		return err
	}
	return check("keep", a.Keep, AuditDefaultKeep)
}

// IsProduction procura os padrões como palavra inteira, sem diferenciar
// maiúsculas: "prod-eu" e "eks_prd1" batem, "product-api" não.
func (p Policy) IsProduction(texts ...string) bool {
	for _, text := range texts {
		for _, word := range strings.FieldsFunc(strings.ToLower(text), isNotLetter) {
			if slices.ContainsFunc(p.ProductionPatterns, func(pattern string) bool {
				return strings.ToLower(pattern) == word
			}) {
				return true
			}
		}
	}
	return false
}

func (p Policy) IsCritical(resourceType string) bool {
	return slices.Contains(p.CriticalResourceTypes, resourceType)
}

func isNotLetter(r rune) bool {
	return !unicode.IsLetter(r)
}
