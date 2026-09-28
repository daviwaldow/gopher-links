package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	return New(s, pool, 0, 0) // sem TTL e sem rate limit nos testes
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

// newTestHandlerOpts monta um Handler com TTL e rate limit configuráveis,
// pros testes das features que dependem disso.
func newTestHandlerOpts(t *testing.T, ttl time.Duration, rate int) (*Handler, store.Store) {
	t.Helper()
	s := store.NewMemoryStore()
	pool := worker.NewPool(s, 1, 10)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	pool.Start(ctx)
	return New(s, pool, ttl, rate), s
}

func postShorten(mux *http.ServeMux, jsonBody string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/shorten", strings.NewReader(jsonBody))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestShortenWithCustomAlias(t *testing.T) {
	h := newTestHandler(t)
	mux := h.Routes()

	rec := postShorten(mux, `{"url":"https://example.com","alias":"meu-link"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("esperava 201, veio %d: %s", rec.Code, rec.Body.String())
	}
	var resp linkResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Code != "meu-link" {
		t.Errorf("esperava code 'meu-link', veio %q", resp.Code)
	}

	// o redirect pelo alias tem que funcionar
	rreq := httptest.NewRequest(http.MethodGet, "/meu-link", nil)
	rrec := httptest.NewRecorder()
	mux.ServeHTTP(rrec, rreq)
	if rrec.Code != http.StatusFound {
		t.Errorf("esperava 302 no alias, veio %d", rrec.Code)
	}
}

func TestShortenRejectsInvalidAlias(t *testing.T) {
	h := newTestHandler(t)
	mux := h.Routes()

	rec := postShorten(mux, `{"url":"https://example.com","alias":"tem espaço"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("esperava 400 pra alias inválido, veio %d", rec.Code)
	}
}

func TestShortenDuplicateAliasConflicts(t *testing.T) {
	h := newTestHandler(t)
	mux := h.Routes()

	if rec := postShorten(mux, `{"url":"https://example.com","alias":"repetido"}`); rec.Code != http.StatusCreated {
		t.Fatalf("primeira criação: esperava 201, veio %d", rec.Code)
	}
	if rec := postShorten(mux, `{"url":"https://outro.com","alias":"repetido"}`); rec.Code != http.StatusConflict {
		t.Errorf("alias repetido: esperava 409, veio %d", rec.Code)
	}
}

func TestExpiredLinkReturnsGone(t *testing.T) {
	h, s := newTestHandlerOpts(t, time.Millisecond, 0) // expira 1ms após criado
	mux := h.Routes()

	link, _ := s.Create("https://example.com")
	time.Sleep(5 * time.Millisecond) // garante que o link já passou do TTL

	req := httptest.NewRequest(http.MethodGet, "/"+link.Code, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusGone {
		t.Errorf("esperava 410 pra link expirado, veio %d", rec.Code)
	}
}

func TestRateLimitBlocksExcess(t *testing.T) {
	h, _ := newTestHandlerOpts(t, 0, 2) // no máximo 2 por minuto por IP
	mux := h.Routes()

	if rec := postShorten(mux, `{"url":"https://example.com"}`); rec.Code != http.StatusCreated {
		t.Fatalf("1a criação: esperava 201, veio %d", rec.Code)
	}
	if rec := postShorten(mux, `{"url":"https://example.com"}`); rec.Code != http.StatusCreated {
		t.Fatalf("2a criação: esperava 201, veio %d", rec.Code)
	}
	if rec := postShorten(mux, `{"url":"https://example.com"}`); rec.Code != http.StatusTooManyRequests {
		t.Errorf("3a criação: esperava 429, veio %d", rec.Code)
	}
}
