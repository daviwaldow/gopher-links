// painel do gopher-links: cria links e mantem a lista atualizada.
// a atualizacao e incremental (atualiza cada item no lugar, em vez de
// redesenhar a lista inteira), pra nao piscar e pra a animacao de entrada
// so rodar quando um link novo aparece.

const form = document.getElementById("shorten-form");
const urlInput = document.getElementById("url-input");
const aliasInput = document.getElementById("alias-input");
const formError = document.getElementById("form-error");
const listEl = document.getElementById("links-list");
const emptyState = document.getElementById("empty-state");
const countEl = document.getElementById("link-count");
const connStatus = document.getElementById("conn-status");
const submitBtn = form.querySelector(".btn-primary");

const POLL_INTERVAL_MS = 2000;

// base do backend (vem do config.js). vazio = mesma origem que serviu a pagina.
const API = window.API_BASE || "";

// code -> elemento ja renderizado, pra atualizar no lugar.
const rendered = new Map();

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  hideError();

  const url = urlInput.value.trim();
  if (!url) return;

  const alias = aliasInput.value.trim();
  const payload = { url };
  if (alias) payload.alias = alias;

  setLoading(true);
  try {
    const res = await fetch(API + "/api/shorten", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });
    const data = await res.json().catch(() => ({}));

    if (!res.ok) {
      showError(data.error || "nao deu pra encurtar essa URL");
      return;
    }

    urlInput.value = "";
    aliasInput.value = "";
    await refreshLinks();
  } catch (err) {
    showError("falha ao falar com o servidor");
  } finally {
    setLoading(false);
  }
});

function setLoading(on) {
  submitBtn.classList.toggle("loading", on);
  submitBtn.disabled = on;
}

function showError(msg) {
  formError.textContent = msg;
  formError.classList.remove("hidden");
}

function hideError() {
  formError.classList.add("hidden");
}

async function refreshLinks() {
  try {
    const res = await fetch(API + "/api/links");
    if (!res.ok) throw new Error("status " + res.status);
    const links = await res.json();
    renderLinks(Array.isArray(links) ? links : []);
    setConn(true);
  } catch (err) {
    setConn(false);
  }
}

function renderLinks(links) {
  countEl.textContent = links.length;
  emptyState.style.display = links.length ? "none" : "";

  const seen = new Set();
  for (const link of links) {
    seen.add(link.code);
    let el = rendered.get(link.code);
    if (!el) {
      el = buildItem(link);
      rendered.set(link.code, el);
    } else {
      updateItem(el, link);
    }
    // reanexar move o elemento pra ordem certa sem reiniciar a animacao.
    listEl.appendChild(el);
  }

  for (const [code, el] of rendered) {
    if (!seen.has(code)) {
      el.remove();
      rendered.delete(code);
    }
  }
}

function buildItem(link) {
  const item = document.createElement("div");
  item.className = "link-item enter";
  item.dataset.code = link.code;

  const left = document.createElement("div");
  left.className = "li-left";
  const code = document.createElement("a");
  code.className = "li-code";
  code.target = "_blank";
  code.rel = "noopener noreferrer";
  const dest = document.createElement("span");
  dest.className = "li-dest";
  left.append(code, dest);

  const right = document.createElement("div");
  right.className = "li-right";

  const status = document.createElement("span");
  const dot = document.createElement("span");
  dot.className = "dot";
  const statusText = document.createElement("span");
  status.append(dot, statusText);

  const hits = document.createElement("span");
  hits.className = "li-hits";

  const copy = document.createElement("button");
  copy.type = "button";
  copy.className = "copy-btn";
  copy.textContent = "copiar";
  copy.addEventListener("click", () => copyLink(copy));

  right.append(status, hits, copy);
  item.append(left, right);

  item._refs = { code, dest, status, statusText, hits, copy };
  updateItem(item, link);
  return item;
}

function updateItem(item, link) {
  const r = item._refs;
  r.code.href = link.short_url;
  r.code.textContent = link.short_url.replace(/^https?:\/\//, "");
  r.dest.textContent = link.destination;
  r.dest.title = link.destination;
  r.status.className = "status status-" + link.status;
  r.statusText.textContent = statusLabel(link.status);

  r.hits.textContent = "";
  const strong = document.createElement("strong");
  strong.textContent = link.hits;
  r.hits.append(strong, document.createTextNode(" cliques"));

  r.copy.dataset.url = link.short_url;
}

function statusLabel(status) {
  if (status === "healthy") return "no ar";
  if (status === "unhealthy") return "fora do ar";
  return "checando";
}

function copyLink(btn) {
  navigator.clipboard
    .writeText(btn.dataset.url || "")
    .then(() => {
      btn.textContent = "copiado";
      btn.classList.add("copied");
      setTimeout(() => {
        btn.textContent = "copiar";
        btn.classList.remove("copied");
      }, 1400);
    })
    .catch(() => {});
}

function setConn(ok) {
  connStatus.textContent = ok
    ? "atualizado automaticamente a cada 2s"
    : "sem conexao com o servidor";
  connStatus.classList.toggle("offline", !ok);
}

refreshLinks();
setInterval(refreshLinks, POLL_INTERVAL_MS);
