package dialog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/IronDeploy/IronBrake/internal/testutil/fakesh"
)

// No Windows o programa falso é uma cópia deste binário de teste.
func TestMain(m *testing.M) {
	fakesh.MaybeRun()
	os.Exit(m.Run())
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func envOf(vars map[string]string) func(string) string {
	return func(key string) string { return vars[key] }
}

func TestHasDisplay(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"X11", map[string]string{"DISPLAY": ":0"}, true},
		{"Wayland", map[string]string{"WAYLAND_DISPLAY": "wayland-0"}, true},
		{"os dois", map[string]string{"DISPLAY": ":0", "WAYLAND_DISPLAY": "wayland-0"}, true},
		{"SSH ou CI: nenhum", map[string]string{}, false},
		{"variáveis vazias", map[string]string{"DISPLAY": "", "WAYLAND_DISPLAY": ""}, false},
	}
	for _, c := range cases {
		if got := hasDisplay(envOf(c.env)); got != c.want {
			t.Errorf("%s: esperava %v, obtive %v", c.name, c.want, got)
		}
	}
}

func TestZenityArgsKeepMessageInOneArgument(t *testing.T) {
	// Nomes de recursos vêm do .tf, que o agente escreve: tudo isto precisa
	// chegar como dado, num único argumento, nunca como opção nem como shell.
	hostile := []string{
		"--help",
		"--ok-label=Aprovar --default-ok",
		"; rm -rf / ;",
		"$(touch /tmp/pwned) `id`",
		"<b>negrito</b> <span foreground=\"red\">x</span> &amp;",
		"linha 1\nlinha 2\r\nlinha 3",
		"aws_s3_bucket.prod[\"x'y\"]",
		"acentuação: ção, ñ, 日本語, emoji 🔥",
		"",
	}
	for _, message := range hostile {
		args := zenityArgs(message, 480*time.Second)

		// A mensagem aparece uma vez, colada ao --text=, e só ali.
		found := 0
		for _, a := range args {
			if a == "--text="+limitMessage(message) {
				found++
			} else if strings.Contains(a, "touch") || strings.Contains(a, "rm -rf") || a == "--help" {
				t.Errorf("a mensagem vazou para outro argumento: %q", a)
			}
		}
		if found != 1 {
			t.Errorf("mensagem %q deveria ir em exatamente um --text=, achei %d", message, found)
		}
	}
}

func TestZenityArgsSafeDefaults(t *testing.T) {
	args := zenityArgs("x", 45*time.Second)
	for _, want := range []string{
		"--question",
		"--no-markup",      // <b>, <span> e & do .tf não viram formatação
		"--default-cancel", // Enter cancela
		"--ok-label=Executar",
		"--cancel-label=Cancelar",
		"--timeout=45",
		"--title=Iron Brake",
	} {
		found := false
		for _, a := range args {
			found = found || a == want
		}
		if !found {
			t.Errorf("faltou %s em %v", want, args)
		}
	}
	for _, a := range args {
		if strings.Contains(a, "--default-ok") || a == "--ok-label=Cancelar" {
			t.Errorf("argumento que inverte o padrão: %q", a)
		}
	}
}

func TestLimitMessageKeepsTheWindowOnScreen(t *testing.T) {
	// O zenity cresce com o texto e, passando da tela, os botões somem.
	short := "IRON BRAKE\nApagados:\n- aws_s3_bucket.a (apagar)"
	if limitMessage(short) != short {
		t.Error("texto curto não deve ser cortado")
	}

	manyLines := strings.Repeat("recurso.x (apagar)\n", 200)
	got := limitMessage(manyLines)
	if n := strings.Count(got, "\n") + 1; n > maxLinuxLines+1 {
		t.Errorf("deveria ter no máximo %d linhas (+ a marca), tem %d", maxLinuxLines, n)
	}
	if !strings.HasSuffix(got, "…") {
		t.Error("o corte precisa ser marcado com …")
	}

	long := strings.Repeat("ç", maxLinuxRunes+500)
	got = limitMessage(long)
	if r := []rune(got); len(r) != maxLinuxRunes+1 {
		t.Errorf("deveria cortar em %d caracteres + a marca: %d", maxLinuxRunes, len(r))
	}
	if !strings.HasPrefix(got, strings.Repeat("ç", maxLinuxRunes)) {
		t.Error("corte no meio de um caractere")
	}
}

func TestZenityPathIsAbsoluteSystemOne(t *testing.T) {
	// Caminho absoluto: um zenity falso no PATH aprovaria tudo sozinho.
	if zenityPath != "/usr/bin/zenity" {
		t.Errorf("esperava /usr/bin/zenity, obtive %q", zenityPath)
	}
}

