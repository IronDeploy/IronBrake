package tfplan

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/IronDeploy/IronBrake/internal/toolpath"
)

const (
	showTimeout = 30 * time.Second
	// o terragrunt pode rodar init e baixar módulos antes do show.
	terragruntShowTimeout = 120 * time.Second
)

// Request diz qual plano ler e com qual ferramenta.
type Request struct {
	Tool     string   // "terraform", "tofu" ou "terragrunt": a mesma do comando
	Cwd      string   // pasta do comando
	Chdir    string   // -chdir (terraform) ou --working-dir (terragrunt)
	PlanFile string   // como veio no comando, relativo à pasta do módulo
	RunAll   []string // terragrunt: {"run-all"} ou {"run", "--all", "--"}; nil = um módulo

	// De onde vem o programa: caminho do ~/.iron/config.yaml (se houver) e as
	// pastas do projeto, dentro das quais um programa do PATH não é aceito.
	Config      toolpath.Config
	ProjectDirs []string
}

// Show roda "<ferramenta> show -json PLANO" e devolve a saída. O terragrunt
// resolve o plano no cache do módulo, do mesmo jeito que o apply resolverá.
// Os erros nunca incluem a saída da ferramenta, que pode conter segredos.
func Show(req Request) ([]byte, error) {
	// Um nome começando com "-" seria lido como opção.
	if req.PlanFile == "" || strings.HasPrefix(req.PlanFile, "-") {
		return nil, errors.New("nome de plano inválido")
	}

	tool, timeout := req.Tool, showTimeout
	if tool == "" {
		tool = "terraform"
	}

	dir := req.Cwd
	var args []string
	switch tool {
	case "terraform", "tofu":
		if req.Chdir != "" {
			args = append(args, "-chdir="+req.Chdir)
		}
	case "terragrunt":
		timeout = terragruntShowTimeout
		args = append(args, req.RunAll...)
		if req.Chdir != "" {
			dir = req.Chdir
			if !filepath.IsAbs(dir) {
				dir = filepath.Join(req.Cwd, dir)
			}
		}
	default:
		return nil, errors.New("ferramenta desconhecida")
	}
	args = append(args, "show", "-json", req.PlanFile)

	program, err := toolpath.Resolve(tool, req.Config, req.ProjectDirs...)
	if err != nil {
		var untrusted *toolpath.UntrustedError
		if errors.As(err, &untrusted) {
			return nil, err
		}
		return nil, errors.New(tool + " não encontrado")
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, program, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TG_NON_INTERACTIVE=true", "TERRAGRUNT_NON_INTERACTIVE=true")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, errors.New(tool + " show demorou demais")
		}
		return nil, errors.New(tool + " show falhou")
	}

	return stdout.Bytes(), nil
}
