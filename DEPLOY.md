# Deploy: frontend no Firebase + backend Go grátis

A ideia da arquitetura:

```
[ navegador ] --- HTML/CSS/JS --->  Firebase Hosting  (só arquivos estáticos)
      |
      |  chamadas fetch (/api/...)          redireciona /{code}
      +----------------------------------->  Backend Go  (Render / Cloud Run / ...)
```

O Firebase **não roda Go** — ele só serve o painel. O backend Go mora em outro
host e o painel fala com ele por HTTP. Por isso o backend tem CORS liberado e
lê a porta da variável `PORT`.

---

## Testando tudo localmente antes (recomendado)

Antes de subir pra internet, vale rodar as duas partes na sua máquina em
portas diferentes (o que já simula "domínios diferentes"):

```bash
# terminal 1 — backend, liberando a origem do frontend local
ALLOWED_ORIGIN="http://localhost:5000" go run ./cmd/server

# terminal 2 — frontend estático (a cópia do Firebase)
cd firebase/public
python3 -m http.server 5000
```

O `firebase/public/config.js` já vem apontando pra `http://localhost:8080`,
então é só abrir `http://localhost:5000` no navegador e testar.

---

## Parte 1 — Backend Go num host grátis

O backend Go precisa de um host que rode container. Duas opções gratuitas:

- **Google Cloud Run (recomendado)** — combina com o Firebase (tudo Google),
  não "dorme" como o Render e tem deploy contínuo a cada push. Passo a passo
  dedicado em **[CLOUDRUN.md](CLOUDRUN.md)** (inclui criar conta e billing).
- **Render** — mais simples de começar (não exige cartão pra web service), mas
  o plano grátis "dorme" após inatividade. Passos abaixo.

Depois de subir o backend por qualquer um dos dois, você terá uma **URL
pública** (ex: `https://...run.app` ou `https://...onrender.com`) — guarde ela
pra Parte 2.

### Opção Render (alternativa)

1. Suba o projeto num repositório **público** no GitHub.
2. Crie conta em https://render.com e clique em **New → Web Service**.
3. Conecte o repositório. O Render detecta o `Dockerfile` sozinho; escolha o
   plano **Free**.
4. Clique em **Create Web Service** e espere o build terminar.
5. No fim, o Render te dá uma URL tipo `https://gopher-links.onrender.com`.
   Teste abrindo `https://…onrender.com/healthz` — tem que responder
   `{"status":"ok"}`.

> ⚠️ No plano grátis o serviço "dorme" depois de uns minutos sem uso. A
> primeira requisição depois disso demora ~30-50s pra acordar. É normal e não
> tem custo — só avise isso se for demonstrar pra alguém.

---

## Parte 2 — Frontend no Firebase Hosting

1. Instale o Node.js (se ainda não tiver) e a CLI do Firebase:
   ```bash
   npm install -g firebase-tools
   ```
2. Faça login:
   ```bash
   firebase login
   ```
3. **Importante:** edite `firebase/public/config.js` e troque a URL pela do
   backend que o Render te deu na Parte 1:
   ```js
   window.API_BASE = "https://gopher-links.onrender.com";
   ```
4. Crie um projeto em https://console.firebase.google.com (botão "Adicionar
   projeto"). Anote o ID do projeto.
5. Entre na pasta do Firebase e associe o projeto:
   ```bash
   cd firebase
   firebase use --add        # selecione o projeto que você criou
   ```
6. Faça o deploy:
   ```bash
   firebase deploy --only hosting
   ```
7. O Firebase te dá a URL final, tipo `https://seu-projeto.web.app`. Abra e
   teste encurtando um link.

---

## Parte 3 — Fechar o CORS (opcional, mas recomendado)

Enquanto está com `ALLOWED_ORIGIN=*`, qualquer site pode chamar seu backend.
Depois que o frontend estiver no ar, restrinja pra só o domínio do Firebase:

- No painel do Render → seu serviço → **Environment** → edite `ALLOWED_ORIGIN`
  pra `https://seu-projeto.web.app` e salve (ele faz redeploy sozinho).

---

## Resolvendo problemas comuns

- **O painel abre mas não cria links / erro de CORS no console (F12):**
  confira se o `ALLOWED_ORIGIN` do backend bate exatamente com a URL do
  Firebase (com `https://`, sem barra no final) e se o `config.js` aponta pra
  URL certa do backend.
- **Clico no link curto e dá erro:** o redirecionamento acontece no backend;
  se ele estiver "dormindo" (plano free), espere alguns segundos e tente de
  novo.
- **`firebase deploy` reclama que não tem projeto:** rode `firebase use --add`
  antes, dentro da pasta `firebase/`.
