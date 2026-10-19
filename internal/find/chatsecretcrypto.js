// chatsecretcrypto.js: the cryptography of secret chats and the store that
// keeps them on this device (docs/chat/SECRET_CHATS.md). Everything here is
// Web Crypto; no key ever leaves the device. Private keys and message keys
// are non-extractable CryptoKey objects kept in IndexedDB; messages are kept
// sealed, exactly as they travelled, and opened only to be drawn.
//
// Scheme (version 1):
//   agreement  X25519 where the browser has it, ECDH P-256 otherwise
//   keys       HKDF-SHA-256 over the shared secret, salt = SHA-256(pubI || pubR),
//              info "docfind secret v1 i2r" / "... r2i" -> two AES-256-GCM keys,
//              one per direction; "... fingerprint" -> 32 bytes shown to people
//   nonce      12 bytes: direction (1 = inviter to invited, 2 = back), 3 zero
//              bytes, 8-byte big-endian counter. Each direction has its own key
//              and its own counter, from 1, stored before use, never reused.
//   aad        "docfind-secret/v1/<chat>/<direction>"
//   body       base64url(counter || ciphertext || tag); the plaintext is JSON,
//              padded with spaces to a multiple of 64 bytes.
const te = new TextEncoder(), td = new TextDecoder();

export const b64u = bytes => btoa(String.fromCharCode(...new Uint8Array(bytes))).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
export function unb64u(s) {
  const bin = atob(String(s).replace(/-/g, '+').replace(/_/g, '/'));
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}
const hex = bytes => [...new Uint8Array(bytes)].map(b => b.toString(16).padStart(2, '0')).join('');
const cat = (a, b) => { const o = new Uint8Array(a.length + b.length); o.set(a); o.set(b, a.length); return o; };

export const supported = () => !!(window.isSecureContext && crypto.subtle && window.indexedDB);

const ALG = { x25519: { name: 'X25519' }, p256: { name: 'ECDH', namedCurve: 'P-256' } };
let x25519Known;
async function hasX25519() {
  if (x25519Known === undefined) {
    try { await crypto.subtle.generateKey(ALG.x25519, false, ['deriveBits']); x25519Known = true; } catch { x25519Known = false; }
  }
  return x25519Known;
}

// keyPair makes one non-extractable private key and returns it with the raw public key.
async function keyPair(alg) {
  const kp = await crypto.subtle.generateKey(ALG[alg], false, ['deriveBits']);
  return { priv: kp.privateKey, pub: b64u(await crypto.subtle.exportKey('raw', kp.publicKey)) };
}

// offerKeys is what an invitation carries: P-256 always, X25519 besides
// where this browser has it, so the invited browser can always answer.
export async function offerKeys() {
  const priv = {}, pub = {};
  for (const alg of (await hasX25519()) ? ['x25519', 'p256'] : ['p256']) {
    const k = await keyPair(alg); priv[alg] = k.priv; pub[alg] = k.pub;
  }
  return { priv, pub, list: Object.entries(pub).map(([alg, key]) => ({ alg, key })) };
}

// answerKey picks the strongest offered algorithm this browser can use.
export async function answerKey(offer) {
  const algs = (offer || []).map(k => k.alg);
  const alg = algs.includes('x25519') && (await hasX25519()) ? 'x25519' : 'p256';
  if (!algs.includes(alg)) throw new Error('no common algorithm');
  return { alg, ...(await keyPair(alg)) };
}

