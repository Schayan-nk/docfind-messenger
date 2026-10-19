// chatextrascss.js: styles of the phase 11 pieces (polls, locations, stickers
// and GIFs, scheduled messages, translation), added by chatextras.js. Dark,
// RTL; lists move with transform and opacity only.
export const EXTRAS_CSS = `
  .x-poll { display: grid; gap: 6px; min-width: min(300px, 70vw); padding: 2px 0 4px; }
  .x-poll-q { font-weight: 700; overflow-wrap: anywhere; }
  .x-poll-kind { font-size: 12px; color: rgb(255 255 255 / .62); }
  .x-opt { all: unset; box-sizing: border-box; cursor: pointer; position: relative; display: grid; grid-template-columns: 22px minmax(0, 1fr) auto; gap: 8px; align-items: center; padding: 7px 8px; border-radius: 10px; overflow: hidden; }
  .x-opt:hover { background: rgb(255 255 255 / .06); }
  .x-opt:focus-visible { outline: 2px solid var(--accent); }
  .x-opt[aria-disabled="true"] { cursor: default; }
  .x-opt[aria-disabled="true"]:hover { background: none; }
  .x-opt .x-dot { width: 18px; height: 18px; border-radius: 50%; border: 2px solid currentColor; opacity: .7; display: grid; place-items: center; }
  .x-opt.multi .x-dot { border-radius: 5px; }
  .x-opt.on .x-dot { background: var(--accent); border-color: var(--accent); opacity: 1; }
  .x-opt.on .x-dot::after { content: ''; width: 8px; height: 4px; border: 2px solid #fff; border-top: 0; border-inline-end: 0; transform: rotate(-45deg) translate(0, -1px); }
  .x-opt-text { overflow-wrap: anywhere; position: relative; z-index: 1; }
  .x-pct { font-size: 12.5px; font-weight: 700; font-variant-numeric: tabular-nums; position: relative; z-index: 1; }
  .x-bar { position: absolute; inset-block: 2px; inset-inline-start: 0; width: 100%; border-radius: 9px; background: rgb(255 255 255 / .1); transform-origin: right center; transform: scaleX(var(--p, 0)); transition: transform var(--dur-slow) var(--ease-out); }
  [dir="ltr"] .x-bar { transform-origin: left center; }
  .x-opt.mine .x-bar { background: rgb(127 209 255 / .22); }
  .x-opt.right .x-bar { background: rgb(76 175 80 / .32); }
  .x-opt.wrong .x-bar { background: rgb(229 115 115 / .32); }
  .x-poll-foot { display: flex; gap: 10px; align-items: center; flex-wrap: wrap; font-size: 12.5px; color: rgb(255 255 255 / .7); }
  .x-link { all: unset; cursor: pointer; color: #7FD1FF; font-size: 12.5px; }
  .x-link:focus-visible { outline: 2px solid var(--accent); border-radius: 4px; }
  .x-explain { font-size: 13px; padding: 6px 8px; border-radius: 8px; background: rgb(255 255 255 / .06); overflow-wrap: anywhere; }
  .x-form { display: grid; gap: 10px; padding: 4px 2px 12px; }
  .x-form .input { width: 100%; box-sizing: border-box; }
  .x-optrow { display: flex; gap: 6px; align-items: center; }
  .x-optrow .input { flex: 1; }
  .x-check { display: flex; gap: 8px; align-items: center; font-size: 14px; }
  .x-check input { width: 18px; height: 18px; accent-color: var(--accent); }

  .x-place { display: grid; gap: 4px; width: min(280px, 68vw); }
  .x-map { position: relative; height: 150px; border-radius: 12px; overflow: hidden; background: #1B3446; cursor: pointer; direction: ltr; }
  .x-map img { position: absolute; width: 256px; height: 256px; max-width: none; user-select: none; -webkit-user-drag: none; }
  .x-map.nogrid { background-image: linear-gradient(rgb(255 255 255 / .07) 1px, transparent 1px), linear-gradient(90deg, rgb(255 255 255 / .07) 1px, transparent 1px); background-size: 24px 24px; }
  .x-pin { position: absolute; left: 50%; top: 50%; width: 30px; height: 30px; transform: translate(-50%, -100%); color: #E5484D; filter: drop-shadow(0 2px 3px rgb(0 0 0 / .5)); pointer-events: none; }
  .x-pin svg { width: 30px; height: 30px; }
  .x-live-dot { position: absolute; left: 50%; top: 50%; width: 14px; height: 14px; margin: -7px 0 0 -7px; border-radius: 50%; background: #7FD1FF; box-shadow: 0 0 0 3px #fff; }
  .x-live-dot::after { content: ''; position: absolute; inset: -10px; border-radius: 50%; border: 2px solid #7FD1FF; animation: xping 1.8s var(--ease-out) infinite; }
  @keyframes xping { from { transform: scale(.4); opacity: 1; } to { transform: scale(1.4); opacity: 0; } }
  .x-coord { font-size: 12px; color: rgb(255 255 255 / .62); direction: ltr; text-align: end; font-variant-numeric: tabular-nums; }
  .x-live { font-size: 12.5px; display: flex; gap: 6px; align-items: center; }
  .x-live b { color: #7FD1FF; }
  .x-bigmap { height: min(60vh, 460px); border-radius: 12px; overflow: hidden; direction: ltr; position: relative; z-index: 0; isolation: isolate; }
  .x-center-pin { position: absolute; left: 50%; top: 50%; z-index: 500; pointer-events: none; width: 34px; height: 34px; transform: translate(-50%, -100%); color: #E5484D; }
  .x-center-pin svg { width: 34px; height: 34px; }

  .m-bubble.x-bare { background: none; box-shadow: none; padding: 0; }
  .m-bubble.x-bare .m-text { float: none; }
  .m-bubble.x-bare .m-meta { float: none; margin: 2px 0 0; padding: 1px 8px; border-radius: 10px; background: rgb(0 0 0 / .4); }
  .x-sticker { width: 180px; height: 180px; display: grid; place-items: center; cursor: pointer; }
  .x-sticker img, .x-sticker video, .x-sticker svg { max-width: 100%; max-height: 100%; object-fit: contain; }
  .x-gif { max-width: min(280px, 70vw); border-radius: 12px; overflow: hidden; display: block; cursor: pointer; }
  .x-gif img, .x-gif video { display: block; width: 100%; height: auto; }

  .x-panel { position: absolute; bottom: 100%; inset-inline: 0; height: min(340px, 50vh); display: grid; grid-template-rows: auto minmax(0, 1fr); background: var(--surface); border-top: 1px solid var(--line); z-index: 4; }
  .x-panel[hidden] { display: none; }
  .x-tabs { display: flex; gap: 4px; padding: 6px 10px; border-bottom: 1px solid var(--line); align-items: center; }
  .x-tab { all: unset; cursor: pointer; padding: 6px 14px; border-radius: 16px; font-size: 13.5px; color: var(--fg-muted); }
  .x-tab[aria-selected="true"] { background: var(--surface-2); color: var(--fg); }
  .x-tab:focus-visible { outline: 2px solid var(--accent); }
  .x-tabs .x-sp { flex: 1; }
  .x-scroll { overflow-y: auto; padding: 8px 10px 12px; overscroll-behavior: contain; }
  .x-pack-title { font-size: 12.5px; color: var(--fg-muted); margin: 10px 4px 6px; }
  .x-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(76px, 1fr)); gap: 6px; }
  .x-grid.gifs { grid-template-columns: repeat(auto-fill, minmax(120px, 1fr)); }
  .x-tile { all: unset; box-sizing: border-box; cursor: pointer; aspect-ratio: 1; border-radius: 10px; display: grid; place-items: center; overflow: hidden; background: rgb(255 255 255 / .03); position: relative; }
  .x-tile:hover { background: rgb(255 255 255 / .08); }
  .x-tile:focus-visible { outline: 2px solid var(--accent); }
  .x-tile img, .x-tile video, .x-tile svg { max-width: 92%; max-height: 92%; object-fit: contain; pointer-events: none; }
  .x-grid.gifs .x-tile { aspect-ratio: 4 / 3; }
  .x-grid.gifs .x-tile img, .x-grid.gifs .x-tile video { max-width: 100%; max-height: 100%; width: 100%; height: 100%; object-fit: cover; }
  .x-tile .x-del { position: absolute; top: 2px; inset-inline-end: 2px; width: 22px; height: 22px; border-radius: 50%; background: rgb(0 0 0 / .6); color: #fff; display: grid; place-items: center; font-size: 14px; pointer-events: auto; }
  .x-empty { color: var(--fg-muted); text-align: center; padding: 24px 8px; font-size: 13.5px; }
  .x-preview { position: fixed; inset: 0; z-index: calc(var(--z-popover) + 3); display: grid; place-items: center; background: rgb(4 12 20 / .55); -webkit-backdrop-filter: blur(8px); backdrop-filter: blur(8px); }
  .x-preview > div { width: min(320px, 80vw); height: min(320px, 80vw); display: grid; place-items: center; }
  .x-preview img, .x-preview video, .x-preview svg { max-width: 100%; max-height: 100%; }

  .x-sched-btn { position: relative; color: var(--accent); }
  .x-sched-btn .m-badge { position: absolute; top: -2px; inset-inline-end: -2px; min-width: 18px; height: 18px; font-size: 11px; }
  .x-sched-btn svg { animation: xpulse 60s infinite; }
  @keyframes xpulse { 0%, 1.2%, 100% { transform: none; } .6% { transform: scale(1.18); } }
  .x-presets { display: flex; gap: 6px; flex-wrap: wrap; }
  .x-chip { all: unset; cursor: pointer; padding: 6px 12px; border-radius: 16px; background: var(--surface-2); font-size: 13px; }
  .x-chip[aria-pressed="true"] { background: var(--accent); color: #fff; }
  .x-chip:focus-visible { outline: 2px solid var(--accent); }
  .x-when { font-size: 13px; color: var(--fg-muted); min-height: 1.4em; }
  .x-sched-item { display: grid; gap: 4px; padding: 10px 4px; border-bottom: 1px solid var(--line); }
  .x-sched-item .x-sched-at { font-size: 12.5px; color: var(--accent); }
  .x-sched-item .x-sched-text { overflow-wrap: anywhere; white-space: pre-wrap; }
  .x-sched-acts { display: flex; gap: 12px; }
  .x-tr { white-space: pre-wrap; overflow-wrap: anywhere; line-height: 1.7; max-height: 50vh; overflow-y: auto; }
  .x-langs { display: flex; gap: 6px; }

  @media (prefers-reduced-motion: reduce) {
    .x-bar { transition: none; }
    .x-live-dot::after, .x-sched-btn svg { animation: none; }
  }
`;
