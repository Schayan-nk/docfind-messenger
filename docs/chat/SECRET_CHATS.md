# Secret chats: how they work, what they protect, and what they do not

Phase 10 of docs/chat/SPEC.md (section 6.12). Written for the people who run DocFind and for anyone
who has to decide how much to trust a secret chat. The limits are stated plainly on purpose.

## In one paragraph

A secret chat is a conversation between one device of one person and one device of another. The two
browsers agree on keys with each other; the DocFind server only introduces them and carries sealed
messages it cannot open. Each sealed message waits on the server only until the receiving device
confirms it, then the server deletes it. Nothing is synced to the person's other devices. A message
can carry a self-destruct timer. Nothing can be forwarded out of a secret chat.

## How it works

### Devices

The first time a browser uses secret chats it makes a device id (128 random bits) and keeps it in its
own IndexedDB database, `docfind-secret:<username>`, separate from the messenger's cache. After each
connection the page sends `secret_hello {device}`; from then on the socket speaks for that device and
receives only what is addressed to it. A device id is a routing label, not a secret: claiming another
device's id gets nothing, because every frame is also bound to the account and every body is sealed
with keys that only the real device holds.

### Starting a chat

1. Device A makes a key pair for P-256 (ECDH) and, when its browser has it, X25519. The private keys
   are created **non-extractable** and stored in IndexedDB as `CryptoKey` objects. A sends
   `secret_open {peer, keys}` with the public keys only.
2. The server checks the person exists, is not A, and has not blocked A (or been blocked), validates
   each public key with Go's `crypto/ecdh`, and stores the chat (`chats.type = 'secret'`, both people
   as members), the record in `secret_chats`, and the public keys with their SHA-256 fingerprints in
   `secret_chat_keys`. The invitation waits for **any** device of B.
3. B's devices that are online show the invitation in the chat list. The device on which B opens it
   accepts it: it picks X25519 if both sides have it, otherwise P-256, makes its own key pair and
   sends `secret_accept {chat, key}`. The server binds the chat to that device. If another of B's
   devices tries later, it is refused and told the chat is taken (`gone`).
4. A receives B's public key. Both sides now compute the same keys (below) and drop their private
   key: it is not needed again.

### Keys and the message format (version 1)

- **Agreement:** X25519, or ECDH on P-256 when a browser lacks X25519 in Web Crypto (X25519 arrived
  in Chrome 133, Firefox 130 and Safari 17). The inviting device always offers P-256 so any browser
  can answer.
- **Derivation:** HKDF-SHA-256 over the 32-byte shared secret, with salt = SHA-256(inviter's public
  key || invited's public key). Three outputs: `info = "docfind secret v1 i2r"` (AES-256-GCM key,
  inviter to invited), `"... r2i"` (the other direction) and `"... fingerprint"` (32 bytes shown to
  people). Both message keys are non-extractable `CryptoKey` objects kept in IndexedDB.
- **Nonce and counter:** AES-GCM with a 96-bit nonce = 1 byte direction (1 inviter to invited,
  2 back) + 3 zero bytes + an 8-byte big-endian counter. Each direction has its own key and its own
  counter, starting at 1. The sender takes the next counter inside an IndexedDB transaction and
  stores it **before** encrypting, so a crash or two open tabs can never seal two messages under the
  same nonce. A key and nonce pair is therefore never reused.
- **Associated data:** `docfind-secret/v1/<chat id>/<direction>`. A body moved to another chat or
  turned around fails to open.
- **Body on the wire:** base64url of `counter (8 bytes) || ciphertext || tag (16 bytes)`. The
  plaintext is a small JSON object padded with spaces to a multiple of 64 bytes, so the server sees
  length only in 64-byte steps.
- **Replay:** the receiver accepts each counter once. It keeps the highest counter it has accepted
  and the counters seen in the 64 below it (the window IPsec uses), so two messages that overtook each
  other both arrive, a resend after a lost answer is dropped if it already arrived, and anything older
  than the window is refused. The sender seals one message at a time and its outbox leaves in counter
  order, so overtaking is rare to begin with.

