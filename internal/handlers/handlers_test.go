package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/daviwaldow/gopher-links/internal/store"
	"github.com/daviwaldow/gopher-links/internal/worker"
)

// monta um Handler de teste com store real e um worker pool real (mas cujo
// resultado eu não preciso conferir aqui — isso já é testado em
// internal/worker). O importante é o pool não travar nem dar panic quando
// o handler chama Submit.
func newTestHandler(t *testing.T) *Handler {
	t.Helper()
	s := store.NewMemoryStore()
	pool := worker.NewPool(s, 1, 10)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	pool.Start(ctx)
	return New(s, pool)
}

func TestShortenAndRedirect(t *testing.T) {
	h := newTestHandler(t)
	mux := h.Routes()

	body := strings.NewReader(`{"url": "https://example.com"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/shorten", body)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("esperava status 201, veio %d: %s", rec.Code, rec.Body.String())
	}

	var resp linkResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("falha ao decodificar resposta: %v", err)
	}
	if resp.Code == "" {
		t.Fatal("esperava um código curto não vazio")
	}

	redirectReq := httptest.NewRequest(http.MethodGet, "/"+resp.Code, nil)
	redirectRec := httptest.NewRecorder()
	mux.ServeHTTP(redirectRec, redirectReq)

	if redirectRec.Code != http.StatusFound {
		t.Fatalf("esperava status 302, veio %d", redirectRec.Code)
	}
	if loc := redirectRec.Header().Get("Location"); loc != "https://example.com" {
		t.Errorf("esperava redirect pra https://example.com, veio %q", loc)
	}
}

func TestShortenRejectsInvalidURL(t *testing.T) {
	h := newTestHandler(t)
	mux := h.Routes()

	body := strings.NewReader(`{"url": "isso-nao-e-url"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/shorten", body)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("esperava status 400, veio %d", rec.Code)
	}
}

func TestRedirectUnknownCodeReturns404(t *testing.T) {
	h := newTestHandler(t)
	mux := h.Routes()

	req := httptest.NewRequest(http.MethodGet, "/naoexiste", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("esperava status 404, veio %d", rec.Code)
	}
}

func TestListLinks(t *testing.T) {
	h := newTestHandler(t)
	mux := h.Routes()

	for _, u := range []string{"https://example.com", "https://golang.org"} {
		body := strings.NewReader(`{"url": "` + u + `"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/shorten", body)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/links", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	var links []linkResponse
	if err := json.NewDecoder(rec.Body).Decode(&links); err != nil {
		t.Fatalf("falha ao decodificar resposta: %v", err)
	}
	if len(links) != 2 {
		t.Fatalf("esperava 2 links, veio %d", len(links))
	}
}

// a página inicial do painel visual tem que responder 200 e devolver HTML,
// mesmo sem nenhum link cadastrado ainda.
func TestIndexPageServesHTML(t *testing.T) {
	h := newTestHandler(t)
	mux := h.Routes()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("esperava status 200, veio %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("esperava Content-Type text/html, veio %q", ct)
	}
}
