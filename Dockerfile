# Build em multi-stage: o primeiro estágio compila o binário, o segundo só
# copia o binário pronto pra uma imagem mínima. Assim a imagem final fica
# bem pequena (não carrega o compilador de Go junto), o que ajuda no deploy
# em serviços gratuitos.

# --- estágio de build ---
FROM golang:1.24-alpine AS build
WORKDIR /app

# baixo as dependências primeiro (fica em cache enquanto o go.mod não muda)
COPY go.mod ./
RUN go mod download

# copio o resto do código e compilo. CGO desligado pra gerar um binário
# estático, que roda numa imagem alpine sem depender de bibliotecas do
# sistema.
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /gopher-links ./cmd/server

# --- imagem final ---
FROM alpine:3.20
# certificados raiz, pra o backend conseguir fazer as checagens HTTPS das
# URLs de destino (sem isso, toda checagem de site https falharia).
RUN apk add --no-cache ca-certificates
COPY --from=build /gopher-links /gopher-links

# a maioria dos serviços injeta a porta pela variável PORT; o main.go já lê
# ela. Deixo 8080 como padrão/documentação.
ENV PORT=8080
EXPOSE 8080

ENTRYPOINT ["/gopher-links"]
