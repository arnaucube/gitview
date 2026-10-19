'use strict';
// Submit search and sorting to the server so they include every repository page.
const search = document.querySelector('#repo-search');
const globalSearch = document.querySelector('#global-search');
const list = document.querySelector('#repo-list');
search?.addEventListener('input', () => { if (globalSearch) globalSearch.value = search.value; });
globalSearch?.addEventListener('input', () => { if (search) search.value = globalSearch.value; });
document.querySelector('#repo-sort')?.addEventListener('change', event => {
  event.target.form.requestSubmit();
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