Inside the sealed JSON, `t` says what it is: `text` (with the timer in force), `read` (my messages
up to counter N are read), `del` (delete these messages on both devices), `ttl` (the chat's timer
changed) and `shot` (a screenshot was noticed). The server cannot tell these apart.

### What the server stores

| Table | What | How long |
|---|---|---|
| `secret_chats` | the two people, the two device ids, the algorithm, the state | until both sides have left a closed chat |
| `secret_chat_keys` | public keys and their SHA-256 fingerprints | until the chat is closed |
| `secret_queue` | one row per frame waiting for one device: the sealed body, or the public values of an invitation, acceptance or close | until that device confirms it (`secret_ack`); a frame no device fetched is dropped after 30 days |

No private key, no shared secret and no plaintext ever reaches the server. There is no message history
on the server for a secret chat: `chat_messages` and `chat_updates` never hold a row for one, so the
ordinary history, search and sync paths have nothing to show.

### The server's rules (internal/chat/secret.go)

- Every frame checks membership first (`Role`), then that the socket's device is the chat's device.
- `secret_send` is decoded strictly: a frame with any field besides `chat`, `cid` and `body` (for
  example `text` or `entities`) is refused. The body must be base64url (no padding), decode to between
  25 bytes and 16 KiB, and start with a counter of at least 1. The server cannot prove a body is
  ciphertext; it refuses everything that does not have the shape of one.
- The ordinary `send` refuses a secret chat (no plain text can enter one), the server does not keep
  drafts for one, and `forward` refuses a secret chat as its source (unchanged from phase 2).
- Logging: the module logs a failure by operation and error class (`sqlstate 23505`, a Go type)
  only. It never logs a frame, a body, a key, or the database's own error message, which can quote a
  row. A test checks this both by running failures and by reading the source.
- Rate: invitations and messages count against the same 20 per second limit as ordinary messages.

### Self-destruct

The timer belongs to the chat; either side can change it and both see a notice. Each message carries
the timer that was in force when it was sent. The receiver's countdown starts when the message is on
screen; the receipt it sends starts the sender's countdown at the same moment. When it runs out, each
device deletes the message from its own store. A ring around the timer icon on the bubble drains as
the time passes.

### Ending a chat

Either side can end it. The server marks it closed, deletes its keys and every frame still waiting,
and tells the other device. Each device then deletes its keys and every message of the chat. The
server also lets any of the invited person's devices decline an invitation that is not yet accepted;
this version's page has no separate decline button (opening the invitation accepts it, and the person
can end the chat from there).

## Threat model

### What a secret chat protects against

- **The server's storage and the people who can read it** (database administrators, backups, log
  files, a stolen database dump): the server holds only public keys and, briefly, sealed bodies.
- **The person's other devices and other accounts**: the chat exists on one device of each side.
- **A server that alters, replays, reorders or moves sealed messages**: AES-GCM detects any change;
  the associated data binds each body to its chat and direction; counters reject replays.
- **A network observer**: the socket is TLS; inside it the bodies are sealed again.

### What it does not protect against

1. **A compromised or malicious server delivering bad code.** This is the most important limit.
   DocFind is a web application: the page and its JavaScript come from the same server that relays
   the messages. Anyone who controls that server, the IIS proxy in front of it, or its TLS key can
   send a modified page that reads the keys or the text as they are used, on the next page load, and
   the person would not see a difference. Browsers have no way to pin or sign the code of a web page.
   Secret chats protect against a server that **stores** or **relays** dishonestly, not against a
   server that **serves** dishonest code. Closing this gap needs a separately installed client (an
   app or a browser extension that checks code hashes), which this version does not have.
2. **Key substitution when the chat starts.** The server introduces the two devices, so it could
   give each a key of its own and sit in the middle. Comparing the key fingerprint (the picture and
   the 64 hex digits under the chat menu) in person or over a trusted channel detects this. Until the
   two people compare, they trust the server on first use.
3. **Metadata.** The server sees who has a secret chat with whom, from which device, when each frame
   passes, its kind at the transport level (invitation, acceptance, message, close), the size of each
   body in 64-byte steps, typing indicators, and when receipts flow (their content is sealed, their
   timing is not).
