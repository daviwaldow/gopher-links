// Package worker implementa um pool de goroutines que checa, em segundo
// plano, se a URL de destino de um link encurtado ainda está no ar.
//
// A ideia é: quando alguém cria um link, eu não quero fazer a pessoa esperar
// a checagem de rede terminar pra responder a requisição. Então a checagem
// entra numa fila (channel) e um número fixo de goroutines vai consumindo
// essa fila em paralelo. Cada checagem tem um timeout próprio via context,
// então se um servidor de destino travar ou demorar demais, isso não trava
// o worker inteiro — só aquele job falha e o worker segue pro próximo.
package worker

import (
	"context"
	"net/http"
	"time"

	"github.com/daviwaldow/gopher-links/internal/store"
)

// Job representa uma checagem pendente: qual código e qual URL verificar.
type Job struct {
	Code        string
	Destination string
}

// Pool é um conjunto fixo de goroutines consumindo Jobs de um channel
// compartilhado.
type Pool struct {
	jobs       chan Job
	store      store.Store
	numWorkers int
	httpClient *http.Client
	checkTTL   time.Duration
}

// Option ajusta configurações do Pool na criação (padrão de "functional
// options", útil quando não quero um construtor com 10 parâmetros).
type Option func(*Pool)

// WithCheckTimeout troca o tempo máximo que uma checagem pode levar antes de
// ser considerada falha. O padrão é 5 segundos.
func WithCheckTimeout(d time.Duration) Option {
	return func(p *Pool) { p.checkTTL = d }
}

// NewPool cria um Pool com numWorkers goroutines e uma fila com capacidade
// pra queueSize jobs pendentes. Não inicia os workers ainda — isso é feito
// no Start, assim quem cria o pool decide quando (e com qual contexto) ele
// começa a rodar.
func NewPool(s store.Store, numWorkers, queueSize int, opts ...Option) *Pool {
	p := &Pool{
		jobs:       make(chan Job, queueSize),
		store:      s,
		numWorkers: numWorkers,
		httpClient: &http.Client{},
		checkTTL:   5 * time.Second,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Submit coloca um job na fila. Se a fila estiver cheia, essa chamada
// bloqueia até abrir espaço — por isso, no handler HTTP, eu chamo Submit
// dentro de uma goroutine separada, pra nunca travar a resposta da API por
// causa disso.
func (p *Pool) Submit(j Job) {
	p.jobs <- j
}

// Start sobe numWorkers goroutines que ficam consumindo jobs até o context
// ser cancelado ou o channel de jobs ser fechado.
func (p *Pool) Start(ctx context.Context) {
	for i := 0; i < p.numWorkers; i++ {
		go p.runWorker(ctx, i)
	}
}

// Close para de aceitar novos jobs. Só deve ser chamado uma vez (fechar um
// channel já fechado gera panic).
func (p *Pool) Close() {
	close(p.jobs)
}

// runWorker é o loop principal de cada goroutine do pool: fica esperando um
// job chegar ou o contexto acabar, o que vier primeiro.
func (p *Pool) runWorker(ctx context.Context, id int) {
	for {
		select {
		case <-ctx.Done():
			return
		case job, ok := <-p.jobs:
			if !ok {
				// channel fechado (Close foi chamado) e sem mais jobs
				return
			}
			p.process(ctx, job)
		}
	}
}

// process executa a checagem de um job e grava o resultado no store.
func (p *Pool) process(ctx context.Context, job Job) {
	checkCtx, cancel := context.WithTimeout(ctx, p.checkTTL)
	defer cancel()

	status := store.StatusHealthy
	if !p.isReachable(checkCtx, job.Destination) {
		status = store.StatusUnhealthy
	}

	// se o link já tiver sido removido entre o Submit e agora, UpdateStatus
	// só devolve ErrNotFound e eu ignoro — não é um erro que importa aqui.
	_ = p.store.UpdateStatus(job.Code, status, time.Now())
}

// isReachable tenta um HEAD primeiro (mais leve) e, se não der certo, tenta
// um GET — porque tem bastante servidor por aí que não implementa HEAD
// direito e devolve erro só por isso.
func (p *Pool) isReachable(ctx context.Context, url string) bool {
	if p.doRequest(ctx, http.MethodHead, url) {
		return true
	}
	return p.doRequest(ctx, http.MethodGet, url)
}

// doRequest faz a requisição de fato e considera "no ar" qualquer resposta
// que não seja erro de servidor (5xx) ou falha de conexão.
func (p *Pool) doRequest(ctx context.Context, method, url string) bool {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return false
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode < 500
}
