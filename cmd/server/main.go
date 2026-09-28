// Ponto de entrada da aplicação: sobe o servidor HTTP e o worker pool, e
// garante que os dois desligam direito quando o processo recebe um sinal de
// término (Ctrl+C ou um `kill`), em vez de simplesmente morrer no meio de
// uma requisição.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/daviwaldow/gopher-links/internal/handlers"
	"github.com/daviwaldow/gopher-links/internal/store"
	"github.com/daviwaldow/gopher-links/internal/worker"
)

const (
	numWorkers     = 4
	jobQueueSize   = 100
	shutdownWindow = 5 * time.Second
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// run existe separado do main() só pra poder devolver um error normal (dá
// pra testar/tratar melhor do que ficar chamando os.Exit direto no meio do
// código).
func run() error {
	// esse contexto é cancelado automaticamente quando o processo recebe
	// SIGINT ou SIGTERM — é o "sinal" que uso pra avisar tanto o worker
	// pool quanto o servidor HTTP que está na hora de desligar.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	s := store.NewMemoryStore()

	pool := worker.NewPool(s, numWorkers, jobQueueSize)
	pool.Start(ctx)

	h := handlers.New(s, pool)

	// Envolvo o roteador com o middleware de CORS pra que o frontend
	// hospedado no Firebase (outro domínio) consiga chamar a API. A origem
	// liberada vem da variável de ambiente ALLOWED_ORIGIN; se não vier,
	// libero geral com "*" (que serve pra rodar/testar localmente).
	allowedOrigin := envOr("ALLOWED_ORIGIN", "*")

	srv := &http.Server{
		Addr:    ":" + envOr("PORT", "8080"),
		Handler: handlers.CORS(allowedOrigin, h.Routes()),
	}

	// o servidor sobe numa goroutine separada porque ListenAndServe é
	// bloqueante — se eu chamasse ele direto aqui, nunca ia chegar no
	// <-ctx.Done() logo abaixo.
	go func() {
		log.Printf("gopher-links rodando em http://localhost%s (origem liberada: %s)", srv.Addr, allowedOrigin)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("erro no servidor: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("encerrando...")

	// dá um tempo limite pras requisições em andamento terminarem antes de
	// forçar o desligamento.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownWindow)
	defer cancel()

	pool.Close()
	return srv.Shutdown(shutdownCtx)
}

// envOr lê uma variável de ambiente e, se ela estiver vazia, devolve o
// valor padrão. Uso isso pra PORT e ALLOWED_ORIGIN porque os serviços de
// hospedagem gratuitos (Render, Cloud Run, etc.) injetam a porta por
// variável de ambiente em vez de deixar eu fixar no código.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