// derive turns my private key and the other public key into the two message
// keys and the fingerprint. pubI/pubR are the inviter's and the invited's.
export async function derive(alg, priv, peerPub, pubI, pubR) {
  const peer = await crypto.subtle.importKey('raw', unb64u(peerPub), ALG[alg], true, []);
  const shared = await crypto.subtle.deriveBits({ name: ALG[alg].name, public: peer }, priv, 256);
  const ikm = await crypto.subtle.importKey('raw', shared, 'HKDF', false, ['deriveKey', 'deriveBits']);
  const salt = new Uint8Array(await crypto.subtle.digest('SHA-256', cat(unb64u(pubI), unb64u(pubR))));
  const hk = info => ({ name: 'HKDF', hash: 'SHA-256', salt, info: te.encode('docfind secret v1 ' + info) });
  const aes = info => crypto.subtle.deriveKey(hk(info), ikm, { name: 'AES-GCM', length: 256 }, false, ['encrypt', 'decrypt']);
  return { i2r: await aes('i2r'), r2i: await aes('r2i'), fp: new Uint8Array(await crypto.subtle.deriveBits(hk('fingerprint'), ikm, 256)) };
}

function nonce(dir, ctr) {
  const n = new Uint8Array(12); const v = new DataView(n.buffer);
  n[0] = dir; v.setBigUint64(4, BigInt(ctr));
  return n;
}
const aad = (chat, dir) => te.encode(`docfind-secret/v1/${chat}/${dir}`);

// seal encrypts one JSON object as message number ctr of direction dir.
export async function seal(key, dir, chat, ctr, obj) {
  const json = te.encode(JSON.stringify(obj));
  const plain = new Uint8Array(Math.ceil((json.length + 1) / 64) * 64).fill(0x20);
  plain.set(json);
  const ct = new Uint8Array(await crypto.subtle.encrypt({ name: 'AES-GCM', iv: nonce(dir, ctr), additionalData: aad(chat, dir) }, key, plain));
  const out = new Uint8Array(8 + ct.length);
  new DataView(out.buffer).setBigUint64(0, BigInt(ctr));
  out.set(ct, 8);
  return b64u(out);
}

export function counterOf(body) {
  const raw = unb64u(body);
  return raw.length < 25 ? 0 : Number(new DataView(raw.buffer).getBigUint64(0));
}

// open decrypts a body sealed by seal; it throws on any change to it.
export async function open(key, dir, chat, body) {
  const raw = unb64u(body);
  const ctr = Number(new DataView(raw.buffer).getBigUint64(0));
  const plain = await crypto.subtle.decrypt({ name: 'AES-GCM', iv: nonce(dir, ctr), additionalData: aad(chat, dir) }, key, raw.subarray(8));
  return { ctr, obj: JSON.parse(td.decode(plain)) };
}

// ------------------------------------------------------------ fingerprint
export function fingerprintHex(fp) {
  const h = hex(fp).toUpperCase().match(/.{4}/g);
  return [0, 4, 8, 12].map(i => h.slice(i, i + 4).join(' ')).join('\n');
}

// fingerprintImage draws the first 128 bits as an 8 by 8 grid of four shades.
export function fingerprintImage(fp, size = 192) {
  const c = document.createElement('canvas');
  c.width = c.height = size; c.setAttribute('role', 'img');
  const g = c.getContext('2d'); const cell = size / 8;
  const shades = ['#FFFFFF', '#D5E6F3', '#2D5775', '#2F99C9'];
  for (let i = 0; i < 64; i++) {
    g.fillStyle = shades[(fp[i >> 2] >> ((i & 3) * 2)) & 3];
    g.fillRect((i % 8) * cell, Math.floor(i / 8) * cell, cell, cell);
  }
  return c;
}

