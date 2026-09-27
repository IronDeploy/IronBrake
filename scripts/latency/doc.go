// Command latency mede a latência do hook como o Claude Code a sente: roda o
// binário N vezes por caso, um processo novo a cada vez, e mostra média, p50,
// p95 e máximo.
//
//	go run ./scripts/latency -bin bin/iron -n 200 -tfdir /pasta/com/create.tfplan
package main
