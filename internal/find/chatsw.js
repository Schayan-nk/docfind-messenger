// chatsw.js: the messenger's part of the service worker (docs/chat/SPEC.md
// 6.10), loaded by sw.js with importScripts. One notification per chat with a
// count rather than one per message; «پاسخ» typed into the notification and
// «خوانده شد» without opening the app; the app badge. Nothing is shown while
// the messenger itself is on screen: the page alerts in-app.
(function () {
  'use strict';
  const fa = n => Number(n).toLocaleString('fa-IR');
  const chatOf = url => { const m = /#c(\d+)/.exec(url || ''); return m ? Number(m[1]) : 0; };

  // badge: the app icon's number, the messages waiting in chat notifications.
  async function badge() {
    if (!self.navigator.setAppBadge) return;
    const all = await self.registration.getNotifications();
    const n = all.filter(x => x.data && x.data.kind === 'chat').reduce((a, x) => a + (x.data.count || 1), 0);
    try { n ? await self.navigator.setAppBadge(n) : await self.navigator.clearAppBadge(); } catch (_) { }
  }

  // chatPush shows a chat's notification; false for anything that is not one.
  self.chatPush = (event, note) => {
    const data = (note && note.data) || {};
    if (data.kind !== 'chat') return false;
    event.waitUntil((async () => {
      const url = data.url || note.navigate || '/messages';
      const wins = await self.clients.matchAll({ type: 'window', includeUncontrolled: true });
      if (wins.some(w => w.focused && w.visibilityState === 'visible' && new URL(w.url).pathname === '/messages')) return;
      const tag = note.tag || 'chat-' + chatOf(url);
      const old = (await self.registration.getNotifications({ tag }))[0];
      const count = ((old && old.data && old.data.count) || 0) + 1;
      await self.registration.showNotification(note.title || 'پیام تازه', {
        body: count > 1 ? `${note.body || ''}\n${fa(count)} پیام تازه` : (note.body || ''),
        tag, renotify: true, silent: !!note.silent,
        icon: '/icon-192.png', badge: '/icon-192.png', lang: 'fa', dir: 'rtl',
        data: { url, kind: 'chat', chat: chatOf(url), count },
        actions: [
          { action: 'reply', type: 'text', title: 'پاسخ', placeholder: 'پاسخ…' },
          { action: 'read', title: 'خوانده شد' },
        ],
      });
      await badge();
    })());
    return true;
  };

  const post = (path, body) => fetch(path, {
    method: 'POST', credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body || {}),
  });

  // chatClick: the two buttons act without opening the app; false lets sw.js
  // open the chat (a press on the notification, or «پاسخ» where the platform
  // cannot take typed text).
  self.chatClick = event => {
    const d = event.notification.data || {};
    if (d.kind !== 'chat' || !d.chat) return false;
    const reply = event.action === 'reply' && event.reply;
    if (!reply && event.action !== 'read') return false;
    event.notification.close();
    event.waitUntil((async () => {
      try {
        if (reply) await post(`/api/chat/chats/${d.chat}/reply`, { text: event.reply });
        else await post(`/api/chat/chats/${d.chat}/read`);
      } catch (_) { }
      await badge();
    })());
    return true;
  };

  // The page reads a chat: its notification goes, and the badge follows.
  self.addEventListener('message', e => {
    if (!e.data || e.data.type !== 'chat-read') return;
    e.waitUntil((async () => {
      for (const n of await self.registration.getNotifications({ tag: 'chat-' + e.data.chat })) n.close();
      await badge();
    })());
  });
})();
