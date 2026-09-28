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
	"time"

	"github.com/daviwaldow/gopher-links/internal/store"
	"github.com/daviwaldow/gopher-links/internal/webui"
	"github.com/daviwaldow/gopher-links/internal/worker"
)

// Handler agrupa tudo que as rotas precisam pra funcionar.
type Handler struct {
	Store *baseDeps
}

type baseDeps struct {
	store   store.Store
	pool    *worker.Pool
	ttl     time.Duration // 0 = links nunca expiram
	limiter *rateLimiter  // nil = sem rate limiting
}

// New monta um Handler. linkTTL define em quanto tempo um link expira (0 =
// nunca) e rateLimitPerMin limita quantos links um mesmo IP pode criar por
// minuto (0 ou negativo desliga o limite).
func New(s store.Store, pool *worker.Pool, linkTTL time.Duration, rateLimitPerMin int) *Handler {
	var limiter *rateLimiter
	if rateLimitPerMin > 0 {
		limiter = newRateLimiter(rateLimitPerMin, time.Minute)
	}
	return &Handler{Store: &baseDeps{store: s, pool: pool, ttl: linkTTL, limiter: limiter}}
}

// Routes monta o roteador com todas as rotas da aplicação: a API e o
// painel visual. Uso o ServeMux novo do Go 1.22+, que já sabe filtrar por
// método HTTP e capturar parâmetros de rota (tipo "/{code}") sem precisar
// de nenhuma lib externa de roteamento.
func (h *Handler) Routes() *http.ServeMux {
	mux := http.NewServeMux()

	// API
	mux.Handle("POST /api/shorten", h.rateLimited(http.HandlerFunc(h.Shorten)))
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

// shortenRequest é o corpo esperado no POST /api/shorten. O alias é opcional:
// se vier, vira o código curto; senão, um código aleatório é gerado.
type shortenRequest struct {
	URL   string `json:"url"`
	Alias string `json:"alias"`
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
	// o corpo esperado é um JSON minúsculo ({"url": "..."}); 4 KiB é de
	// sobra. Limitar evita que um cliente mande um corpo gigante e faça o
	// servidor gastar memória à toa.
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)

	var req shortenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "corpo da requisição inválido (esperado JSON)")
		return
	}

	if !isValidURL(req.URL) {
		writeError(w, http.StatusBadRequest, "a url precisa ser absoluta e usar http ou https")
		return
	}

	// alias opcional: se o usuário mandou um, valido e tento criar com ele;
	// senão, gero um código aleatório.
	var (
		link *store.Link
		err  error
	)
	if alias := strings.TrimSpace(req.Alias); alias != "" {
		if !isValidAlias(alias) {
			writeError(w, http.StatusBadRequest, "alias inválido: use 3 a 32 caracteres (letras, números, - ou _) e não pode ser uma palavra reservada")
			return
		}
		link, err = h.Store.store.CreateWithCode(alias, req.URL)
		if errors.Is(err, store.ErrCodeTaken) {
			writeError(w, http.StatusConflict, "esse alias já está em uso, escolha outro")
			return
		}
	} else {
		link, err = h.Store.store.Create(req.URL)
	}
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

	if h.expired(link) {
		writeError(w, http.StatusGone, "esse link expirou")
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

	if h.expired(link) {
		writeError(w, http.StatusGone, "esse link expirou")
		return
	}

	writeJSON(w, http.StatusOK, toResponse(link, r))
}

// ListLinks trata o GET /api/links: devolve todos os links cadastrados
// (menos os já expirados). É o endpoint que o painel usa pra montar a tabela.
func (h *Handler) ListLinks(w http.ResponseWriter, r *http.Request) {
	links := h.Store.store.All()
	out := make([]linkResponse, 0, len(links))
	for _, l := range links {
		if h.expired(l) {
			continue
		}
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
// allowedOrigins pode ser "*" (libera qualquer origem — útil só pra teste
// local) ou uma lista separada por vírgula de origens permitidas, ex.:
// "https://gopherlinks.web.app,http://localhost:8080". Nesse caso eu ecoo de
// volta só a origem que veio na requisição, se ela estiver na lista — que é a
// forma correta de restringir (mandar a lista inteira no header não é válido).
func CORS(allowedOrigins string, next http.Handler) http.Handler {
	allowAll := strings.TrimSpace(allowedOrigins) == "*"
	allowed := make(map[string]bool)
	if !allowAll {
		for _, o := range strings.Split(allowedOrigins, ",") {
			if o = strings.TrimSpace(o); o != "" {
				allowed[o] = true
			}
		}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		switch {
		case allowAll:
			w.Header().Set("Access-Control-Allow-Origin", "*")
		case origin != "" && allowed[origin]:
			// como a resposta varia conforme a origem, aviso os caches disso.
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Add("Vary", "Origin")
		}
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

// SecurityHeaders adiciona cabeçalhos de segurança nas respostas servidas
// pelo próprio Go (o painel embutido em webui). O frontend hospedado no
// Firebase já recebe headers equivalentes via firebase.json; este middleware
// cobre o caso do Go servindo o painel direto (localhost ou no Render).
//
// A CSP usa connect-src 'self' porque, quando o Go serve o painel, o config.js
// deixa API_BASE vazio e o fetch vai pra mesma origem.
func SecurityHeaders(next http.Handler) http.Handler {
	const csp = "default-src 'self'; base-uri 'self'; object-src 'none'; " +
		"frame-ancestors 'none'; form-action 'self'; img-src 'self' data:; " +
		"style-src 'self'; script-src 'self'; connect-src 'self'"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Content-Security-Policy", csp)
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
//
// Atrás de um proxy que termina o TLS (Render, Cloud Run, etc.) o request que
// chega ao app é HTTP puro (r.TLS == nil), mas o cliente falou HTTPS. Por isso
// confio primeiro no header X-Forwarded-Proto que o proxy preenche; sem ele,
// caio no r.TLS (útil rodando direto, sem proxy).
func shortURLFor(r *http.Request, code string) string {
	scheme := "http"
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	} else if r.TLS != nil {
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

// expired diz se um link já passou do TTL configurado. Com ttl == 0 os links
// nunca expiram.
func (h *Handler) expired(link *store.Link) bool {
	ttl := h.Store.ttl
	return ttl > 0 && time.Since(link.CreatedAt) > ttl
}

// rateLimited envolve um handler com o rate limiter, quando ele está ligado.
func (h *Handler) rateLimited(next http.Handler) http.Handler {
	if h.Store.limiter == nil {
		return next
	}
	return h.Store.limiter.middleware(next)
}

// reservedAliases são códigos que não podem virar alias porque colidiriam com
// rotas fixas (elas têm prioridade no roteador e o link ficaria inacessível).
// Os arquivos estáticos (style.css etc.) têm ponto, que o conjunto de
// caracteres do isValidAlias já barra.
var reservedAliases = map[string]bool{
	"api":     true,
	"healthz": true,
}

// isValidAlias valida um alias escolhido pelo usuário: 3 a 32 caracteres, só
// letras, números, hífen ou underscore, e fora da lista de reservados.
func isValidAlias(a string) bool {
	if len(a) < 3 || len(a) > 32 {
		return false
	}
	if reservedAliases[strings.ToLower(a)] {
		return false
	}
	for _, r := range a {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_':
		default:
			return false
		}
	}
	return true
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
