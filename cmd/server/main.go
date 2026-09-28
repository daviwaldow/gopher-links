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
	"strconv"
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

	// Escolho o armazenamento pela variável DATABASE_URL: se ela existir, uso
	// Postgres (os links persistem entre reinícios); senão, caio no store em
	// memória, que é o suficiente pra rodar/testar localmente. Como os dois
	// implementam a interface store.Store, o resto do código nem percebe.
	var s store.Store
	if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
		ps, err := store.NewPostgresStore(ctx, dsn)
		if err != nil {
			return err
		}
		defer ps.Close()
		s = ps
		log.Println("armazenamento: postgres")
	} else {
		s = store.NewMemoryStore()
		log.Println("armazenamento: memória (defina DATABASE_URL pra persistir os links)")
	}

	pool := worker.NewPool(s, numWorkers, jobQueueSize)
	pool.Start(ctx)

	// Config de expiração e rate limiting, via ambiente:
	// - LINK_TTL_DAYS: links expiram após N dias (0/vazio = nunca).
	// - RATE_LIMIT_PER_MIN: máximo de links que um IP cria por minuto (0 = sem limite).
	linkTTL := time.Duration(envInt("LINK_TTL_DAYS", 0)) * 24 * time.Hour
	rateLimitPerMin := envInt("RATE_LIMIT_PER_MIN", 30)

	// se há TTL, sobe a rotina que apaga links expirados de tempos em tempos.
	if linkTTL > 0 {
		go runJanitor(ctx, s, linkTTL)
	}

	h := handlers.New(s, pool, linkTTL, rateLimitPerMin)

	// Envolvo o roteador com o middleware de CORS pra que o frontend
	// hospedado no Firebase (outro domínio) consiga chamar a API. As origens
	// liberadas vêm da variável ALLOWED_ORIGIN — pode ser uma origem, uma
	// lista separada por vírgula, ou "*". Se não vier, libero geral com "*"
	// (que serve pra rodar/testar localmente, onde o Go serve tudo junto).
	allowedOrigin := envOr("ALLOWED_ORIGIN", "*")

	// Timeouts explícitos: sem eles, uma conexão lenta ou maliciosa pode
	// ficar segurando um socket indefinidamente (ataque tipo slowloris). Os
	// valores são folgados o suficiente pra uso normal desta API.
	srv := &http.Server{
		Addr:              ":" + envOr("PORT", "8080"),
		Handler:           handlers.SecurityHeaders(handlers.CORS(allowedOrigin, h.Routes())),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
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

// envInt lê uma variável de ambiente como inteiro; se estiver vazia ou não
// for um número, devolve o valor padrão.
func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

// runJanitor roda em background e, a cada hora, apaga os links que já passaram
// do TTL. Para quando o contexto é cancelado (shutdown).
func runJanitor(ctx context.Context, s store.Store, ttl time.Duration) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := s.DeleteExpired(time.Now().Add(-ttl))
			if err != nil {
				log.Printf("limpeza de links expirados falhou: %v", err)
				continue
			}
			if n > 0 {
				log.Printf("limpeza: %d link(s) expirado(s) removido(s)", n)
			}
		}
	}
}