// ------------------------------------------------------------ the store
export class SecretStore {
  constructor(user) { this.name = 'docfind-secret:' + user; this.db = null; }
  async open() {
    this.db = await new Promise((ok, fail) => {
      const rq = indexedDB.open(this.name, 1);
      rq.onupgradeneeded = () => {
        const db = rq.result;
        db.createObjectStore('meta', { keyPath: 'k' });
        db.createObjectStore('chats', { keyPath: 'chat' });
        db.createObjectStore('msgs', { keyPath: ['chat', 'key'] }).createIndex('chat', 'chat');
        db.createObjectStore('outbox', { keyPath: 'cid' }).createIndex('chat', 'chat');
      };
      rq.onsuccess = () => ok(rq.result); rq.onerror = () => fail(rq.error);
    });
    return this;
  }
  tx(stores, mode, fn) {
    return new Promise((ok, fail) => {
      const t = this.db.transaction(stores, mode); let out;
      Promise.resolve(fn(t)).then(v => { out = v; });
      t.oncomplete = () => ok(out); t.onerror = () => fail(t.error); t.onabort = () => fail(t.error);
    });
  }
  req(r) { return new Promise((ok, fail) => { r.onsuccess = () => ok(r.result); r.onerror = () => fail(r.error); }); }
  async device() {
    return this.tx(['meta'], 'readwrite', async t => {
      const s = t.objectStore('meta'); const cur = await this.req(s.get('device'));
      if (cur) return cur.v;
      const v = hex(crypto.getRandomValues(new Uint8Array(16)));
      s.put({ k: 'device', v }); return v;
    });
  }
  chats() { return this.tx(['chats'], 'readonly', t => this.req(t.objectStore('chats').getAll())); }
  // putChat never moves a counter back: the copy in memory may be older than
  // what nextCounter and takeIncoming have stored since.
  putChat(rec) {
    return this.tx(['chats'], 'readwrite', async t => {
      const s = t.objectStore('chats'); const cur = await this.req(s.get(rec.chat));
      if (cur) { rec.sendCtr = Math.max(rec.sendCtr || 0, cur.sendCtr || 0); rec.recvCtr = cur.recvCtr || 0; rec.recvSeen = cur.recvSeen || []; }
      s.put(rec);
    });
  }
  // nextCounter takes the next sending number inside one transaction, so two
  // tabs of this device never seal two messages under the same nonce.
  nextCounter(chat) {
    return this.tx(['chats'], 'readwrite', async t => {
      const s = t.objectStore('chats'); const rec = await this.req(s.get(chat));
      rec.sendCtr = (rec.sendCtr || 0) + 1; s.put(rec); return rec.sendCtr;
    });
  }
  // takeIncoming stores a received message unless its number was seen before
  // (a replay or a resend). Each number is taken once, within a window of the
  // 64 numbers below the highest, so two messages that overtook each other
  // both arrive; anything older than the window is refused.
  takeIncoming(chat, ctr, msg) {
    return this.tx(['chats', 'msgs'], 'readwrite', async t => {
      const s = t.objectStore('chats'); const rec = await this.req(s.get(chat));
      const high = (rec && rec.recvCtr) || 0, seen = (rec && rec.recvSeen) || [];
      if (!rec || ctr <= high - 64 || seen.includes(ctr)) return false;
      rec.recvCtr = Math.max(high, ctr);
      rec.recvSeen = [...seen, ctr].filter(n => n > rec.recvCtr - 64);
      s.put(rec);
      if (msg) t.objectStore('msgs').put(msg);
      return true;
    });
  }
  msgs(chat) { return this.tx(['msgs'], 'readonly', t => this.req(t.objectStore('msgs').index('chat').getAll(chat))); }
  putMsgs(list) { return this.tx(['msgs'], 'readwrite', t => { for (const m of list) t.objectStore('msgs').put(m); }); }
  dropMsgs(chat, keys) { return this.tx(['msgs'], 'readwrite', t => { for (const k of keys) t.objectStore('msgs').delete([chat, k]); }); }
  outbox() { return this.tx(['outbox'], 'readonly', t => this.req(t.objectStore('outbox').getAll())); }
  putOut(item) { return this.tx(['outbox'], 'readwrite', t => { t.objectStore('outbox').put(item); }); }
  dropOut(cid) { return this.tx(['outbox'], 'readwrite', t => { t.objectStore('outbox').delete(cid); }); }
  // wipe forgets a chat: its keys, its messages and anything unsent.
  wipe(chat) {
    return this.tx(['chats', 'msgs', 'outbox'], 'readwrite', async t => {
      t.objectStore('chats').delete(chat);
      for (const st of ['msgs', 'outbox']) {
        const keys = await this.req(t.objectStore(st).index('chat').getAllKeys(chat));
        for (const k of keys) t.objectStore(st).delete(k);
      }
    });
  }
}
