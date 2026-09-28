// script do painel: cria links via API e mantém a tabela atualizada
// consultando GET /api/links de tempos em tempos. Nada de framework aqui,
// só fetch + manipulação de DOM mesmo, que é o suficiente pro tamanho da
// tela.

const form = document.getElementById("shorten-form");
const urlInput = document.getElementById("url-input");
const aliasInput = document.getElementById("alias-input");
const formError = document.getElementById("form-error");
const linksBody = document.getElementById("links-body");
const linkCount = document.getElementById("link-count");
const emptyRow = document.getElementById("empty-row");
const connStatus = document.getElementById("conn-status");

const POLL_INTERVAL_MS = 2000;

// Base do backend. Vem do config.js. Se estiver vazio, as chamadas vão pra
// mesma origem que serviu a página (caso do Go servindo tudo junto). Quando
// o painel está no Firebase, o config.js aponta pra URL do backend.
const API = window.API_BASE || "";

// envia o form pra API. Se der certo, já chama refreshLinks() na hora pra
// não esperar o próximo ciclo de polling mostrar o link novo.
form.addEventListener("submit", async (event) => {
  event.preventDefault();
  hideError();

  const url = urlInput.value.trim();
  if (!url) return;

  const alias = aliasInput.value.trim();
  const payload = { url };
  if (alias) payload.alias = alias;

  const submitButton = form.querySelector("button");
  submitButton.disabled = true;

  try {
    const res = await fetch(API + "/api/shorten", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });

    const data = await res.json();

    if (!res.ok) {
      showError(data.error || "não deu pra encurtar essa URL");
      return;
    }

    urlInput.value = "";
    aliasInput.value = "";
    await refreshLinks();
  } catch (err) {
    showError("falha ao falar com o servidor");
  } finally {
    submitButton.disabled = false;
  }
});

function showError(msg) {
  formError.textContent = msg;
  formError.classList.remove("hidden");
}

function hideError() {
  formError.classList.add("hidden");
}

// busca a lista de links na API e redesenha a tabela inteira. Simples,
// mas como o número de links de um projeto de portfólio é pequeno, não
// compensa complicar com atualização incremental de DOM.
async function refreshLinks() {
  try {
    const res = await fetch(API + "/api/links");
    if (!res.ok) throw new Error("status " + res.status);

    const links = await res.json();
    renderLinks(links);
    setConnStatus(true);
  } catch (err) {
    setConnStatus(false);
  }
}

function renderLinks(links) {
  linkCount.textContent = links.length;

  if (links.length === 0) {
    linksBody.innerHTML = "";
    linksBody.appendChild(emptyRow);
    return;
  }

  // mais recentes primeiro
  links.sort((a, b) => (a.code < b.code ? 1 : -1));

  linksBody.innerHTML = links.map(rowHTML).join("");

  // liga os botões de copiar depois de recriar o HTML
  linksBody.querySelectorAll(".copy-btn").forEach((btn) => {
    btn.addEventListener("click", () => copyToClipboard(btn.dataset.url, btn));
  });
}

function rowHTML(link) {
  return `
    <tr>
      <td><a class="short-link" href="${link.short_url}" target="_blank">${link.short_url.replace(/^https?:\/\//, "")}</a></td>
      <td><span class="destination" title="${escapeHtml(link.destination)}">${escapeHtml(link.destination)}</span></td>
      <td><span class="status ${link.status}">${statusLabel(link.status)}</span></td>
      <td>${link.hits}</td>
      <td><button class="copy-btn" data-url="${link.short_url}">copiar</button></td>
    </tr>
  `;
}

function statusLabel(status) {
  switch (status) {
    case "healthy":
      return "no ar";
    case "unhealthy":
      return "fora do ar";
    default:
      return "checando...";
  }
}

function escapeHtml(str) {
  const div = document.createElement("div");
  div.textContent = str;
  return div.innerHTML;
}

function copyToClipboard(text, btn) {
  navigator.clipboard
    .writeText(text)
    .then(() => showCopied(btn))
    .catch(() => {
      /* clipboard pode falhar em contexto não-https; sem problema, ignora */
    });
}

// troca o texto do botão por "copiado!" por um instante, como feedback visual.
function showCopied(btn) {
  if (!btn) return;
  const original = btn.textContent;
  btn.textContent = "copiado!";
  btn.classList.add("copied");
  setTimeout(() => {
    btn.textContent = original;
    btn.classList.remove("copied");
  }, 1500);
}

function setConnStatus(ok) {
  connStatus.textContent = ok
    ? "atualizado automaticamente a cada " + POLL_INTERVAL_MS / 1000 + "s"
    : "sem conexão com o servidor";
}

// primeira carga + polling
refreshLinks();
setInterval(refreshLinks, POLL_INTERVAL_MS);
