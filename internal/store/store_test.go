package store

import (
	"errors"
	"sync"
	"testing"
)

// testa o fluxo básico: cria um link e busca ele de volta.
func TestCreateAndGet(t *testing.T) {
	s := NewMemoryStore()

	link, err := s.Create("https://example.com")
	if err != nil {
		t.Fatalf("Create devolveu erro: %v", err)
	}
	if link.Status != StatusPending {
		t.Errorf("esperava status %q pra link novo, veio %q", StatusPending, link.Status)
	}

	got, err := s.Get(link.Code)
	if err != nil {
		t.Fatalf("Get devolveu erro: %v", err)
	}
	if got.Destination != "https://example.com" {
		t.Errorf("esperava destino %q, veio %q", "https://example.com", got.Destination)
	}
}

// buscar um código que não existe tem que devolver ErrNotFound, não crashar
// nem devolver um zero value silencioso.
func TestGetNotFound(t *testing.T) {
	s := NewMemoryStore()

	_, err := s.Get("naoexiste")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("esperava ErrNotFound, veio %v", err)
	}
}

// é isso que o worker pool chama depois de checar a URL de destino.
func TestUpdateStatus(t *testing.T) {
	s := NewMemoryStore()
	link, _ := s.Create("https://example.com")

	if err := s.UpdateStatus(link.Code, StatusHealthy, link.CreatedAt); err != nil {
		t.Fatalf("UpdateStatus devolveu erro: %v", err)
	}

	got, _ := s.Get(link.Code)
	if got.Status != StatusHealthy {
		t.Errorf("esperava status %q, veio %q", StatusHealthy, got.Status)
	}
}

func TestIncrementHits(t *testing.T) {
	s := NewMemoryStore()
	link, _ := s.Create("https://example.com")

	for i := 0; i < 3; i++ {
		if err := s.IncrementHits(link.Code); err != nil {
			t.Fatalf("IncrementHits devolveu erro: %v", err)
		}
	}

	got, _ := s.Get(link.Code)
	if got.Hits != 3 {
		t.Errorf("esperava 3 cliques, veio %d", got.Hits)
	}
}

// esse teste é o motivo de eu ter colocado o mutex no MemoryStore: várias
// goroutines mexendo no mesmo link ao mesmo tempo. Rodar com `go test -race`
// prova que não tem data race escondida.
func TestConcurrentAccess(t *testing.T) {
	s := NewMemoryStore()
	link, _ := s.Create("https://example.com")

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = s.IncrementHits(link.Code)
		}()
		go func() {
			defer wg.Done()
			_, _ = s.Get(link.Code)
		}()
	}
	wg.Wait()

	got, _ := s.Get(link.Code)
	if got.Hits != 100 {
		t.Errorf("esperava 100 cliques depois dos incrementos concorrentes, veio %d", got.Hits)
	}
}
