'use strict';

window.desktopLatest.onMessage((message) => {
  document.getElementById('text').textContent = message.text;
  document.getElementById('alias').textContent = message.alias || 'Без имени';
  const time=document.getElementById('time'); time.dateTime=message.createdAt; time.textContent=new Intl.DateTimeFormat(undefined,{dateStyle:'short',timeStyle:'short'}).format(new Date(message.createdAt));
});
document.getElementById('close').addEventListener('click', () => window.desktopLatest.close());