4. **The devices themselves.** Malware, a browser extension that can read the page, or someone using
   the unlocked device can read what is on screen. Messages are stored sealed in IndexedDB, but the
   keys that open them are stored next to them in the same browser profile: "non-extractable" stops
   page script from exporting a key, not a forensic copy of the profile.
5. **Screenshots and cameras.** A browser cannot block a screenshot. On a desktop the Print Screen key
   is noticed and the other side is told; while the page is hidden (the app switcher takes a picture)
   the messages are blurred. Neither is a guarantee, and a phone camera defeats both.
6. **No forward secrecy inside a chat.** Each chat has one key agreement. The private keys are
   discarded once the message keys exist, but those two keys stay on each device for the life of the
   chat; whoever takes a device's store can read every message still on it. There is no ratchet as in
   Signal. Ending a chat and starting a new one makes new keys.
7. **The timer is a promise of the other device's page.** A modified page (see 1) or a person
   copying text by hand can keep a message after its timer.
8. **Loss is final.** Clearing the browser's site data, using another browser or losing the device
   ends the chat on that side. There is no backup, by design. Safari may clear the storage of a site
   that has not been used for seven days (not of a web app added to the home screen).
9. **A device that never returns.** A sealed body waits up to 30 days for its device. Ending the chat
   deletes it at once.

## Features that are not in secret chats (this version)

- Text only: no files, photos, voice or video messages, stickers or polls.
- No formatting is drawn and links are not made clickable: every message is shown as plain text,
  because the server cannot check what a secret message contains.
- No reply, edit, reactions or pins. Delete is always for both devices.
- One device per side; a secret chat does not move to a new device.

## Frames

| Frame (client to server) | Data | Answer |
|---|---|---|
| `secret_hello` | `device` | waiting frames as `secret`, then `ack {live: [chat ids this device holds]}` |
| `secret_open` | `peer`, `keys: [{alg, key}]` | `ack {chat}` |
| `secret_accept` | `chat`, `key: {alg, key}` | `ack` |
| `secret_send` | `chat`, `cid` (UUIDv7), `body` | `ack {id, at}` |
| `secret_ack` | `ids` | `ack` |
| `secret_close` | `chat` | `ack` |

Server to client: `secret {id, chat, kind: request | accept | msg | close | gone, from, cid, body, at, peer}`.
Errors come as `err {code, text}` with Persian text and no internals (`secret_state`,
`secret_device`, `secret_body`, `secret_key`, and the module's usual codes).

## Motion (SPEC 8)

| Animation | Status |
|---|---|
| Send: the text flies from the input to its bubble (FLIP) | done (shared `fly`) |
| Incoming bubble scales in from 0.9 with a fade | done (shared `enter`) |
| Read ticks: the second tick draws from the first | done (same tick markup and CSS as ordinary chats) |
| Self-destruct: a ring drains around the timer icon | done |
| Delete and self-destruct: the bubble scales to 0.8 and fades, the rest closes the gap with FLIP | done |
| Menus (chat menu, bubble menu, timer list) grow from the press point with the spring easing | done (shared `openMenu`) |
| Send button: lock icon morphs to the send icon with the first character | done |
| Reduced motion: every movement becomes a 100 ms fade; the ring is drawn at its current point and does not move | done |

## Tests

- `internal/chat/secret_test.go` (no database needed): an outsider cannot send, accept, close, collect
  or confirm anything in a secret chat, even claiming a member's device id; plain text and anything
  not shaped like a sealed body is refused before it is stored; a storage failure is logged without
  the body (and every log call in the secret files passes only constant text, the operation and an
  error class); frames reach only the chat's device; acceptance is first-device-wins and only for
  offered algorithms; closing; key validation.
- `internal/chat/secretstore_test.go` (with `DOCFIND_PG_*`): the same flow against PostgreSQL,
  including the ordinary send refusing a secret chat and the chat row going once both sides left.
- Checked in a browser with two people against a local stand-in relay: invitation, acceptance on
  opening, a message both ways, read ticks, equal fingerprints on both sides, the timer notice, a
  2-second message disappearing from both devices, and ending the chat.
