// Rate limiting simples por IP, em memória, sem depender de nenhuma lib
// externa. Uso uma janela fixa: cada IP pode fazer no máximo `limit`
// requisições dentro de `window`; passou disso, devolve 429 até a janela
// virar. É o suficiente pra proteger o /api/shorten de abuso num serviço de
// uma instância só (o estado é por processo, não compartilhado).
package handlers

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// visitor guarda quantas requisições um IP fez na janela atual.
type visitor struct {
	count       int
	windowStart time.Time
}

type rateLimiter struct {
	mu        sync.Mutex
	visitors  map[string]*visitor
	limit     int
	window    time.Duration
	lastPurge time.Time
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{
		visitors: make(map[string]*visitor),
		limit:    limit,
		window:   window,
	}
}

// allow registra uma requisição do IP e diz se ela pode passar.
func (rl *rateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()

	// de tempos em tempos, limpo entradas velhas pra o map não crescer pra
	// sempre conforme novos IPs aparecem.
	if now.Sub(rl.lastPurge) > rl.window {
		for k, v := range rl.visitors {
			if now.Sub(v.windowStart) > rl.window {
				delete(rl.visitors, k)
			}
		}
		rl.lastPurge = now
	}

	v, ok := rl.visitors[ip]
	if !ok || now.Sub(v.windowStart) > rl.window {
		rl.visitors[ip] = &visitor{count: 1, windowStart: now}
		return true
	}
	if v.count >= rl.limit {
		return false
	}
	v.count++
	return true
}

// middleware bloqueia requisições que estouram o limite, respondendo 429.
func (rl *rateLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !rl.allow(clientIP(r)) {
			writeError(w, http.StatusTooManyRequests, "muitas requisições; espere um pouco e tente de novo")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP descobre o IP do cliente. Atrás de um proxy (Render), o IP real
// vem no X-Forwarded-For (primeiro da lista); sem proxy, uso o RemoteAddr.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
