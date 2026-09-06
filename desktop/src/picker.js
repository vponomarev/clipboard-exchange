'use strict';

const container = document.getElementById('messages');
let messages = [];
let active = 0;

function activate(index) {
  if (!messages.length) return;
  active = Math.max(0, Math.min(messages.length - 1, index));
  const buttons = Array.from(container.querySelectorAll('button'));
  buttons.forEach((button, position) => button.classList.toggle('active', position === active));
  buttons[active]?.scrollIntoView({ block: 'nearest' });
}

async function paste(index) {
  await window.desktopPicker.paste(index);
}

window.desktopPicker.onMessages((next) => {
  if (!Array.isArray(next)) {
    messages = [];
    container.replaceChildren();
    const loading=document.createElement('p'); loading.className='empty'; loading.textContent='Загрузка…'; container.append(loading);
    return;
  }
  messages = next;
  active = 0;
  container.replaceChildren();
  if (!messages.length) { const empty=document.createElement('p'); empty.className='empty'; empty.textContent='В комнате пока нет текстовых сообщений'; container.append(empty); return; }
  messages.forEach((message, index) => {
    const button=document.createElement('button'); button.type='button'; button.setAttribute('role','option');
    const text=document.createElement('span'); text.className='text'; text.textContent=message.text;
    const alias=document.createElement('span'); alias.className='alias'; alias.textContent=message.alias || 'Без имени';
    const time=document.createElement('time'); time.className='time'; time.dateTime=message.createdAt; time.textContent=new Intl.DateTimeFormat(undefined,{dateStyle:'short',timeStyle:'short'}).format(new Date(message.createdAt));
    button.append(text,alias,time); button.addEventListener('click',()=>void paste(index)); container.append(button);
  });
  activate(0);
});

addEventListener('keydown', (event) => {
  if (event.key === 'Escape') { event.preventDefault(); window.desktopPicker.close(); }
  else if (event.key === 'ArrowDown') { event.preventDefault(); activate(active+1); }
  else if (event.key === 'ArrowUp') { event.preventDefault(); activate(active-1); }
  else if (event.key === 'Enter') { event.preventDefault(); void paste(active); }
});
