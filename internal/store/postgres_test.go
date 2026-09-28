package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

// TestPostgresStore roda um ciclo completo (criar, ler, atualizar status,
// contar clique, listar) contra um Postgres de verdade. Só roda se a variável
// TEST_DATABASE_URL apontar pra um banco de TESTE — na CI (sem essa variável)
// o teste é pulado, então não é preciso um banco pra a CI ficar verde.
//
// ATENÇÃO: use um banco descartável. O teste escreve e apaga linhas na
// tabela "links".
func TestPostgresStore(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("defina TEST_DATABASE_URL (banco de teste) pra rodar este teste")
	}

	ctx := context.Background()
	s, err := NewPostgresStore(ctx, dsn)
	if err != nil {
		t.Fatalf("conectando no postgres: %v", err)
	}
	defer s.Close()

	link, err := s.Create("https://example.com")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// apaga a linha criada no fim do teste, aconteça o que acontecer.
	t.Cleanup(func() {
		_, _ = s.pool.Exec(context.Background(), "DELETE FROM links WHERE code = $1", link.Code)
	})

	if link.Code == "" {
		t.Fatal("esperava um código não vazio")
	}
	if link.Status != StatusPending {
		t.Errorf("esperava status pending na criação, veio %q", link.Status)
	}

	// lê de volta
	got, err := s.Get(link.Code)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Destination != "https://example.com" {
		t.Errorf("destino errado: %q", got.Destination)
	}

	// atualiza o status (o que o worker faz)
	if err := s.UpdateStatus(link.Code, StatusHealthy, time.Now()); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	if got, _ = s.Get(link.Code); got.Status != StatusHealthy {
		t.Errorf("esperava healthy apos UpdateStatus, veio %q", got.Status)
	}

	// conta um clique
	if err := s.IncrementHits(link.Code); err != nil {
		t.Fatalf("IncrementHits: %v", err)
	}
	if got, _ = s.Get(link.Code); got.Hits != 1 {
		t.Errorf("esperava 1 clique, veio %d", got.Hits)
	}

	// o link deve aparecer em All()
	found := false
	for _, l := range s.All() {
		if l.Code == link.Code {
			found = true
			break
		}
	}
	if !found {
		t.Error("o link criado não apareceu em All()")
	}

	// código inexistente devolve ErrNotFound
	if _, err := s.Get("naoexiste_xyz"); !errors.Is(err, ErrNotFound) {
		t.Errorf("esperava ErrNotFound pra código inexistente, veio %v", err)
	}
}
