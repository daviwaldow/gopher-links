// Package handlers conecta as requisições HTTP ao store e ao worker pool.
// Os handlers dependem da interface store.Store, não de uma implementação
// concreta — isso é o que permite testar tudo aqui sem precisar de um banco
// de verdade (ver handlers_test.go).
package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/daviwaldow/gopher-links/internal/store"
	"github.com/daviwaldow/gopher-links/internal/webui"
	"github.com/daviwaldow/gopher-links/internal/worker"
)

// Handler agrupa tudo que as rotas precisam pra funcionar.
type Handler struct {
	Store *baseDeps
}

type baseDeps struct {
	store store.Store
	pool  *worker.Pool
}

// New monta um Handler usando s como armazenamento e pool para agendar as
// checagens assíncronas de saúde depois que um link é criado.
func New(s store.Store, pool *worker.Pool) *Handler {
	return &Handler{Store: &baseDeps{store: s, pool: pool}}
}

// Routes monta o roteador com todas as rotas da aplicação: a API e o
// painel visual. Uso o ServeMux novo do Go 1.22+, que já sabe filtrar por
// método HTTP e capturar parâmetros de rota (tipo "/{code}") sem precisar
// de nenhuma lib externa de roteamento.
func (h *Handler) Routes() *http.ServeMux {
	mux := http.NewServeMux()

	// API
	mux.HandleFunc("POST /api/shorten", h.Shorten)
	mux.HandleFunc("GET /api/links", h.ListLinks)
	mux.HandleFunc("GET /api/links/{code}", h.LinkStats)
	mux.HandleFunc("GET /healthz", h.Healthz)

	// painel visual: a página principal + os arquivos estáticos, todos na
	// raiz. Registro cada arquivo pelo nome exato porque um padrão literal
	// (tipo "/style.css") é mais específico que o curinga "/{code}", então
	// o Go dá prioridade a ele automaticamente — sem isso, uma requisição
	// pra "/style.css" cairia na rota de redirecionamento.
	mux.HandleFunc("GET /{$}", webui.Index)
	mux.HandleFunc("GET /style.css", webui.Asset)
	mux.HandleFunc("GET /app.js", webui.Asset)
	mux.HandleFunc("GET /config.js", webui.Asset)

	// redirecionamento do link curto — o curinga "/{code}" é o padrão mais
	// genérico, então só pega o que não bateu nas rotas específicas acima.
	mux.HandleFunc("GET /{code}", h.Redirect)

	return mux
}

// shortenRequest é o corpo esperado no POST /api/shorten.
type shortenRequest struct {
	URL string `json:"url"`
}

// linkResponse é o formato usado pra devolver um link em JSON, tanto na
// criação quanto nas consultas.
type linkResponse struct {
	Code        string `json:"code"`
	Destination string `json:"destination"`
	Status      string `json:"status"`
	Hits        int64  `json:"hits"`
	ShortURL    string `json:"short_url"`
}

// Shorten trata o POST /api/shorten: valida a URL recebida, cria o link e
// dispara a checagem de saúde no worker pool sem esperar ela terminar.
func (h *Handler) Shorten(w http.ResponseWriter, r *http.Request) {
	var req shortenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "corpo da requisição inválido (esperado JSON)")
		return
	}

	if !isValidURL(req.URL) {
		writeError(w, http.StatusBadRequest, "a url precisa ser absoluta e usar http ou https")
		return
	}

	link, err := h.Store.store.Create(req.URL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "não consegui criar o link")
		return
	}

	// jogo o Submit numa goroutine porque, se a fila do pool estiver
	// cheia, Submit bloqueia — e eu não quero que uma criação de link
	// fique presa esperando isso. A resposta já sai com status "pending".
	go h.Store.pool.Submit(worker.Job{Code: link.Code, Destination: link.Destination})

	writeJSON(w, http.StatusCreated, toResponse(link, r))
}

// Redirect trata o GET /{code}: redireciona (302) pra URL original e conta
// mais um clique. Devolve 404 se o código não existir.
func (h *Handler) Redirect(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")

	link, err := h.Store.store.Get(code)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "link não encontrado")
			return
		}
		writeError(w, http.StatusInternalServerError, "erro ao buscar o link")
		return
	}

	_ = h.Store.store.IncrementHits(code)
	http.Redirect(w, r, link.Destination, http.StatusFound)
}

// LinkStats trata o GET /api/links/{code}: devolve os metadados de um link
// específico (incluindo o status da checagem de saúde) sem redirecionar.
func (h *Handler) LinkStats(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")

	link, err := h.Store.store.Get(code)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "link não encontrado")
			return
		}
		writeError(w, http.StatusInternalServerError, "erro ao buscar o link")
		return
	}

	writeJSON(w, http.StatusOK, toResponse(link, r))
}

// ListLinks trata o GET /api/links: devolve todos os links cadastrados.
// É o endpoint que o painel visual usa pra montar a tabela na tela.
func (h *Handler) ListLinks(w http.ResponseWriter, r *http.Request) {
	links := h.Store.store.All()
	out := make([]linkResponse, 0, len(links))
	for _, l := range links {
		out = append(out, toResponse(l, r))
	}
	writeJSON(w, http.StatusOK, out)
}

// Healthz é só um endpoint simples pra saber se o servidor está de pé.
func (h *Handler) Healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// CORS é um middleware que libera o backend pra ser chamado por um frontend
// hospedado em outro domínio (no meu caso, o painel servido pelo Firebase
// Hosting). Sem isso, o navegador bloqueia as chamadas fetch por causa da
// política de mesma origem (same-origin policy).
//
// allowedOrigin é a origem liberada — em produção eu passo o domínio do
// Firebase; "*" libera qualquer um (útil só pra teste).
func CORS(allowedOrigin string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		// O navegador manda um OPTIONS antes do POST de verdade (o
		// "preflight") pra perguntar se pode. Respondo aqui mesmo, sem
		// deixar chegar nas rotas, senão o mux devolveria 405.
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// toResponse converte um *store.Link (modelo interno) pro formato de
// resposta JSON, já montando a URL curta completa.
func toResponse(link *store.Link, r *http.Request) linkResponse {
	return linkResponse{
		Code:        link.Code,
		Destination: link.Destination,
		Status:      string(link.Status),
		Hits:        link.Hits,
		ShortURL:    shortURLFor(r, link.Code),
	}
}

// shortURLFor monta a URL curta completa (com esquema e host) a partir da
// requisição atual, pra funcionar tanto em localhost quanto num domínio
// de verdade sem precisar configurar nada.
func shortURLFor(r *http.Request, code string) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host + "/" + code
}

// isValidURL checa se a string recebida é uma URL absoluta http(s). Não
// tento validar se o domínio existe aqui — isso é justamente o trabalho do
// worker pool, feito depois e em background.
func isValidURL(raw string) bool {
	if strings.TrimSpace(raw) == "" {
		return false
	}
	u, err := url.ParseRequestURI(raw)
	if err != nil {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

// writeJSON serializa payload como JSON e escreve na resposta com o status
// informado. Centralizei isso aqui pra não repetir o mesmo Header/Encode em
// cada handler.
func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// writeError é um atalho pra devolver um erro em JSON no formato
// {"error": "mensagem"}.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