func TestZenityAnswer(t *testing.T) {
	// Códigos observados com o zenity real (3.44 e 4.1) sob Xvfb: Executar 0;
	// Cancelar, Enter, Esc e fechar a janela 1; tempo esgotado 5. Sem display
	// o zenity também sai com 1, mas escreve sobre o display no stderr.
	cases := []struct {
		name   string
		code   int
		stderr string
		want   Answer
	}{
		{"Executar", 0, "", Approved},
		{"Executar com ruído no stderr", 0, "Gtk-WARNING: a11y bus", Approved},
		{"Cancelar, Enter, Esc ou fechar", 1, "", Rejected},
		{"Cancelar com ruído do GTK", 1, "Gtk-WARNING **: Unable to acquire the address of the accessibility bus", Rejected},
		{"tempo esgotado", 5, "", Rejected},
		{"sem display", 1, "Gtk-WARNING **: Failed to open display", Unavailable},
		{"sem display (3.x)", 1, "Gtk-WARNING **: cannot open display: ", Unavailable},
		// Qualquer outro código é falha do programa, não resposta.
		{"opção desconhecida", 255, "", Unavailable},
		{"abortou", 134, "", Unavailable},
		{"morto por sinal", -1, "", Unavailable},
		{"código inesperado", 2, "", Unavailable},
	}
	for _, c := range cases {
		if got := zenityAnswer(c.code, c.stderr); got != c.want {
			t.Errorf("%s: esperava %v, obtive %v", c.name, c.want, got)
		}
	}
}

// fakeZenity instala um programa falso que sai com o código pedido.
func fakeZenity(t *testing.T, script string) string {
	t.Helper()
	return fakesh.Install(t, filepath.Join(t.TempDir(), "zenity"), script)
}

func TestConfirmLinuxWithFakeProgram(t *testing.T) {
	display := envOf(map[string]string{"DISPLAY": ":0"})
	cases := []struct {
		name   string
		script string
		want   Answer
	}{
		{"Executar", "#!/bin/sh\nexit 0\n", Approved},
		{"Cancelar", "#!/bin/sh\nexit 1\n", Rejected},
		{"tempo esgotado do zenity", "#!/bin/sh\nexit 5\n", Rejected},
		{"sem display de verdade", "#!/bin/sh\necho 'Gtk-WARNING **: Failed to open display' >&2\nexit 1\n", Unavailable},
		{"programa falhou", "#!/bin/sh\nexit 255\n", Unavailable},
	}
	for _, c := range cases {
		program := fakeZenity(t, c.script)
		if got := confirmLinux(program, "motivo", 30*time.Second, display); got != c.want {
			t.Errorf("%s: esperava %v, obtive %v", c.name, c.want, got)
		}
	}
}

func TestConfirmLinuxWithoutDisplayNeverRunsTheProgram(t *testing.T) {
	// Um programa que aprovaria tudo, e que deixaria rastro se rodasse.
	dir := t.TempDir()
	marker := filepath.Join(dir, "rodou")
	program := fakesh.Install(t, filepath.Join(dir, "zenity"), "#!/bin/sh\ntouch "+marker+"\nexit 0\n")

	for _, env := range []map[string]string{{}, {"DISPLAY": ""}} {
		if got := confirmLinux(program, "motivo", 30*time.Second, envOf(env)); got != Unavailable {
			t.Errorf("sem display (%v): esperava Unavailable, obtive %v", env, got)
		}
	}
	if fileExists(marker) {
		t.Error("sem display o programa não pode nem ser executado")
	}
}

func TestConfirmLinuxMissingProgramIsUnavailable(t *testing.T) {
	display := envOf(map[string]string{"DISPLAY": ":0"})
	missing := filepath.Join(t.TempDir(), "nao-existe")
	if got := confirmLinux(missing, "motivo", 30*time.Second, display); got != Unavailable {
		t.Errorf("sem o zenity instalado: esperava Unavailable, obtive %v", got)
	}
}

func TestConfirmLinuxGivesUpAsRejected(t *testing.T) {
	// O zenity travado (sem responder nem respeitar o --timeout) não pode
	// segurar o agente: passando do prazo, é recusa, nunca aprovação.
	display := envOf(map[string]string{"DISPLAY": ":0"})
	program := fakeZenity(t, "#!/bin/sh\nexec sleep 30\n")

	old := killMargin
	killMargin = 0
	defer func() { killMargin = old }()

	start := time.Now()
	if got := confirmLinux(program, "motivo", time.Second, display); got != Rejected {
		t.Errorf("esperava Rejected, obtive %v", got)
	}
	if time.Since(start) > 10*time.Second {
		t.Error("deveria desistir perto do prazo")
	}
}
