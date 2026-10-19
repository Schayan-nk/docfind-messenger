# docfind-messenger

A self-hosted team messenger written in Go: one-to-one and group chats,
channels, files and voice messages, end-to-end encrypted secret chats, and
voice and video calls. The interface is Persian and right to left, and it
installs as an app on phones and desktops (PWA).

It began as the messenger inside DocFind, a document and workflow system,
and runs here on its own: one binary, PostgreSQL, and nothing hosted by
anyone else. STUN, TURN and the group-call media server are built in.

## What it does

**Chats**
- Private chats, groups, supergroups with topics, channels and Saved Messages
- Replies, forwards, edits, deletes, pins, reactions, mentions, drafts
- Polls, scheduled messages, animated stickers (Lottie, expressions refused),
  shared locations on a map, link previews
- Folders, archive, mute, unread markers, invite links with approval, an
  admin log per chat
- Search that understands Persian: Arabic and Persian letter forms, digits
  and zero-width joiners are folded before matching

**Files**
- Resumable uploads, stored by their SHA-256 and never under the name sent
- Albums, voice messages with waveforms, round video messages
- Thumbnails and waveforms through ffmpeg when it is installed

**Calls**
- One-to-one voice and video, browser to browser, with the built-in TURN
  relay for networks that need it (time-limited credentials)
- Group voice and video through the built-in selective forwarding unit
  (pion/webrtc)

**Secret chats**
- End-to-end encrypted, keys only on the devices; the server stores public
  keys and ciphertext. See [docs/chat/SECRET_CHATS.md](docs/chat/SECRET_CHATS.md).

**Accounts and security**
- Roles (administrator, manager, viewer) with per-account allow and deny
  overrides
- Two-step sign-in with an authenticator app, a code by email or by SMS, and
  passkeys (WebAuthn)
- A sign-in policy (password rules, lockouts, two steps for everyone), the
  list of active sessions, and an audit trail of every change
- A Content-Security-Policy that runs scripts from this server only (the
  pages' own inline scripts included), CSRF and origin checks, rate limits,
  and link previews that cannot be aimed at private addresses

**Notifications**
- Web Push to phones and desktops, quiet hours, and a notification centre in
  the app

**Translation (optional)**
- A Translate button between Persian, English and German, answered by a
  self-hosted Ollama model

The design the code follows, with what each part is for, is in
[docs/chat/SPEC.md](docs/chat/SPEC.md). Code comments cite it by section.

## Requirements

- Go 1.26 to build
- PostgreSQL for chats, accounts, notifications and the audit trail
- HTTPS in front of it: browsers only allow the camera and microphone on a
  secure page. Use a reverse proxy that passes WebSocket upgrades, or give the
  server a certificate with `DOCFIND_TLS_CERT` and `DOCFIND_TLS_KEY`
- ffmpeg (optional) for video thumbnails and voice waveforms

## Quick start

```sh
go build -o docfind-messenger ./cmd/messenger

cp .env.example .env        # then set DOCFIND_PG_* at least

# The first administrator, typed at the terminal (the password is not echoed):
./docfind-messenger -users data/users.json -add-user admin -role admin

# On this machine only, to try it:
./docfind-messenger -users data/users.json

# For the network, once an account exists:
./docfind-messenger -users data/users.json -bind 0.0.0.0 -port 8300
```

The server refuses to listen beyond this machine while the accounts file is
empty, so the first administrator cannot be created over the network by
whoever gets there first.

`-check-chat` tests what the messenger needs (PostgreSQL, the media folder,
ffmpeg, TURN and the running server's socket) and prints a line for each.

## Configuration

Everything is read from the environment, or from a `.env` file beside the
binary (`-env-file` to name another). Nothing secret belongs in the code or in
a config file under version control. [.env.example](.env.example) lists every
variable with what it does; the ones you need:

| Variable | Purpose |
|---|---|
| `DOCFIND_PG_HOST`, `DOCFIND_PG_PORT`, `DOCFIND_PG_USER`, `DOCFIND_PG_PASSWORD`, `DOCFIND_PG_DATABASE`, `DOCFIND_PG_SSLMODE` | The database. The schema is created and upgraded on start. |
| `DOCFIND_PUSH_SUBJECT` | A `mailto:` address push services can reach you at. Without it, device notifications stay off. |
| `DOCFIND_TURN_SECRET` | Turns the built-in TURN relay on. Calls between networks that cannot reach each other need it. |
| `DOCFIND_PASSKEY_RPID`, `DOCFIND_PASSKEY_ORIGINS` | The host name passkeys are bound to, and the origins they are used from. |

Files the server keeps beside the accounts file (`-users`): the session
signing key, the active sessions, profile pictures, the key that seals
two-step secrets, and by default the chat media and logs. Back that folder up
with the database.

## Command line

| Flag | |
|---|---|
| `-users <file>` | The accounts file. Required. |
| `-bind <address>`, `-port <n>` | Where to listen. Default 127.0.0.1:8300. |
| `-trust-proxy-headers` | Take the client address from X-Forwarded-For. Only behind a proxy you control. |
| `-add-user <name> -role admin\|manager\|viewer` | Create an account, or reset its password, then exit. |
| `-set-grant <name> -perm <id> -state allow\|deny\|inherit` | Set one permission override, with the server stopped. |
| `-set-phone <name> -phone <number>` | Set an account's mobile number, with the server stopped. |
| `-check-chat` | Test what the messenger needs, then exit. |
| `-migrate-down <version> -confirm-data-loss` | Run one migration's down script. Destroys its data. |
| `-env-file <path>`, `-env-file-wins` | Which `.env` to read, and whether it overrides the real environment. |
| `-version` | Print the version. |

## Layout

| Path | |
|---|---|
| `cmd/messenger` | The binary: options, start-up, wiring |
| `internal/chat` | Chats, messages, media, search, calls signalling, secret chats, the WebSocket hub |
| `internal/calls` | The TURN server and the group-call media server |
| `internal/find` | The HTTP server: pages, the account and settings API, push, two-step, passkeys |
| `internal/auth` | Accounts, sessions, roles, permissions, the sign-in policy |
| `internal/pgstore` | PostgreSQL access and the numbered migrations |
| `internal/notify`, `internal/webpush` | The notification queue and the Web Push sender |
| `internal/webauthn` | Passkeys |
| `internal/audit`, `internal/obs` | The audit trail, request logs and metrics |
| `internal/netguard` | The check that keeps outbound requests off private networks |

## Development

```sh
go vet ./...
go test ./...
```

The route and security tests run the real router against stores in memory.
The tests that need PostgreSQL run when `DOCFIND_PG_*` points at a test
database and skip otherwise. Use one you can throw away, and run them one
package at a time (`go test -p 1 ./...`): several packages write to the
same tables.

## License

MIT. See [LICENSE](LICENSE). The bundled third-party files keep their own
licences: Leaflet (BSD-2-Clause), lottie-web (MIT), Lucide icons (ISC),
Vazirmatn and Archivo (SIL Open Font License).
