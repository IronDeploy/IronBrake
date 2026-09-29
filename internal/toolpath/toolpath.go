package toolpath

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/IronDeploy/IronBrake/internal/safefile"
)

// Tools são os programas que o Iron Brake executa por conta própria.
var Tools = []string{"terraform", "tofu", "terragrunt"}

// Config é o ~/.iron/config.yaml: configuração do usuário, fora de qualquer
// projeto. Fica aqui, e não no .iron/policy.yaml, de propósito: o hook executa
// o programa indicado, e o policy.yaml vem do repositório (ou de um agente
// que o edita). Um arquivo de projeto não pode escolher o que roda na sua máquina.
type Config struct {
	Tools map[string]string `yaml:"tools"` // nome → caminho absoluto
}

const maxConfigSize = 64 << 10

// ConfigPath é ~/.iron/config.yaml. Vazio se não houver pasta do usuário.
func ConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".iron", "config.yaml")
}

// ErrConfig marca um config.yaml ilegível ou inválido.
var ErrConfig = errors.New("~/.iron/config.yaml inválido")

// Load lê o arquivo. Ausente = configuração vazia. Chave desconhecida, programa
// que não está em Tools ou caminho que não é absoluto é erro.
func Load(path string) (Config, error) {
	if path == "" {
		return Config{}, nil
	}
	data, err := safefile.Read(path, maxConfigSize)
	if errors.Is(err, fs.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("%w: não consegui ler", ErrConfig)
	}

	var c Config
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&c); err != nil && !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("%w: YAML inválido ou com chave desconhecida", ErrConfig)
	}
	for name, p := range c.Tools {
		if !contains(Tools, name) {
			return Config{}, fmt.Errorf("%w: tools.%s desconhecido (use %s)", ErrConfig, name, strings.Join(Tools, ", "))
		}
		if !filepath.IsAbs(p) {
			return Config{}, fmt.Errorf("%w: tools.%s precisa ser um caminho absoluto", ErrConfig, name)
		}
	}
	return c, nil
}

// UntrustedError diz por que um programa não foi usado. Nunca carrega saída
// do programa.
type UntrustedError struct {
	Tool string
	Path string
	Why  string
}

func (e *UntrustedError) Error() string {
	return fmt.Sprintf("%s em %s não é confiável: %s", e.Tool, e.Path, e.Why)
}

// Resolve devolve o caminho real do programa a executar.
//
//   - Com o caminho configurado em cfg.Tools: só confere que é um arquivo
//     executável e que ninguém mais pode escrever nele.
//   - Sem configuração: procura no PATH e recusa o que estiver dentro de
//     projectDirs (um ./bin/terraform do repositório) ou puder ser trocado por
//     qualquer usuário (arquivo ou pasta gravável por "outros").
//
// Devolve o caminho com os links resolvidos, para o que foi conferido ser
// exatamente o que roda.
func Resolve(tool string, cfg Config, projectDirs ...string) (string, error) {
	if configured := cfg.Tools[tool]; configured != "" {
		real, err := checkFile(tool, configured)
		if err != nil {
			return "", err
		}
		return real, nil
	}

	found, err := exec.LookPath(tool)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(found) {
		found, err = filepath.Abs(found)
		if err != nil {
			return "", err
		}
	}
	real, err := checkFile(tool, found)
	if err != nil {
		return "", err
	}
	home, _ := os.UserHomeDir()
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		home = resolved
	}
	for _, dir := range projectDirs {
		if dir == "" {
			continue
		}
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			dir = resolved
		}
		// Projeto aberto na home (ou na raiz) contém todas as ferramentas do
		// usuário, como ~/bin e ~/go/bin: aí a regra não diz nada.
		if home != "" && inside(dir, home) {
			continue
		}
		if inside(dir, real) {
			return "", &UntrustedError{tool, real, "está dentro da pasta do projeto"}
		}
	}
	return real, nil
}

// checkFile resolve os links e exige um arquivo comum, executável, que só o
// dono (ou o administrador) possa alterar, numa pasta que também não seja
// gravável por outros.
func checkFile(tool, path string) (string, error) {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", &UntrustedError{tool, path, "não existe ou não dá para resolver"}
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", &UntrustedError{tool, real, "não existe"}
	}
	if !info.Mode().IsRegular() {
		return "", &UntrustedError{tool, real, "não é um arquivo comum"}
	}
	if runtime.GOOS == "windows" {
		return real, nil // as permissões do Windows não são bits de modo
	}
	if info.Mode().Perm()&0o111 == 0 {
		return "", &UntrustedError{tool, real, "não tem permissão de execução"}
	}
	if info.Mode().Perm()&0o002 != 0 {
		return "", &UntrustedError{tool, real, "qualquer usuário pode alterar o arquivo"}
	}
	if dirInfo, err := os.Stat(filepath.Dir(real)); err == nil && dirInfo.Mode().Perm()&0o002 != 0 {
		return "", &UntrustedError{tool, real, "qualquer usuário pode trocar o arquivo (a pasta é gravável por todos)"}
	}
	return real, nil
}

// inside diz se path está em dir (ou é o próprio dir).
func inside(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
