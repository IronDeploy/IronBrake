package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"time"
)

type latencyCase struct {
	name     string
	command  string
	cwd      string
	wantCode int
	external bool // chama o terraform show
}

func main() {
	bin := flag.String("bin", "bin/iron", "binário do iron")
	runs := flag.Int("n", 200, "execuções por caso")
	tfdir := flag.String("tfdir", "", "pasta com terraform init feito e um create.tfplan (sem ela, o caso do terraform show é pulado)")
	flag.Parse()

	cases := []latencyCase{
		{"git status (allow)", "git status", "/srv/loja", 0, false},
		{"git push --force (deny)", "git push --force origin main", "/srv/loja", 2, false},
		{"kubectl delete ns prod (deny)", "kubectl delete namespace prod", "/srv/loja", 2, false},
		{"kubectl get pods (allow, grava sessão)", "kubectl get pods", "/srv/loja", 0, false},
		{"terraform apply sem plano (deny)", "terraform apply -auto-approve", "/srv/loja", 2, false},
	}
	if *tfdir != "" {
		cases = append(cases, latencyCase{"terraform apply create.tfplan (terraform show)", "terraform apply create.tfplan", *tfdir, 0, true})
	}

	// HOME temporária: sem o estado e o kubeconfig de quem roda.
	home, err := os.MkdirTemp("", "iron-latency-")
	if err != nil {
		fail(err)
	}
	defer os.RemoveAll(home)

	fmt.Printf("%d execuções por caso (+5 de aquecimento, fora da conta)\n\n", *runs)
	fmt.Printf("%-48s %8s %8s %8s %8s\n", "caso", "média", "p50", "p95", "máximo")

	var internal []time.Duration
	for _, c := range cases {
		durations := make([]time.Duration, 0, *runs)
		for i := range *runs + 5 {
			d := runOnce(*bin, home, c, i)
			if i >= 5 {
				durations = append(durations, d)
			}
		}
		if !c.external {
			internal = append(internal, durations...)
		}
		printRow(c.name, durations)
	}
	fmt.Println()
	printRow("TODOS sem programa externo", internal)
}

func runOnce(bin, home string, c latencyCase, i int) time.Duration {
	event := `{"session_id":"latency-` + strconv.Itoa(i) + `","tool_name":"Bash","cwd":` +
		strconv.Quote(c.cwd) + `,"tool_input":{"command":` + strconv.Quote(c.command) + `}}`

	cmd := exec.Command(bin, "hook")
	cmd.Stdin = bytes.NewBufferString(event)
	cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}

	start := time.Now()
	err := cmd.Run()
	elapsed := time.Since(start)

	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		fail(err)
	}
	if code != c.wantCode {
		fail(fmt.Errorf("%s: esperava código %d, obtive %d", c.name, c.wantCode, code))
	}
	return elapsed
}

func printRow(name string, durations []time.Duration) {
	sorted := slices.Clone(durations)
	slices.Sort(sorted)

	var total time.Duration
	for _, d := range sorted {
		total += d
	}
	mean := total / time.Duration(len(sorted))

	fmt.Printf("%-48s %8s %8s %8s %8s\n", name,
		ms(mean), ms(percentile(sorted, 50)), ms(percentile(sorted, 95)), ms(sorted[len(sorted)-1]))
}

// percentile: p95 de 200 medidas ordenadas é a 190ª.
func percentile(sorted []time.Duration, p int) time.Duration {
	index := (len(sorted)*p+99)/100 - 1
	return sorted[index]
}

func ms(d time.Duration) string {
	return fmt.Sprintf("%.1fms", float64(d.Microseconds())/1000)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "latency:", err)
	os.Exit(1)
}
