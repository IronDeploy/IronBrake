// Package session dá memória ao hook, que é um processo novo a cada comando:
// guarda por sessão só hashes de comandos e contadores, para detectar agentes
// em loop.
package session
