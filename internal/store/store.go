// Package store cuida de guardar os links encurtados.
//
// Hoje é tudo em memória (um map protegido por mutex), mas separei atrás de
// uma interface (Store) de propósito: se um dia eu quiser trocar por
// Postgres ou Redis, só preciso criar outro tipo que implemente a mesma
// interface. Os handlers e o worker pool não sabem nem precisam saber que o
// armazenamento é em memória.
package store

import (
	"errors"
	"math/rand"
	"sync"
	"time"
)

// Status é o resultado da checagem de saúde feita em background pelo worker
// pool. Todo link nasce como "pending" até o worker confirmar se o destino
// responde ou não.
type Status string

const (
	StatusPending   Status = "pending"
	StatusHealthy   Status = "healthy"
	StatusUnhealthy Status = "unhealthy"
)

// ErrNotFound é devolvido quando o código curto não existe.
var ErrNotFound = errors.New("store: link não encontrado")

// ErrCodeTaken é devolvido quando alguém tenta criar um link com um código
// (alias) que já existe.
var ErrCodeTaken = errors.New("store: código já em uso")

// Link representa um único link encurtado e os metadados dele.
type Link struct {
	Code        string
	Destination string
	CreatedAt   time.Time
	Status      Status
	CheckedAt   time.Time
	Hits        int64
}

// Store é o contrato que os handlers HTTP usam para ler e escrever links.
// Definir isso como interface (em vez de usar *MemoryStore direto em todo
// lugar) é o que permite testar os handlers sem precisar de um banco de
// verdade rodando.
type Store interface {
	Create(destination string) (*Link, error)
	CreateWithCode(code, destination string) (*Link, error)
	Get(code string) (*Link, error)
	UpdateStatus(code string, status Status, checkedAt time.Time) error
	IncrementHits(code string) error
	All() []*Link
	DeleteExpired(cutoff time.Time) (int, error)
}

// MemoryStore guarda tudo num map, protegido por um RWMutex, porque tanto os
// handlers HTTP quanto as goroutines do worker pool mexem nisso ao mesmo
// tempo (várias requisições simultâneas + checagens de saúde rodando em
// paralelo). Sem o mutex, isso seria uma race condition clássica.
type MemoryStore struct {
	mu    sync.RWMutex
	links map[string]*Link
	rng   *rand.Rand
}

// NewMemoryStore cria um MemoryStore vazio, pronto pra usar.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		links: make(map[string]*Link),
		rng:   rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// alfabeto usado para gerar os códigos curtos (base62, sem caracteres
// ambíguos como "0" e "O" não foi uma preocupação aqui, mas dá pra ajustar
// facilmente se precisar).
const codeAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
const codeLength = 7

// Create gera um código curto único para o destino informado e salva o link
// com status "pending". A checagem de saúde acontece depois, de forma
// assíncrona — quem chama Create não fica esperando por ela.
func (s *MemoryStore) Create(destination string) (*Link, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// gera códigos até achar um que ainda não existe no map. Com 7
	// caracteres em base62 a chance de colisão é bem baixa, mas prefiro
	// garantir do que assumir.
	var code string
	for {
		code = s.randomCode()
		if _, exists := s.links[code]; !exists {
			break
		}
	}

	link := &Link{
		Code:        code,
		Destination: destination,
		CreatedAt:   time.Now(),
		Status:      StatusPending,
	}
	s.links[code] = link
	return link, nil
}

// CreateWithCode cria um link usando um código específico (alias escolhido
// pelo usuário). Se o código já existir, devolve ErrCodeTaken.
func (s *MemoryStore) CreateWithCode(code, destination string) (*Link, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.links[code]; exists {
		return nil, ErrCodeTaken
	}

	link := &Link{
		Code:        code,
		Destination: destination,
		CreatedAt:   time.Now(),
		Status:      StatusPending,
	}
	s.links[code] = link
	return link, nil
}

// DeleteExpired remove todos os links criados antes de cutoff e devolve
// quantos foram apagados. Usado pela rotina de limpeza de links antigos.
func (s *MemoryStore) DeleteExpired(cutoff time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	n := 0
	for code, link := range s.links {
		if link.CreatedAt.Before(cutoff) {
			delete(s.links, code)
			n++
		}
	}
	return n, nil
}

// randomCode sorteia uma string aleatória de codeLength caracteres a partir
// do alfabeto definido acima.
func (s *MemoryStore) randomCode() string {
	b := make([]byte, codeLength)
	for i := range b {
		b[i] = codeAlphabet[s.rng.Intn(len(codeAlphabet))]
	}
	return string(b)
}

// Get busca um link pelo código. Devolve uma cópia (não o ponteiro
// original) pra evitar que quem chamou fique segurando uma referência que
// pode ser alterada por outra goroutine depois.
func (s *MemoryStore) Get(code string) (*Link, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	link, ok := s.links[code]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *link
	return &cp, nil
}

// UpdateStatus grava o resultado de uma checagem de saúde. É chamado pelas
// goroutines do worker, então concorre com leituras vindas dos handlers —
// por isso o Lock (de escrita) em vez do RLock.
func (s *MemoryStore) UpdateStatus(code string, status Status, checkedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	link, ok := s.links[code]
	if !ok {
		return ErrNotFound
	}
	link.Status = status
	link.CheckedAt = checkedAt
	return nil
}

// IncrementHits soma 1 ao contador de cliques de um link. Chamado toda vez
// que alguém acessa a URL curta e é redirecionado.
func (s *MemoryStore) IncrementHits(code string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	link, ok := s.links[code]
	if !ok {
		return ErrNotFound
	}
	link.Hits++
	return nil
}

// All devolve uma cópia de todos os links cadastrados. Uso principalmente
// pra alimentar o painel visual (lista de links na tela).
func (s *MemoryStore) All() []*Link {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]*Link, 0, len(s.links))
	for _, link := range s.links {
		cp := *link
		out = append(out, &cp)
	}
	return out
}
