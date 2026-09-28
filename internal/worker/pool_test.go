package worker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/daviwaldow/gopher-links/internal/store"
)

// sobe um servidor de teste que responde OK e confere que o pool marca o
// link como "healthy" depois de processar o job.
func TestPoolMarksReachableURLHealthy(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	s := store.NewMemoryStore()
	link, _ := s.Create(upstream.URL)

	pool := NewPool(s, 2, 10, WithCheckTimeout(2*time.Second))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)

	pool.Submit(Job{Code: link.Code, Destination: link.Destination})

	waitForStatus(t, s, link.Code, store.StatusHealthy)
}

// porta 0 em localhost nunca aceita conexão, então serve pra simular uma
// URL fora do ar sem depender de internet no teste.
func TestPoolMarksUnreachableURLUnhealthy(t *testing.T) {
	s := store.NewMemoryStore()
	link, _ := s.Create("http://127.0.0.1:0")

	pool := NewPool(s, 2, 10, WithCheckTimeout(1*time.Second))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)

	pool.Submit(Job{Code: link.Code, Destination: link.Destination})

	waitForStatus(t, s, link.Code, store.StatusUnhealthy)
}

// como a checagem roda em outra goroutine, não dá pra simplesmente checar o
// status logo depois do Submit — tenho que dar um tempo pro worker
// processar. Esse helper faz polling até o status mudar ou estourar o prazo.
func waitForStatus(t *testing.T, s store.Store, code string, want store.Status) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		link, err := s.Get(code)
		if err != nil {
			t.Fatalf("Get devolveu erro: %v", err)
		}
		if link.Status == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("esperei demais pelo status %q e não chegou", want)
}
