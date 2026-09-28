# gopher-links

Encurtador de links escrito em Go puro, sem framework web. Tem um painel
visual simples e uma checagem de saúde assíncrona: assim que um link é criado,
um pool de workers verifica em segundo plano se a URL de destino está no ar,
sem travar a resposta da API.

Montei o projeto pra praticar concorrência em Go de forma aplicada. Não é só
um "print de goroutine", e sim um worker pool de verdade resolvendo um
problema real: checar a disponibilidade de várias URLs sem bloquear as
requisições.

![painel do gopher-links](docs/painel.png)

## Demo

Painel: https://gopherlinks.web.app
API: https://gopher-links.onrender.com

O deploy é contínuo. Cada push na `main` publica o frontend no Firebase e
reconstrói o backend no Render (ver `.github/workflows/`).

## Como funciona

O `POST /api/shorten` cria o link e responde na hora, já com o status
`pending`. A verificação da URL fica pra depois, em segundo plano.

Um worker pool (`internal/worker`) consome os jobs de checagem por um channel.
Cada checagem tem o próprio timeout via `context`, então uma URL lenta ou
travada não segura os outros workers. O resultado (`healthy` ou `unhealthy`)
volta pro store, e o painel (`GET /`) consulta `GET /api/links` a cada dois
segundos pra mostrar a atualização sem precisar dar F5.

O armazenamento (`internal/store`) fica atrás de uma interface (`Store`), com
duas implementações:

- Em memória: o padrão, ótimo pra rodar e testar localmente.
- Postgres (`PostgresStore`): usado quando a variável `DATABASE_URL` está
  definida, aí os links persistem entre reinícios.

Como os handlers e o worker dependem só da interface, dá pra trocar de uma
implementação pra outra sem mexer no resto do código.

O projeto ainda tem alias personalizado (campo `alias` no POST), expiração de
links por tempo de vida (`LINK_TTL_DAYS`, com limpeza periódica em segundo
plano) e rate limiting por IP no `/api/shorten` (`RATE_LIMIT_PER_MIN`).

## Rotas

| Método | Rota                | O que faz                                             |
|--------|---------------------|-------------------------------------------------------|
| GET    | `/`                 | painel visual                                         |
| POST   | `/api/shorten`      | body `{"url":"...","alias":"opcional"}`, cria um link |
| GET    | `/{code}`           | redireciona (302) pro destino e conta o clique        |
| GET    | `/api/links/{code}` | metadados de um link específico                       |
| GET    | `/api/links`        | lista todos os links                                  |
| GET    | `/healthz`          | healthcheck do servidor                               |

Exemplo com curl:

```bash
curl -X POST localhost:8080/api/shorten -d '{"url":"https://go.dev"}'
# {"code":"aB3xQ9z","destination":"https://go.dev","status":"pending",...}

curl localhost:8080/api/links/aB3xQ9z
# o status vira "healthy" assim que a checagem em segundo plano termina
```

## Rodando localmente

```bash
make run          # sobe em http://localhost:8080
make build        # gera o binário em bin/gopher-links
make test         # roda os testes
make test-race    # roda os testes com o detector de race conditions
```

Depois é só abrir `http://localhost:8080` no navegador.

## Estrutura

```
cmd/server/         main(): sobe o servidor e desliga tudo direito no Ctrl+C
internal/store/     interface Store e as implementações (memória e Postgres)
internal/worker/    worker pool das checagens de saúde assíncronas
internal/handlers/  handlers HTTP e middlewares (CORS, headers de segurança)
internal/webui/     painel visual (HTML, CSS e JS embutidos no binário)
firebase/           cópia estática do painel e config do Firebase Hosting
Dockerfile          build do backend pra rodar em Render, Cloud Run, etc.
render.yaml         blueprint pra subir no Render
.github/workflows/  CI/CD: deploy do frontend e do backend, keep-warm, testes, secret-scan
DEPLOY.md           passo a passo do frontend no Firebase e backend gratuito
CLOUDRUN.md         deploy do backend no Google Cloud Run
```

## Em produção

O painel pode ser servido pelo próprio Go (do jeito acima) ou hospedado à
parte no Firebase Hosting, com o backend Go num host gratuito como Render ou
Cloud Run. Nesse modo o frontend e o backend ficam em domínios diferentes, e
por isso o backend tem CORS e lê a porta da variável `PORT`. O passo a passo
completo está no [DEPLOY.md](DEPLOY.md), e o deploy do backend no Google Cloud
Run está no [CLOUDRUN.md](CLOUDRUN.md).

## Variáveis de ambiente

| Variável             | Padrão | Pra que serve                                       |
|----------------------|--------|-----------------------------------------------------|
| `PORT`               | `8080` | porta do servidor (os hosts gratuitos injetam isso) |
| `ALLOWED_ORIGIN`     | `*`    | origens liberadas no CORS; aceita lista por vírgula |
| `DATABASE_URL`       | vazio  | connection string do Postgres; sem ela, usa memória |
| `LINK_TTL_DAYS`      | `0`    | dias até um link expirar (0 nunca expira)           |
| `RATE_LIMIT_PER_MIN` | `30`   | links que um mesmo IP cria por minuto (0 sem limite)|

## Próximos passos

- métricas (Prometheus) e um dashboard de uso
- QR code pra cada link curto
- painel de administração pra listar e remover links
