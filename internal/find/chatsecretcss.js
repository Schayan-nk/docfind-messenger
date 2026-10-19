// chatsecretcss.js: the secret chats' styles, added to the messenger page by
// chatsecretpane.js. Dark only, RTL; the secret colour is the page's own green.
export const SECRET_CSS = `
  .m-lock { display: inline-grid; place-items: center; vertical-align: -2px; margin-inline-end: 4px; color: var(--ok-ink); }
  .m-lock svg { width: 14px; height: 14px; }
  .m-row.m-secret .m-row-name { color: var(--ok-ink); }
  .m-row.m-secret .m-row-last.m-invite { color: var(--ok-ink); }
  .m-secret-intro { align-self: center; max-width: min(360px, 92%); margin: 10px 0 14px; padding: 14px 16px; border-radius: 14px; background: rgb(0 0 0 / .35); color: var(--fg); font-size: 13.5px; line-height: 1.8; }
  .m-secret-intro b { display: flex; align-items: center; justify-content: center; gap: 6px; margin-bottom: 6px; color: var(--ok-ink); }
  .m-secret-intro .m-lock svg { width: 16px; height: 16px; }
  .m-secret-intro ul { margin: 0; padding-inline-start: 18px; color: var(--fg-muted); }
  .m-secret-note { align-self: center; margin: 6px 0; padding: 3px 12px; border-radius: 12px; background: rgb(0 0 0 / .35); font-size: 12.5px; color: var(--fg-muted); text-align: center; }
  .m-secret-msgs .m-bubble { -webkit-user-drag: none; }
  .m-ttl { display: inline-grid; width: 14px; height: 14px; }
  .m-ttl svg { width: 14px; height: 14px; transform: rotate(-90deg); }
  .m-ttl circle { fill: none; stroke-width: 2.6; }
  .m-ttl .bg { stroke: rgb(255 255 255 / .22); }
  .m-ttl .fg { stroke: currentColor; stroke-dasharray: 100; stroke-dashoffset: 0; }
  .m-ttl-btn { position: relative; flex: none; width: 44px; height: 44px; }
  .m-ttl-btn.on { color: var(--ok-ink); }
  .m-ttl-n { position: absolute; bottom: 3px; inset-inline-start: 2px; min-width: 14px; font-size: 10px; font-weight: 700; line-height: 1; color: var(--ok-ink); }
  .m-secret-foot[hidden] { display: none; }
  .m-fp canvas, .m-modal > canvas { justify-self: center; border-radius: 10px; image-rendering: pixelated; }
  .m-fp-hex { margin: 0; text-align: center; font: 600 14px/1.7 ui-monospace, Consolas, monospace; color: var(--fg); letter-spacing: .04em; white-space: pre; }
  body.m-secret-hide .m-secret-msgs { filter: blur(18px); }
  .m-new-card { grid-template-rows: auto auto auto minmax(0, 1fr); }
  .m-secret-toggle { all: unset; box-sizing: border-box; cursor: pointer; display: flex; align-items: center; gap: 8px; padding: 8px 12px; border-radius: 10px; background: var(--surface-2); color: var(--fg-muted); font-size: 13.5px; transition: background var(--dur-fast) var(--ease-out), color var(--dur-fast) var(--ease-out); }
  .m-secret-toggle svg { width: 18px; height: 18px; flex: none; }
  .m-secret-toggle[aria-pressed="true"] { color: var(--ok-ink); background: var(--surface-3); }
  .m-secret-toggle:focus-visible { outline: 2px solid var(--accent); }
  .m-new.m-secret-mode #m-people > .m-row:first-child { display: none; }
  @media (prefers-reduced-motion: reduce) { .m-secret-toggle { transition: none; } }
`;
