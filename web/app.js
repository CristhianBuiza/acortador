"use strict";

const $ = (sel) => document.querySelector(sel);
const TOKEN_KEY = "acortador.token";

const form = $("#shorten");
const errorBox = $("#error");
const result = $("#result");
const shortLink = $("#short");
const tokenInput = $("#token");
const table = $("#links");

tokenInput.value = localStorage.getItem(TOKEN_KEY) || "";
$("#host").textContent = `Links cortos en ${location.hostname}`;

async function api(method, path, body) {
  const headers = { "Content-Type": "application/json" };
  const token = localStorage.getItem(TOKEN_KEY);
  if (token) headers.Authorization = `Bearer ${token}`;

  const res = await fetch(path, { method, headers, body: body ? JSON.stringify(body) : undefined });
  if (res.status === 204) return null;
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || `Error ${res.status}`);
  return data;
}

function showError(msg) {
  errorBox.textContent = msg;
  errorBox.hidden = !msg;
}

form.addEventListener("submit", async (e) => {
  e.preventDefault();
  showError("");
  const button = form.querySelector("button");
  button.disabled = true;
  try {
    const link = await api("POST", "/api/links", {
      url: form.url.value.trim(),
      alias: form.alias.value.trim(),
    });
    shortLink.href = link.short_url;
    shortLink.textContent = link.short_url;
    result.hidden = false;
    form.reset();
    if ($("#access").open) loadLinks();
  } catch (err) {
    showError(err.message);
    if (/token/i.test(err.message)) $("#access").open = true;
  } finally {
    button.disabled = false;
  }
});

$("#copy").addEventListener("click", async (e) => {
  await navigator.clipboard.writeText(shortLink.textContent);
  e.target.textContent = "Copiado";
  setTimeout(() => (e.target.textContent = "Copiar"), 1500);
});

$("#save-token").addEventListener("click", () => {
  const token = tokenInput.value.trim();
  if (token) localStorage.setItem(TOKEN_KEY, token);
  else localStorage.removeItem(TOKEN_KEY);
  loadLinks();
});

$("#access").addEventListener("toggle", (e) => {
  if (e.target.open) loadLinks();
});

async function loadLinks() {
  const tbody = table.querySelector("tbody");
  tbody.replaceChildren();
  try {
    const links = await api("GET", "/api/links");
    for (const link of links) tbody.append(row(link));
    table.hidden = links.length === 0;
  } catch {
    table.hidden = true;
  }
}

function row(link) {
  const tr = document.createElement("tr");

  const short = document.createElement("td");
  const a = document.createElement("a");
  a.href = link.short_url;
  a.target = "_blank";
  a.rel = "noopener";
  a.textContent = link.code;
  short.append(a);

  const dest = document.createElement("td");
  dest.textContent = link.url;
  dest.title = link.url;

  const clicks = document.createElement("td");
  clicks.textContent = link.clicks;

  const actions = document.createElement("td");
  const del = document.createElement("button");
  del.type = "button";
  del.textContent = "×";
  del.title = "Borrar";
  del.addEventListener("click", async () => {
    if (!confirm(`¿Borrar /${link.code}?`)) return;
    try {
      await api("DELETE", `/api/links/${encodeURIComponent(link.code)}`);
      tr.remove();
    } catch (err) {
      alert(err.message);
    }
  });
  actions.append(del);

  tr.append(short, dest, clicks, actions);
  return tr;
}
