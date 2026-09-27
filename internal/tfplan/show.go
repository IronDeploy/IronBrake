package tfplan

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"
)

const showTimeout = 30 * time.Second

// Show roda "terraform [-chdir=DIR] show -json PLANO" em cwd. Os erros nunca
// incluem a saída do terraform, que pode conter segredos.
func Show(cwd, chdir, planFile string) ([]byte, error) {
	// Um nome começando com "-" seria lido como opção.
	if planFile == "" || strings.HasPrefix(planFile, "-") {
		return nil, errors.New("nome de plano inválido")
	}

	args := []string{"show", "-json", planFile}
	if chdir != "" {
		args = append([]string{"-chdir=" + chdir}, args...)
	}

	ctx, cancel := context.WithTimeout(context.Background(), showTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "terraform", args...)
	cmd.Dir = cwd
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, errors.New("terraform show demorou demais")
		}
		return nil, errors.New("terraform show falhou")
	}

	return stdout.Bytes(), nil
}
