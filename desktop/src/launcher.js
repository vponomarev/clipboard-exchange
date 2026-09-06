'use strict';

const form = document.getElementById('connect-form');
const input = document.getElementById('server-url');
const error = document.getElementById('error');
const initialError = new URLSearchParams(location.search).get('error');
if (initialError) error.textContent = initialError;

form.addEventListener('submit', async (event) => {
  event.preventDefault();
  error.textContent = '';
  const button = form.querySelector('button');
  button.disabled = true;
  try {
    await window.clipboardExchangeDesktop.connect(input.value);
  } catch (problem) {
    error.textContent = problem.message || 'Не удалось подключиться';
    button.disabled = false;
    input.select();
  }
});
