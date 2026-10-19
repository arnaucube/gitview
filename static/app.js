'use strict';
// Search and sorting operate on the server-rendered repository list.
const search = document.querySelector('#repo-search');
const globalSearch = document.querySelector('#global-search');
const list = document.querySelector('#repo-list');
const cards = [...document.querySelectorAll('[data-repo]')];
function filterRepositories(value) {
  const query = value.trim().toLowerCase();
  let count = 0;
  for (const card of cards) {
    card.hidden = !card.textContent.toLowerCase().includes(query);
    if (!card.hidden) count++;
  }
  const counter = document.querySelector('#result-count');
  if (counter) counter.textContent = count;
  const empty = document.querySelector('#no-results');
  if (empty) empty.hidden = count !== 0 || !query;
  if (list) list.hidden = count === 0 && !!query;
}
search?.addEventListener('input', () => { if (globalSearch) globalSearch.value = search.value; filterRepositories(search.value); });
globalSearch?.addEventListener('input', () => {
  if (search) { search.value = globalSearch.value; filterRepositories(globalSearch.value); }
});
// On repository pages, Enter carries the search back to the workspace.
globalSearch?.addEventListener('keydown', event => {
  if (event.key === 'Enter' && !search) location.href = '/?q=' + encodeURIComponent(globalSearch.value);
});
const initialQuery = new URLSearchParams(location.search).get('q');
if (initialQuery && search) { search.value = initialQuery; globalSearch.value = initialQuery; filterRepositories(initialQuery); }
document.querySelector('#repo-sort')?.addEventListener('change', event => {
  const byName = event.target.value === 'name';
  cards.sort((a, b) => byName ? a.dataset.repo.localeCompare(b.dataset.repo) : Number(a.dataset.index) - Number(b.dataset.index));
  for (const card of cards) list.append(card);
  document.querySelector('#sort-description').textContent = byName ? 'name' : event.target.selectedOptions[0].dataset.description;
});
document.querySelector('#view-toggle')?.addEventListener('click', event => {
  const compact = list.classList.toggle('compact');
  event.currentTarget.setAttribute('aria-pressed', String(compact));
});
document.querySelector('#theme-toggle')?.addEventListener('click', () => {
  const theme = document.documentElement.dataset.theme === 'dark' ? 'light' : 'dark';
  document.documentElement.dataset.theme = theme;
  try { localStorage.setItem('gitview-theme', theme); } catch (_) { /* Theme still works without storage. */ }
});
document.addEventListener('keydown', event => {
  if (event.key === '/' && !/INPUT|TEXTAREA|SELECT/.test(document.activeElement.tagName)) {
    event.preventDefault(); (search || globalSearch)?.focus();
  }
  if (event.key === 'Escape') document.querySelectorAll('details[open]').forEach(item => item.open = false);
});
document.addEventListener('click', event => {
  document.querySelectorAll('details[open]').forEach(item => { if (!item.contains(event.target)) item.open = false; });
});
for (const input of document.querySelectorAll('[data-clone]')) input.value = location.origin + input.dataset.clone;
let toastTimer;
function toast(message) {
  const element = document.querySelector('#toast');
  element.textContent = message;
  element.classList.add('visible');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => element.classList.remove('visible'), 2500);
}
for (const button of document.querySelectorAll('[data-copy]')) button.addEventListener('click', async () => {
  const input = document.getElementById(button.dataset.copy);
  try { await navigator.clipboard.writeText(input.value); toast('Clone URL copied'); }
  catch (_) { input.focus(); input.select(); toast('Select and copy the clone URL'); }
});
document.querySelector('[data-scroll-readme]')?.addEventListener('click', event => {
  event.preventDefault();
  const readme = document.querySelector('.readme-panel');
  if (readme) readme.scrollIntoView({ behavior: 'smooth' }); else toast('No README in this repository');
});
