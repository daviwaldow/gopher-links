# Deploy do backend no Google Cloud Run (deploy contínuo via GitHub)

O Cloud Run roda o container do backend Go e combina bem com o Firebase por
ser tudo Google. Diferente do Render, o free tier do Cloud Run não "dorme" da
mesma forma — ele escala pra zero mas acorda bem rápido.

Aqui a configuração é **deploy contínuo**: conecto o repositório do GitHub uma
vez e, a cada `git push` na branch `main`, o Google rebuilda a imagem (a partir
do `Dockerfile` que já está no projeto) e publica sozinho.

> Sobre custo: o Cloud Run **exige** um cartão cadastrado (billing ativo), mas
> tem um free tier mensal generoso (2 milhões de requisições, entre outros
> limites) que cobre folgado um projeto de portfólio. Conta nova ainda ganha
> créditos de teste. Pra um uso pequeno assim, não deve gerar cobrança — mas o
> cartão é obrigatório pra ativar.

---

## Parte 0 — Criar a conta e o projeto no Google Cloud

1. Acesse https://console.cloud.google.com com sua conta Google.
2. No topo, clique no seletor de projetos → **Novo projeto**. Dê um nome (ex:
   `gopher-links`) e crie. Anote o **ID do projeto** (é diferente do nome,
   algo tipo `gopher-links-4821`).
3. Ative o faturamento: menu **Faturamento (Billing)** → vincule/crie uma conta
   de faturamento e adicione um cartão. (É o passo que libera o Cloud Run.)
4. Ative as APIs necessárias: na busca do topo, procure e ative (botão
   **Ativar**) estas três:
   - **Cloud Run Admin API**
   - **Cloud Build API**
   - **Artifact Registry API**

   (Se você seguir o fluxo da Parte 1 pelo console, ele costuma oferecer ativar
   essas APIs automaticamente — mas ativar antes evita erro no meio.)

---

## Parte 1 — Conectar o GitHub e configurar o deploy contínuo

1. Suba o projeto num repositório no GitHub (pode ser público ou privado).
2. No console, vá em **Cloud Run** → **Deploy container** → **Service**.
3. Marque a opção **"Continuously deploy from a repository (source or
   function)"** e clique em **Set up with Cloud Build**.
4. **Repository provider:** GitHub → autorize o acesso → escolha o seu
   repositório e a branch **`main`**.
5. **Build type:** escolha **Dockerfile** e deixe o caminho como `/Dockerfile`
   (é o que já está na raiz do projeto). Salve.
6. De volta na tela do serviço, configure:
   - **Region:** escolha uma perto do Brasil, tipo `southamerica-east1` (São
     Paulo).
   - **Authentication:** marque **Allow unauthenticated invocations** — isso é
     essencial, senão o navegador recebe 403 ao chamar a API.
7. Abra **Container(s), Volumes, Networking, Security** → aba **Variables &
   Secrets** → **Add variable**:
   - Nome: `ALLOWED_ORIGIN`  ·  Valor: `*`
     (depois troca pelo domínio do Firebase — ver Parte 3)
   - Não precisa setar `PORT`: o Cloud Run injeta sozinho e o app já lê.
8. Clique em **Create**. O primeiro build/deploy leva alguns minutos.
9. Quando terminar, o Cloud Run te dá a URL pública do serviço, tipo
   `https://gopher-links-xxxxxxxx-rj.a.run.app`. Teste abrindo
   `.../healthz` — tem que responder `{"status":"ok"}`.

A partir daqui, todo `git push` na `main` dispara um novo deploy automático.

---

## Parte 2 — Apontar o frontend (Firebase) pro Cloud Run

1. Edite `firebase/public/config.js` e coloque a URL do Cloud Run:
   ```js
   window.API_BASE = "https://gopher-links-xxxxxxxx-rj.a.run.app";
   ```
2. Faça o deploy do frontend (detalhes no [DEPLOY.md](DEPLOY.md), Parte 2):
   ```bash
   cd firebase
   firebase deploy --only hosting
   ```

---

## Parte 3 — Fechar o CORS (recomendado)

Depois que o frontend estiver no ar com um domínio fixo do Firebase:

1. Cloud Run → seu serviço → **Edit & deploy new revision** → **Variables**.
2. Troque `ALLOWED_ORIGIN` de `*` pro domínio do Firebase, ex:
   `https://seu-projeto.web.app` (com `https://`, sem barra no final).
3. Deploy. Agora só o seu frontend pode chamar a API.

---

## Testando o container localmente antes (opcional)

Se quiser ter certeza de que a imagem sobe antes de mandar pro Cloud Run (e
tiver Docker instalado):

```bash
docker build -t gopher-links .
docker run -p 8080:8080 -e PORT=8080 -e ALLOWED_ORIGIN="*" gopher-links
# abra http://localhost:8080
```

O Cloud Run injeta a `PORT` sozinho; o `-e PORT=8080` acima é só pra rodar
igual na sua máquina.

---

## Se der problema

- **Deploy falha com "container failed to listen on PORT":** o app tem que
  escutar em `0.0.0.0:$PORT`. Esse projeto já faz isso (lê `PORT` do ambiente),
  então normalmente é sinal de que o build pegou uma versão antiga — confira se
  o push foi pra branch certa.
- **Frontend não chama a API / erro de CORS no console (F12):** confira se
  `ALLOWED_ORIGIN` bate exatamente com a URL do Firebase e se o `config.js`
  aponta pra URL certa do Cloud Run.
- **403 ao chamar a API:** faltou marcar **Allow unauthenticated invocations**
  no serviço do Cloud Run.
