// Implementação de Store que persiste os links num banco Postgres.
//
// O MemoryStore (store.go) é ótimo pra rodar local e pros testes, mas perde
// tudo quando o processo reinicia — e o Render free reinicia/hiberna o
// serviço, então os links sumiriam. Este PostgresStore resolve isso sem que
// os handlers ou o worker pool precisem saber de nada: os dois dependem da
// interface Store, então é só trocar a implementação injetada no main.
package store

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// tempo máximo de cada consulta ao banco, pra uma conexão lenta não segurar
// um handler indefinidamente.
const queryTimeout = 5 * time.Second

// PostgresStore guarda os links num Postgres usando um pool de conexões.
type PostgresStore struct {
	pool *pgxpool.Pool
}

// garantia em tempo de compilação de que *PostgresStore cumpre a interface
// Store — se algum método faltar ou mudar de assinatura, isso quebra o build.
var _ Store = (*PostgresStore)(nil)

// createTableSQL cria a tabela na primeira execução. Uso "IF NOT EXISTS" pra
// a aplicação subir sozinha num banco vazio, sem precisar de um passo de
// migração separado (suficiente pro tamanho deste projeto).
const createTableSQL = `
CREATE TABLE IF NOT EXISTS links (
    code        TEXT PRIMARY KEY,
    destination TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    status      TEXT NOT NULL DEFAULT 'pending',
    checked_at  TIMESTAMPTZ,
    hits        BIGINT NOT NULL DEFAULT 0
);`

// NewPostgresStore abre o pool de conexões (a partir da connection string,
// tipicamente vinda da variável DATABASE_URL) e garante que a tabela existe.
func NewPostgresStore(ctx context.Context, dsn string) (*PostgresStore, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("store: conectando no postgres: %w", err)
	}
	if _, err := pool.Exec(ctx, createTableSQL); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: criando tabela links: %w", err)
	}
	return &PostgresStore{pool: pool}, nil
}

// Close fecha o pool de conexões. Chamado no shutdown do servidor.
func (s *PostgresStore) Close() {
	s.pool.Close()
}

// Create gera um código curto único e insere o link com status "pending". Se
// por azar o código já existir (viola a PK), tenta de novo com outro.
func (s *PostgresStore) Create(destination string) (*Link, error) {
	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()

	for attempts := 0; attempts < 5; attempts++ {
		code := generateCode()

		var createdAt time.Time
		err := s.pool.QueryRow(ctx,
			`INSERT INTO links (code, destination, status)
			 VALUES ($1, $2, $3)
			 RETURNING created_at`,
			code, destination, string(StatusPending),
		).Scan(&createdAt)
		if err != nil {
			if isUniqueViolation(err) {
				continue // colisão de código: tenta outro
			}
			return nil, err
		}

		return &Link{
			Code:        code,
			Destination: destination,
			CreatedAt:   createdAt,
			Status:      StatusPending,
		}, nil
	}
	return nil, errors.New("store: não consegui gerar um código único")
}

// CreateWithCode insere um link com um código específico (alias). Se o código
// já existir, o INSERT viola a chave primária e devolvo ErrCodeTaken.
func (s *PostgresStore) CreateWithCode(code, destination string) (*Link, error) {
	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()

	var createdAt time.Time
	err := s.pool.QueryRow(ctx,
		`INSERT INTO links (code, destination, status)
		 VALUES ($1, $2, $3)
		 RETURNING created_at`,
		code, destination, string(StatusPending),
	).Scan(&createdAt)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrCodeTaken
		}
		return nil, err
	}

	return &Link{
		Code:        code,
		Destination: destination,
		CreatedAt:   createdAt,
		Status:      StatusPending,
	}, nil
}

// DeleteExpired remove os links criados antes de cutoff e devolve quantos
// foram removidos.
func (s *PostgresStore) DeleteExpired(cutoff time.Time) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()

	tag, err := s.pool.Exec(ctx, `DELETE FROM links WHERE created_at < $1`, cutoff)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// Get busca um link pelo código; devolve ErrNotFound se não existir.
func (s *PostgresStore) Get(code string) (*Link, error) {
	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()

	row := s.pool.QueryRow(ctx,
		`SELECT code, destination, created_at, status, checked_at, hits
		 FROM links WHERE code = $1`, code)
	link, err := scanLink(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return link, nil
}

// UpdateStatus grava o resultado da checagem de saúde feita pelo worker.
func (s *PostgresStore) UpdateStatus(code string, status Status, checkedAt time.Time) error {
	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()

	tag, err := s.pool.Exec(ctx,
		`UPDATE links SET status = $1, checked_at = $2 WHERE code = $3`,
		string(status), checkedAt, code)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// IncrementHits soma 1 ao contador de cliques do link. O incremento é feito
// no próprio SQL (hits = hits + 1) pra ser atômico, sem precisar ler-somar-
// gravar de forma separada.
func (s *PostgresStore) IncrementHits(code string) error {
	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()

	tag, err := s.pool.Exec(ctx,
		`UPDATE links SET hits = hits + 1 WHERE code = $1`, code)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// All devolve todos os links, mais recentes primeiro. Alimenta o painel.
func (s *PostgresStore) All() []*Link {
	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()

	rows, err := s.pool.Query(ctx,
		`SELECT code, destination, created_at, status, checked_at, hits
		 FROM links ORDER BY created_at DESC`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	out := make([]*Link, 0)
	for rows.Next() {
		link, err := scanLink(rows)
		if err != nil {
			return nil
		}
		out = append(out, link)
	}
	return out
}

// rowScanner é o que pgx.Row e pgx.Rows têm em comum: um Scan. Deixa scanLink
// servir tanto pra uma linha só (Get) quanto pra iteração (All).
type rowScanner interface {
	Scan(dest ...any) error
}

// scanLink lê uma linha da tabela links pra um *Link. checked_at é anulável
// (links "pending" ainda não foram checados), então leio num *time.Time.
func scanLink(row rowScanner) (*Link, error) {
	var (
		l         Link
		status    string
		checkedAt *time.Time
	)
	if err := row.Scan(&l.Code, &l.Destination, &l.CreatedAt, &status, &checkedAt, &l.Hits); err != nil {
		return nil, err
	}
	l.Status = Status(status)
	if checkedAt != nil {
		l.CheckedAt = *checkedAt
	}
	return &l, nil
}

// isUniqueViolation identifica o erro de violação de chave única do Postgres
// (SQLSTATE 23505), usado pra detectar colisão de código no Create.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// generateCode sorteia um código curto usando crypto/rand (aleatoriedade de
// qualidade, sem precisar semear um gerador). Reaproveita o alfabeto e o
// tamanho definidos em store.go.
func generateCode() string {
	b := make([]byte, codeLength)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand praticamente nunca falha; se falhar, o pior caso é um
		// código menos aleatório, ainda utilizável.
		return string(b)
	}
	for i := range b {
		b[i] = codeAlphabet[int(b[i])%len(codeAlphabet)]
	}
	return string(b)
}
