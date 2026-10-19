# Messenger: design specification

The specification this messenger was built against: one-to-one and group chats, channels,
media, voice and video calls, secret chats and notifications, with Telegram as the yardstick.
Code comments cite it by section ("SPEC 6.4") and by phase ("phase 0, item 4").
Not every item is built; the README says what is.

## 4. Phase 0: Prerequisites (Gate)

Do these items first. The call features cannot work without them.

1. **HTTPS.** Browsers allow camera and microphone access only in a secure context. Make sure that the server runs behind an HTTPS reverse proxy, or serves TLS itself. Make sure that the proxy forwards WebSocket upgrade headers (`Upgrade`, `Connection`). Set the proxy read timeout to a minimum of 1 hour for WebSocket connections.
2. **TURN and STUN.** Add an embedded TURN server with `pion/turn` in the server process. Use time-limited credentials (TURN REST API method: HMAC-SHA1 of `expiry:userID` with `DOCFIND_TURN_SECRET`). Use UDP 3478, TCP 3478, and TLS 5349. Document the firewall ports and the UDP relay port range.
3. **PostgreSQL migrations.** Add a migration runner with numbered SQL files in `migrations/`. Each migration must be idempotent and must have a down script.
4. **File storage.** Add a storage folder for chat media, configurable from the Settings UI. Use content-addressed names (SHA-256). Never use the original file name as a path.
5. **Health check.** Add `docfind-messenger -check-chat` that tests PostgreSQL, the storage folder, TURN, ffmpeg, and the WebSocket endpoint. Print a clear pass or fail line for each item.
6. **ffmpeg.** Put a static ffmpeg build next to the server binary. Set its path in the environment variable `DOCFIND_FFMPEG`. DocFind uses it for video thumbnails, voice waveforms, sticker conversion for Safari, and call recording.

## 5. Architecture

### 5.1 Transport

- One authenticated WebSocket per device: `GET /api/chat/ws`.
- The client sends the JWT in the first message, not in the URL. Reject the connection if the token is not valid or if `TokenVersion` changed.
- When an admin increases `TokenVersion`, close all open sockets of that account within 5 seconds.
- Use a JSON envelope: `{ "t": "type", "id": "client-msg-id", "d": {...} }`.
- Every message from the client has a client-generated ID (UUIDv7). The server uses it for deduplication. A resend must not create a duplicate message.
- Use a per-chat sequence number (`pts`). On reconnect, the client sends the last `pts` per chat. The server sends only the missing updates ("getDifference" model, the same as Telegram).
- Send a ping each 25 seconds. Close dead connections after 60 seconds.
- Use REST endpoints (`/api/chat/...`) for history pages, uploads, search, and settings. Use the WebSocket for real-time events.

### 5.2 Server Components

Put the module in a new package folder (for example `internal/chat/`, `internal/calls/`). Use these parts:

- **Hub:** keeps the map of user → open device connections. Fans out events.
- **Message service:** create, edit, delete, forward, react, pin, read state.
- **Media service:** upload (chunked and resumable), thumbnails, transcode metadata, download with HTTP Range support.
- **Search service:** PostgreSQL full-text search. Persian text needs normalization (Arabic ي → Persian ی, ك → ک, remove tatweel and zero-width non-joiner variants, convert Persian and Arabic digits).
- **Push service:** PostgreSQL queue. One row for each notification. Retry with backoff until success, then delete the row.
- **Call signaling service:** offer, answer, ICE candidates, call state machine.
- **SFU:** for group calls. Use `pion/webrtc` to forward media. Do not mix audio on the server unless needed.
- **Bot service:** HTTP Bot API (Section 6.11).
- **Scheduler:** scheduled messages, self-destruct, auto-delete timers, push retries, story expiry.

### 5.3 Scale Target

- Design for 500 concurrent users and 5,000 registered users on one server.
- A message must reach an online recipient in less than 300 ms on the LAN.
- A group call must support 30 video participants and 200 audio-only listeners.
- Run `go test -race` on all new packages.

### 5.4 Database Schema (Minimum)

Make these tables. Add indexes for every foreign key and every column that you query.

- `chat_users_ext` (user_id FK to the user table, username, bio, avatar_file_id, last_seen_at, privacy JSON)
- `chats` (id, type: `private | group | supergroup | channel | saved | secret`, title, about, avatar_file_id, created_by, settings JSON, is_forum, slow_mode_seconds, auto_delete_seconds, created_at)
- `chat_members` (chat_id, user_id, role: `owner | admin | member | restricted | banned | left`, rights JSON, custom_title, joined_at, muted_until, folder_ids, pinned_order, archived, last_read_pts, last_read_at)
- `topics` (id, chat_id, title, icon, color, closed, pinned)
- `messages` (id, chat_id, topic_id, pts, sender_id, sender_chat_id, client_msg_id, kind, text, entities JSON, reply_to_id, quote JSON, fwd_from JSON, media_id, edited_at, deleted_at, silent, scheduled_for, self_destruct_seconds, expires_at, views, created_at)
- `message_reactions` (message_id, user_id, emoji, created_at)
- `message_reads` (chat_id, user_id, max_read_pts)
- `pins` (chat_id, message_id, pinned_by, pinned_at)
- `media` (id, sha256, mime, size, width, height, duration, waveform, thumb_id, original_name, uploaded_by, ocr_text)
- `polls`, `poll_options`, `poll_votes`
- `drafts` (user_id, chat_id, topic_id, text, entities, reply_to_id, updated_at)
- `folders` (id, user_id, title, icon, include_rules JSON, exclude_rules JSON, order)
- `invite_links` (id, chat_id, token, created_by, expires_at, usage_limit, usage_count, requires_approval, revoked)
- `join_requests` (chat_id, user_id, link_id, created_at, status)
- `admin_log` (id, chat_id, actor_id, action, payload JSON, created_at)
- `blocks` (user_id, blocked_user_id)
- `contacts` (user_id, contact_user_id, display_name)
- `devices` (id, user_id, name, platform, push_endpoint, push_keys, last_active_at, ip, created_at)
- `push_queue` (id, device_id, payload, attempts, next_attempt_at)
- `calls` (id, type: `p2p | group`, chat_id, started_by, started_at, ended_at, end_reason, is_video, recording_media_id)
- `call_participants` (call_id, user_id, joined_at, left_at, muted, video_on, hand_raised_at)
- `stories`, `story_views`
- `bots`, `bot_commands`, `bot_updates`
- `secret_chat_keys` (chat_id, device_id, public_key, fingerprint), the server stores public keys only

## 6. Feature List (Telegram Parity)

Each feature has acceptance criteria. A feature is complete only when all of its criteria pass.

### 6.1 Profile, Presence, Privacy

- Username (unique, 5 to 32 characters, Latin letters, digits, underscore). Persian display name from the DocFind user table.
- Profile photo (more than one, with history), bio, and a profile page.
- Online status, "last seen recently / within a week / within a month", and exact last seen time.
- Privacy settings for: last seen, profile photo, bio, forwarded messages link, calls, groups invite, voice messages. Each setting has the options "Everybody / My contacts / Nobody" plus "Always share with" and "Never share with" lists.
- If a user hides last seen, that user also does not see the last seen of others (Telegram rule).
- Block and unblock users. A blocked user cannot send messages or call.
- Contacts list with custom display names.

**Acceptance:** Two users with different privacy settings see the correct status of each other. A blocked user gets an error on send and on call.

### 6.2 Chat Types

1. **Private chat**, two users.
2. **Saved Messages**, a private chat with yourself. Use it as personal notes. Support tags on saved messages.
3. **Group**, up to 200 members. All members can see history from the start.
4. **Supergroup**, up to 10,000 members (design limit for this company). Admin roles, slow mode, permissions, hidden history option for new members.
5. **Forum (Topics)**, a supergroup with topics. Each topic has its own thread, unread count, and pinned messages. The "General" topic exists by default.
6. **Channel**, broadcast. Only admins post. Subscribers read. View counter on each post. Optional author signature. Optional linked discussion group for comments.
7. **Secret chat**, end-to-end encrypted, device-bound (see 6.12).

**Acceptance:** Make each chat type, add members, send messages, and confirm the permission rules in an automated test.

### 6.3 Messages

- Send text up to 4,096 characters. Split longer text into more than one message.
- Formatting entities: bold, italic, underline, strikethrough, monospace, code block with language, spoiler, link with custom text, block quote, expandable block quote, mention, hashtag, cashtag, bot command, email, phone, URL. Store entities as offset and length (UTF-16 units, the same as Telegram). Sanitize all output. Never put user text into `innerHTML` without escape.
- Reply to a message. Reply to a part of a message (quote). Reply to a message in a different chat.
- Forward messages one by one or in a group. Options: show sender, hide sender, hide captions.
- Edit a sent message. Show "edited" with the time. Keep no edit history visible to others.
- Delete for me. Delete for everyone (no time limit in private chats, admin rights in groups).
- Pin more than one message per chat. Show a pinned bar with navigation. "Pin for both" in private chats.
- Reactions: Unicode emoji. Admins set the allowed reactions per chat. Show who reacted.
- Mentions with `@username` and with a name link for users without a username. A mention counter and a "jump to next mention" button.
- Hashtags. A click on a hashtag searches the chat.
- Link preview (fetch on the server, with a timeout, size limit, and SSRF protection: block private IP ranges, localhost, and metadata addresses). The sender can remove or move the preview above or below the text.
- Scheduled messages: send at a date and time. "Send when online" option. A separate list of scheduled messages per chat.
- Silent messages: deliver without sound.
- Drafts: save per chat and per topic. Sync to all devices of the user.
- Message status: one tick (sent to server), two ticks (read). A clock icon when the message is in the send queue.
- Read receipts: in groups, show who read the message (groups up to 100 members, messages less than 7 days old).
- Typing and action states: typing, recording voice, recording video, sending photo, sending file, choosing sticker. Show them in the chat header and the chat list.
- Self-destruct timer on media in private chats.
- Auto-delete timer per chat (1 day, 1 week, 1 month, custom).
- Message translation: a "Translate" button. Use a self-hosted model (the Ollama instance on the server) between Persian, English, and German. If Ollama is not available, hide the button.
- Message selection mode: select more than one message to forward, delete, or copy.
- Copy a message link (for supergroups and channels).
- Message effects, animated emoji, interactive emoji, and dice: see Section 8.7.

**Acceptance:** Every message action syncs to every open device of every member within 1 second on the LAN. Every action works after reconnect with no duplicates and no lost messages.

### 6.4 Media and Files

- Photos: compress on the client (option to send as file without compression). Make server thumbnails. Strip EXIF location data unless the user sends it as a file.
- Albums: up to 10 photos or videos in one group message. Reorder before send.
- Video: stream with HTTP Range. Show a preview frame and the duration.
- Files: any type up to 2 GB. Chunked upload (512 KB chunks), resume after network loss, pause and cancel. Show progress.
- Voice messages: record with `MediaRecorder` (Opus in WebM or Ogg; use AAC in MP4 for iPhone Safari). Show a waveform. Playback speed 1x, 1.5x, 2x. Hold to record, slide to cancel, lock for hands-free. Voice-to-text with the self-hosted model, optional.
- Video messages (round): record a short round video up to 60 seconds with the front camera.
- Stickers and GIFs: support static WebP, animated Lottie (`.tgs`), and video WebM stickers (see Section 8.8). Admins upload sticker packs. A GIF search uses only local GIF packs (no external GIF service).
- Emoji keyboard with recent emoji and search in Persian and English.
- Location: send a fixed point. Live location for 15 minutes, 1 hour, 8 hours. Use a map library that you serve locally with tiles from an accessible provider. If no tile provider is reachable, show coordinates and a static grid.
- Contact card: share a DocFind user as a contact.
- Polls: anonymous or public, single or multiple answer, quiz mode with a correct answer and an explanation, close time.
- Shared media gallery per chat: tabs for media, files, links, voice, music, GIFs, and group members.
- **DocFind integration:** share a document from the DocFind corpus in a chat as a card. The card opens the DocFind viewer. Apply the same document access rules as the search UI. A user without access to the document sees "access denied", not the content.
- Run the existing DocFind OCR on images and PDFs sent in chats. Store the result in `media.ocr_text` so that search finds text inside attachments.
- Validate every upload: check the real file type with magic bytes, not only the extension. Reject executable types unless an admin allows them. Serve all user files with `Content-Disposition: attachment` and `X-Content-Type-Options: nosniff`, except safe image and video types that the app displays.

**Acceptance:** Upload a 1.5 GB file, cut the network at 50%, restore the network, and the upload resumes and completes. The SHA-256 on the server matches the source.

### 6.5 Chat List and Organization

- Sort by last activity. Pin chats at the top (5 in the main list, more in folders).
- Unread counter, mention badge (@), reaction badge.
- Mark as read, mark as unread.
- Mute: 1 hour, 8 hours, 1 day, 7 days, forever, custom. Mute only for mentions.
- Archive. Muted archived chats stay in the archive. Unmuted chats return to the main list on a new message.
- Chat folders: include and exclude rules by chat type, contacts, muted, read, archived, and selected chats. Folder tabs at the top. Share a folder with an invite link.
- Swipe actions on mobile: archive, pin, mute, delete, mark as read.

### 6.6 Search

- Global search: chats, users, messages, files, and links.
- In-chat search with "previous / next" navigation and filters: by sender, by date, by media type.
- Search inside the OCR text of attachments.
- Persian normalization (see 5.2). Highlight the found words.
- Search results respect membership. A user must never see a message from a chat that the user is not a member of. Add a security test for this rule.

### 6.7 Groups and Channels Administration

- Roles: owner, admin, member, restricted, banned.
- Granular admin rights (the same as Telegram): change info, delete messages, ban users, invite users, pin messages, manage topics, manage video chats, post messages (channels), edit messages of others (channels), add new admins, remain anonymous.
- Member restrictions: send messages, send media (by type), send polls, add link previews, invite users, pin messages, change info. Restrict until a date.
- Slow mode: 10 s, 30 s, 1 min, 5 min, 15 min, 1 h.
- Invite links: primary link, extra links with name, expiry, usage limit, and approval required. Revoke a link. Show who joined through each link.
- Join requests: approve or decline, one by one or all.
- Recent actions log (`admin_log`) for 48 hours, filter by action and by admin.
- Transfer ownership (requires password confirmation).
- Anti-spam option for large groups: block messages with links from new members for a configurable period.
- Channel: discussion group link, comments under each post, post signatures, silent broadcast, view statistics.
- Group and channel statistics for admins: member growth, messages per day, top posters, view counts. Use the Chart.js copy that DocFind already serves.

### 6.8 Calls: One to One

- Voice call and video call between two users.
- Use WebRTC with the self-hosted STUN and TURN (Phase 0).
- Media path: peer to peer when possible, TURN relay when not. Force TURN with a setting for networks that block UDP.
- Encryption key verification: derive four emoji from the DTLS fingerprints of both sides (hash both fingerprints in sorted order, map the hash to an emoji table). Both users see the same four emoji. This is the same idea as Telegram.
- Call screen: mute, camera on and off, switch camera, speaker or earpiece (where the browser allows), screen sharing, end call.
- Picture-in-picture when the user leaves the call screen in the app.
- Incoming call screen with ring tone, accept, decline, and "decline with message" quick replies.
- Call states: ringing, connecting, connected, reconnecting, ended. Show the duration.
- Reconnect automatically on network change (ICE restart). Keep the call for a maximum of 30 seconds without media before you end it.
- Call quality: show a signal indicator from `getStats()` (round trip time, packet loss).
- Noise suppression and echo cancellation (browser constraints).
- Call history in a "Calls" tab: incoming, outgoing, missed, duration. "Call back" button.
- Missed call notification through push.
- Privacy rule "who can call me" (6.1) applies.
- Upgrade a voice call to a video call during the call.
- Call rating after a call (1 to 5 stars), stored for diagnostics.

**Acceptance:** A call works in these four cases: (1) two PCs on the LAN, (2) a PC on the LAN and a phone on mobile data, (3) both users behind symmetric NAT (TURN only), (4) iPhone Safari PWA to Chrome on Windows.

### 6.9 Calls: Group Voice and Video Chats

- Start a voice chat or video chat in a group or channel. Members join and leave at any time.
- Use the SFU (5.2). Use simulcast (three video layers). The client subscribes to the layer that fits the tile size.
- Show a grid of video tiles and a list of audio-only participants. Highlight the active speaker.
- Screen sharing as a separate stream.
- Raise hand. Admins see the list of raised hands.
- Admin controls: mute a participant, mute all new participants, remove a participant, end the chat for all.
- "Join as listener" mode in channel live streams. Only admins speak.
- Recording: record audio only or audio and video on the server. Save the recording as a message in the chat. Show a red dot to all participants during recording.
- Schedule a voice chat with a start time and a reminder.
- Invite link to a voice chat.
- Join from a second device without a duplicate tile.

**Acceptance:** Run a load test with 30 video publishers and 200 audio listeners on the server. CPU load must stay below 50%. Measure and record the results in `docs/chat/LOADTEST.md`.

### 6.10 Notifications

- Web Push with VAPID keys from environment variables. Encrypt the payload (RFC 8291). Send only the chat name and a short preview. Respect the "show preview" setting.
- Important: Push endpoints of browser vendors can be blocked or slow from Iran. Use the PostgreSQL push queue with retry. Also deliver every notification through the open WebSocket when the app is open. Test iPhone PWA push (needs iOS 16.4 or later and the app added to the Home Screen).
- Per chat settings: on, off, mentions only, custom sound.
- Global settings: private chats, groups, channels, calls, in-app sound, in-app vibration, badge counter.
- Group notifications for one chat on the device (one notification with a counter, not 50 notifications).
- Notification actions: reply from the notification, mark as read.
- Do not notify the device that sent the message.

### 6.11 Bots

- Bot accounts that an admin makes in the DocFind admin UI. Each bot has a token (stored as a hash).
- Bot API over HTTPS with a subset of Telegram Bot API methods: `getMe`, `sendMessage`, `editMessageText`, `deleteMessage`, `sendDocument`, `sendPhoto`, `answerCallbackQuery`, `setMyCommands`, `getUpdates`, `setWebhook`.
- Inline keyboards with callback buttons and URL buttons. Reply keyboards.
- Bot commands menu (`/command` list).
- **DocFind chatbot:** connect the planned DocFind chatbot as a bot in this system. Users chat with it in a private chat.
- Rate limit each bot (30 messages per second globally, 1 message per second per chat).

### 6.12 Secret Chats (End-to-End Encryption)

- Start a secret chat with one user from one device to one device.
- Key exchange: X25519 (or ECDH P-256 if the browser does not support X25519 in Web Crypto). Message encryption: AES-256-GCM with a counter. Store private keys in IndexedDB as non-extractable `CryptoKey` objects.
- The server stores and forwards only ciphertext. The server cannot read the content.
- Show a key fingerprint (image and hex) for manual verification.
- Self-destruct timer for every message.
- No forward from secret chats. Try to block screenshots where the platform allows (show a notice when a screenshot is detected on supported platforms).
- Secret chats do not sync to other devices (the same as Telegram).
- Record the threat model and the limits in `docs/chat/SECRET_CHATS.md`. Be honest: a web app loads code from the server, so a compromised server can deliver bad code. Write this limit in the document.

### 6.13 Multi-Device and Account Security

- Active sessions list: device, platform, IP, location (only city from a local GeoIP file, optional), last active. Terminate one session or all other sessions. Connect this to `TokenVersion` or a per-device revocation list.
- QR login: scan a QR code from a logged-in phone to log in on a PC.
- Two-step verification: TOTP (RFC 6238) with the standard library. Recovery codes.
- Login alert in Saved Messages when a new device logs in.
- Auto-terminate inactive sessions after a configurable period.
- Local passcode lock for the PWA (lock the UI after inactivity).

### 6.14 Stories (Last Priority)

- Photo or video story for 24 hours. Privacy: everybody, contacts, close friends, selected users.
- Story viewers list. Reactions on stories.
- Highlight stories on the profile.
- Mark this phase as optional. Build it only after phases 1 to 7 pass.

### 6.15 Settings and Appearance

- Dark theme only. Offer several dark accent colours and dark chat wallpapers.
- Message text size.
- Language: Persian (default) and English.
- Data and storage: auto-download rules per media type and per network type, cache size limit, clear cache.
- Chat list view: two lines or three lines.
- Send with Enter or with Ctrl+Enter.

### 6.16 Telegram Features to Exclude or Adapt

Do not build these. Write one line for each in `DECISIONS.md`:

- Phone number login and SMS (DocFind uses its own user table).
- Payments, Telegram Stars, Fragment usernames, Premium subscription.
- Mini Apps marketplace (bot inline keyboards are enough).
- Public directory of channels across organizations.

## 7. Frontend Requirements

- Use the current DocFind frontend structure. If DocFind uses plain JavaScript, use plain JavaScript modules. Do not add a large framework without a written reason in `DECISIONS.md`.
- Layout: three columns on desktop (folders, chat list, chat). One column with navigation on mobile.
- Virtual scroll for long message lists (100,000 messages in one chat must scroll smoothly).
- Load history in pages of 50 messages. Keep the scroll position when older pages load.
- Local cache in IndexedDB: chats, the last 200 messages per chat, and drafts. Show cached content first, then sync.
- Offline send queue: messages that the user sends without a connection go to a queue and send on reconnect.
- Service worker: cache the app shell, handle push, handle notification clicks.
- RTL: message bubbles, input, and menus mirror correctly. Mixed Persian and English text uses `dir="auto"` on each bubble.
- Accessibility: keyboard navigation for all actions, ARIA labels in Persian, focus visible.
- Performance budget: the chat module adds less than 120 KB gzipped to the first load. Load the call code, the emoji data, the Lottie player, and the map library only when the user opens them.

## 8. Animations and Motion (Telegram Parity)

Telegram feels fast because of its motion design. Build the same motion. Each animation below has a target duration and a behaviour. Record each animation in `docs/chat/ANIMATIONS.md` with the status `done`, `partial`, or `excluded`.

### 8.1 Foundation

Build this foundation in Phase 1, before the first animation.

- **Tools:** Use CSS transitions, CSS keyframes, and the Web Animations API. Do not add an animation framework. Use the FLIP technique (First, Last, Invert, Play) for list moves and shared-element transitions. Use the View Transitions API (`document.startViewTransition`) when the browser supports it. Use the FLIP fallback when it does not.
- **Lottie player:** Telegram animated stickers and animated emoji are Lottie files. A `.tgs` file is gzipped Lottie JSON. Download the light build of `lottie-web` (no expressions, SVG or canvas renderer) and serve it locally. Load it only when the first animated sticker or emoji comes into view. Decompress `.tgs` on the client with `DecompressionStream('gzip')`. Decompress on the server for browsers without `DecompressionStream`.
- **Motion tokens in `app.css`:**
  - `--dur-fast: 120ms` (hover, press, ticks)
  - `--dur-base: 240ms` (most transitions)
  - `--dur-slow: 360ms` (screen changes, sheets)
  - `--ease-out: cubic-bezier(0.2, 0, 0, 1)`
  - `--ease-in-out: cubic-bezier(0.4, 0, 0.2, 1)`
  - `--ease-spring`: a `linear()` easing with sampled spring values (small overshoot) for sheets, menus, and the send button. Use `cubic-bezier(0.34, 1.56, 0.64, 1)` as the fallback.
- **Rules:**
  - Animate only `transform` and `opacity` in lists and on screens with many elements. Do not animate `height`, `width`, `top`, `left`, `box-shadow`, or `filter` there. Use FLIP with `transform` for size and position changes.
  - Set `will-change` only for the duration of the animation. Remove it when the animation ends.
  - Use `requestAnimationFrame` for animations that JavaScript drives. Do not use `setInterval`.
  - Stop every animation that is not visible (`IntersectionObserver`). Stop every animation when the tab is hidden (Page Visibility API).
  - Gesture animations (swipe back, swipe to reply, drag to dismiss) must follow the finger with no delay. The gesture completes or cancels based on the release speed (velocity), not only on the distance.
  - No animation is longer than 500 ms, except the emoji effects, the dice, and the stickers.
  - Every animation has a reduced-motion variant (Section 8.13).

### 8.2 Chat List

- New message in a chat: the chat row moves to the top. The other rows move down with a FLIP animation (`--dur-base`). The rows do not jump.
- Unread badge: a new badge scales in from 0 to 1 with `--ease-spring`. A count change rolls the digits vertically. A badge that goes to zero scales out.
- Online dot on an avatar: scales in and scales out.
- Swipe actions (mobile): the row follows the finger. The action icons show when the row moves more than 30% of its width. A release at more than 50%, or a fast swipe, does the action. A spring return on cancel.
- Archive: pull the list down past the top to show the Archive row. The row grows from zero height. The pull distance is reduced to 50% after the row is fully shown (a resist effect).
- Folder tabs: the active underline slides to the new tab (`--dur-base`). On mobile, the list content slides horizontally in the swipe direction. On desktop, the list content cross-fades. Swipe left and right between folders on mobile.
- Pin and unpin: the row moves to the new position with a FLIP animation.
- Typing in the chat list row: three dots that bounce in sequence, in the place of the last message text.
- Mute, archive, or delete from the menu: the row fades out and the rows below slide up.

### 8.3 Navigation and Screens

- Open a chat (mobile): the chat screen slides in from the end side (the left side in RTL). The chat list moves 30% in the same direction behind it, with a dim overlay. Swipe from the screen edge to go back, with the same animation in reverse. This gesture follows the finger.
- Open a chat (desktop): cross-fade the chat column (`--dur-fast`). The header and the input do not flash.
- Open a profile: the small avatar in the header grows into the large profile header (shared-element transition). A scroll on the profile shrinks the avatar back to a small size in the top bar.
- Open a full-screen avatar or media: the thumbnail grows into the viewer (shared-element). Drag down to dismiss: the viewer scales down and follows the finger, and the background fades. Swipe left and right between the media items of the chat.
- Bottom sheets (attachment menu, context sheets, forward sheet): slide up with `--ease-spring`. Drag down to close with velocity. The background dims and blurs.
- Modal dialogs: scale from 0.9 to 1 with fade (`--dur-fast`).
- Settings and sub pages: push and pop with a slide (mobile) or a fade (desktop).
- Tabs in the shared media gallery: the content slides horizontally in the swipe direction.
- Search: the search field expands from the search icon. The results fade in. "Clear" fades out the results and collapses the field.
- Header title change (for example "Connecting…" → "Updating…" → the chat name): cross-fade with a vertical slide of 8 px.

### 8.4 Messages

- Send: the message text leaves the input and flies to its final position in the list. The bubble forms around the text during the flight (`--dur-slow`, FLIP from the input rectangle to the bubble rectangle). The input clears at the start of the flight.
- Send status: the clock icon cross-fades to one tick. When the message is read, the second tick draws from the first tick with a short stroke animation.
- Incoming message: the bubble appears at the bottom with a scale from 0.9 and a fade (`--dur-base`). The list above moves up with a FLIP animation, not a jump.
- Date pill: a floating date pill shows at the top while the user scrolls. It fades out 1 second after the scroll stops. The pill changes its text with a cross-fade.
- Unread divider: the "Unread messages" bar is in the list at the first unread message. The list opens at this position. The bar fades out after the user reads past it.
- Jump to bottom button: scales in when the user is more than one screen from the bottom. The unread count on the button rolls. A tap scrolls to the bottom with `--ease-out`.
- Jump to a reply or a search result: the list scrolls to the message with `--ease-out`. The target bubble shows a highlight pulse (accent colour at 30% opacity, fade out in 1.2 s).
- Swipe to reply (mobile): the bubble follows the finger to the side. A reply icon appears and scales up when the limit is reached. The device vibrates (Vibration API; not available in iOS Safari).
- Context menu on a long press or a right click: the bubble scales up a small amount, the rest of the chat dims and blurs, and the menu grows from the press point (`--ease-spring`). The reaction bar above the menu shows the emoji with a pop, with a delay of 20 ms between each emoji.
- Selection mode: the check circles slide in from the start side, and the bubbles move to make space (FLIP). The selection toolbar slides in from the bottom.
- Edit: the bubble content cross-fades to the new content. The "edited" label fades in.
- Delete: the bubble scales to 0.8 and fades out. The messages below move up with FLIP.
- Pin: a small copy of the bubble moves up to the pinned bar (shared-element). The pinned bar slides down from the header. With several pinned messages, a vertical segmented indicator on the start side of the bar moves between segments when the user taps the bar.
- Spoiler: moving particles (small dots that move slowly) cover the hidden text. A tap shows the text with a radial reveal from the tap point. The particles fade out.
- Expandable block quote: expands with FLIP on the elements below.
- Link preview: appears below the text with a fade and a vertical slide of 8 px.
- Typing status in the header: three dots that bounce, or the action text ("recording voice…", "sending photo…") with a related small icon animation.
- Counters in the "Scroll to mention" and "Scroll to reaction" buttons: the digits roll.
- Scheduled messages: the clock icon on the bubble pulses once each minute.
- Self-destruct timer: a circular countdown draws around the timer icon.
- Views counter in channels: the number rolls up.

### 8.5 Composer (Input)

- Send button: the microphone icon morphs to the send icon when the user types the first character, and back when the input is empty. Use a cross-fade with scale (`--dur-fast`, `--ease-spring`).
- Input height: grows and shrinks with the text, to a maximum of 8 lines. The message list moves with a FLIP animation.
- Attachment menu: the sheet slides up, and the option icons pop in with a delay of 30 ms between each icon.
- Emoji and sticker panel: slides up from below the input. The input moves up. Category tabs slide. A long press on a sticker shows a large preview with scale and a blurred background.
- Voice record (hold): the microphone button grows into a red circle with a pulse ring. A timer and a red dot blink. A "slide to cancel" label moves with a repeating arrow. A lock icon slides up. The user slides up to lock, and the UI changes to stop and send buttons with `--ease-spring`. The user slides to the side to cancel, and the record UI scales down and the microphone shakes once.
- Live waveform while the user records: bars that move with the microphone level from an `AnalyserNode`.
- Video message record (round): the circle grows from the camera button. A ring progress shows the time.
- Reply, edit, and forward bar above the input: slides down with a fade (`--dur-base`). The close button rotates on hover.
- Mention and command autocomplete: a list slides up above the input with a fade.
- Send button long press: a menu with "Send silently" and "Schedule" grows from the button.
- Draft saved: no animation. Do not show a toast for drafts.

### 8.6 Media

- Upload: a circular progress ring draws around a cancel icon on the media. The ring uses the accent colour on a dark overlay. When the upload completes, the ring fades out and the image sharpens from the blurred thumbnail (blur-up).
- Download of a file: the same ring on the file icon. When the download completes, the icon cross-fades to the file type icon.
- Photo in the chat: shows the blurred small thumbnail first, then cross-fades to the full image (`--dur-base`).
- Album: the tiles move with FLIP when more items arrive.
- Video: the play button scales in on hover (desktop). Inline auto-play videos (GIFs) start with a fade.
- Voice message: the waveform bars fill with the accent colour from the start to the current position. The play button cross-fades to pause. The speed button cycles 1x, 1.5x, 2x with a vertical roll of the label.
- Round video message: plays inside the circle. A ring progress draws around the circle. A tap on the circle expands it to a larger size with scale.
- Media viewer: pinch to zoom with the gesture, double tap to zoom in and out with `--ease-out`, swipe between items with a horizontal slide, drag down to dismiss (Section 8.3).
- Live location: the map pin pulses with expanding rings. The remaining time shows on a ring around the avatar on the map.
- Poll: after a vote, the result bars grow from 0 to the percentage with `--ease-out`. The percentages roll. In quiz mode, the correct answer gets a check icon that scales in. A wrong answer shakes horizontally once.
- Shared media gallery: items fade in on scroll, in groups.

### 8.7 Reactions, Animated Emoji, Interactive Emoji, and Dice

- Reaction add: the emoji scales from 0 to 1.2 to 1 with `--ease-spring` in the reaction chip. A short burst of small particles leaves the chip. The count rolls.
- Reaction remove: the chip scales out. The remaining chips move with FLIP.
- Double tap on a bubble: the default reaction (heart) animates as above. A larger heart floats up from the tap point and fades.
- Reaction effects: when a user adds a reaction, a large Lottie effect for that emoji plays once over the bubble (confetti for 🎉, fire for 🔥, and similar). Make or get open-licensed Lottie effects for the default reaction set. Record the licence of each file in `ANIMATIONS.md`.
- Animated emoji: a message with only one emoji shows the emoji large, without a bubble. It plays the Lottie version of the emoji if the local emoji pack has one. The animation plays once and loops only on tap.
- Interactive emoji: a tap on a large emoji plays the full-screen effect for that emoji (for example, many hearts for ❤️, fireworks for 🎉, a thumbs up that grows for 👍). Send a WebSocket event `emoji_interaction` to the other user. If the other user has the same chat open, the effect plays on the screen of the other user at the same time.
- Dice and games: 🎲 (1 to 6), 🎯 (darts), 🏀 (basketball), ⚽ (football), 🎳 (bowling), 🎰 (slot machine). The server selects the result with `crypto/rand` and stores it in the message. The client plays the Lottie animation to the frame of that result. A tap replays the animation. The result does not change on replay.
- Emoji status (optional): a small animated emoji next to the name in the profile.

### 8.8 Stickers

- Formats: static WebP, animated Lottie (`.tgs`, gzipped Lottie JSON, maximum 64 KB), and video WebM (VP9 with alpha, maximum 256 KB, 3 seconds, 30 fps). The admin uploads sticker packs. Validate every file (Section 8.14).
- Safari does not play WebM with alpha correctly. Convert video stickers on the server with ffmpeg to HEVC with alpha in MP4 for Safari. Keep WebM for the other browsers. Serve the format that matches the browser.
- Sticker send: the sticker flies from the panel to its position in the chat (FLIP). The sticker plays once on arrival.
- Stickers in the chat: play once when they come into view. Loop only on tap, or always when the user enables the "Loop animated stickers" setting.
- Limit the number of Lottie animations that play at the same time: 3 on mobile and 6 on desktop. The other visible animations show their first frame. Change the limit if the frame-rate test (Section 8.14) shows that a higher limit is possible.
- Render stickers at 30 fps. Render at 60 fps only for the effects, and only on desktop.
- Sticker preview on long press: scale up with a blurred background. Swipe between stickers in the preview.
- Sticker pack preview sheet: the stickers pop in with a delay of 20 ms between each sticker.
- Recent and favourite stickers: a used sticker moves to the recent row with FLIP.
- Sticker suggestions: when the user types an emoji, a row of matching stickers slides up above the input.

### 8.9 Calls

- Incoming call screen: the avatar has expanding pulse rings (two rings, 2 s cycle). The accept button bounces a small amount each 2 s. The ringtone and the vibration pattern (where available) repeat until the user answers or 45 seconds pass.
- Call background: an animated gradient with four colours that move slowly (the same as the Telegram call screen). The colours change with the call state: a neutral set for "ringing" and "connecting", a green set for "connected", and an orange set for "reconnecting". Cross-fade between the sets in 1 s. Build it with a canvas or a CSS mesh gradient. Stop it in reduced-motion mode.
- Avatar in a voice call: a blob behind the avatar grows and shrinks with the voice level of the remote user (`AnalyserNode` on the remote track). A second blob shows the local level.
- State text: "Ringing…", "Connecting…", "Connected" with a dots animation. The duration timer starts with a cross-fade.
- Encryption emoji: the four emoji scale in one after the other, with a delay of 80 ms between each emoji, when the connection is complete. A tap on the emoji shows a card with the explanation, with scale and fade.
- Control buttons (mute, camera, speaker, screen share, end): a tap scales the button to 0.9 and back. The mute button cross-fades its icon and changes its background with `--dur-fast`.
- Camera on: the local video grows from the camera button to the picture-in-picture corner (shared-element). The remote video fades in when the first frame arrives.
- Switch camera: a flip animation (rotate 180 degrees on the Y axis) on the local video.
- Video call layout: the user can drag the local video to the four corners with a spring snap.
- Picture-in-picture when the user leaves the call screen: the call screen shrinks to the small floating window (shared-element). A tap on the window grows it back.
- Quality indicator: the signal bars change with a cross-fade. A "Weak network" label slides down from the top.
- End call: the controls slide down, and the screen fades to the rating card. The stars fill one after the other on tap.
- Group call tiles: new tiles scale in. Removed tiles scale out, and the grid moves with FLIP. The active speaker tile gets a bright accent border that pulses with the voice level. The grid changes layout (2, 4, 9, 16 tiles) with FLIP.
- Group call audio-only participants: the avatar list shows the voice blob of each speaking user. The muted icon scales in on the avatar.
- Raise hand: the hand icon scales in with `--ease-spring` and moves from side to side once. The admin list of raised hands slides in.
- Join and leave: a small toast with the name and avatar slides up at the bottom, and slides out after 3 s.
- Screen share: the shared screen tile grows to the main position with FLIP. A "You are sharing your screen" bar with a stop button slides down.
- Recording indicator: a red dot that blinks each second next to the title.
- Live stream (channel): the "LIVE" label pulses. The viewer count rolls.

### 8.10 Wallpaper and Theme

- Chat wallpaper: an animated gradient with four colours (the same as the Telegram animated backgrounds). Each time the user sends a message, the gradient rotates its colour positions with a 500 ms `--ease-in-out` animation. Offer six dark gradient sets and a pattern overlay (an SVG pattern with low opacity). Build it with a canvas with a 4-point mesh in a low-resolution buffer (for example 50 by 50 pixels), scaled to the screen, so that the GPU cost is low.
- Accent colour change: a circular reveal from the position of the selected colour. The new colour grows as a circle over the whole screen (`--dur-slow`). Use the View Transitions API when available.
- Message text size: the bubbles change size with a 200 ms transition on the font size.
- Hover states (desktop): a background tint with `--dur-fast`.
- Press states (mobile): a tint on touch start, with no delay.

### 8.11 Loading, Progress, and Feedback

- Skeleton loading: the chat list and the message list show grey placeholder rows with a moving highlight (1.5 s cycle) while data loads from the server. Show the IndexedDB cache first, without a skeleton, when a cache exists.
- Pull to refresh (mobile): a circular progress at the top. The circle draws with the pull distance, then rotates while the data loads.
- Spinner: a circular arc that rotates, with a change of the arc length during the rotation.
- Toast and undo: a toast slides up from the bottom with a small circular countdown next to the Undo button (5 s). The toast slides down at the end or on tap.
- Connection state banner: slides down from the header with "Waiting for network…" and a dots animation. It slides up when the connection returns.
- Copy to clipboard: a toast with a check icon that draws its stroke.
- Error shake: a horizontal shake of 3 cycles and 6 px on a form field with an error.
- Haptics: use the Vibration API for swipe to reply, reaction add, long press menu, and voice record lock, with short patterns of 10 to 20 ms. The Vibration API is not available in iOS Safari. Do not show an error when the API is not available.

### 8.12 Stories (Optional, with Section 6.14)

- Story ring around the avatar: a gradient ring that rotates slowly when the story is unseen, and a grey ring when it is seen.
- Open a story: the avatar grows into the full-screen story (shared-element).
- Progress segments at the top: one segment for each story. A segment fills in the story time. Hold to pause. Tap on the sides to go forward and back.
- Change between users: the Telegram cube rotation (a 3D transform of the two story views around a vertical axis). The swipe follows the finger.
- Reply to a story: the input slides up.
- Close: drag down to dismiss with scale.

### 8.13 Reduced Motion and Power Saving

- Obey the system setting `prefers-reduced-motion: reduce`. In this mode: change every movement animation to a fade of 100 ms, do not move the chat list rows (the list updates in place), show the first frame of stickers and animated emoji (play only on tap), stop the wallpaper animation, stop the call background animation, and remove the spoiler particles (use a static cover).
- Add a "Power saving" settings page, the same as Telegram, with these switches: animated stickers, animated emoji, GIF auto-play, video auto-play, animated wallpaper, and interface animations. Add an "Auto" mode that turns power saving on when the battery is below 20%. Use the Battery Status API where the browser gives it. iOS Safari does not give it.
- When the tab is in the background, stop all animations and all media auto-play.

### 8.14 Performance and Security Tests for Animations

- Frame-rate test with Playwright and Chrome DevTools Protocol tracing. Measure dropped frames during: (1) a scroll through 10,000 messages, (2) 20 animated stickers in view, (3) 20 open and close chat transitions, (4) the message send flight with 50 messages in the list, (5) a group call with 16 video tiles. Target: less than 5% dropped frames on the test PC. Record the results in `docs/chat/LOADTEST.md`. Test on a real Android phone and an iPhone by hand, and record the results in `docs/chat/MANUAL_TESTS.md`.
- Memory test: open 50 chats one after the other. The memory of the tab must not grow by more than 50 MB. Destroy each Lottie instance when its element is removed (no leak).
- Lottie security: Lottie JSON can contain expressions (JavaScript). Use the light build of `lottie-web`, which does not run expressions. Also remove the expression fields from the JSON on upload. Reject the file if the JSON still contains expressions after the removal. Limit the JSON size and the layer count. This is security check 16 (Section 10).
- Sticker and emoji files come only from the local server. The client never loads an animation from an external URL. This is security check 17 (Section 10).

## 9. Build Order

Do the phases in this order. At the end of each phase, all tests run.

Build the animation foundation (Section 8.1) in Phase 1. Build each animation in the phase of its feature. Phase 12 is a polish pass over all animations, with the frame-rate test.

| Phase | Content |
|---|---|
| 0 | Prerequisites (Section 4) |
| 1 | WebSocket, private chats, Saved Messages, text messages, read state, typing, presence |
| 2 | Reply, forward, edit, delete, pin, reactions, formatting, drafts, mentions |
| 3 | Media, files, voice messages, video messages, albums, link preview |
| 4 | Groups, supergroups, topics, channels, admin rights, invite links, admin log |
| 5 | Chat list folders, archive, mute, global search, OCR search |
| 6 | Notifications, multi-device sessions, 2FA, QR login |
| 7 | One-to-one calls |
| 8 | Group calls, screen share, recording, live streams |
| 9 | Bots |
| 10 | Secret chats |
| 11 | Polls, stickers, GIFs, location, scheduled messages, translation |
| 12 | Animation polish pass, frame-rate test, memory test (Section 8.14) |
| 13 | Stories (optional) |

## 10. Security Requirements

These are the security tests the server must pass. Each check must pass.

1. A user cannot read, edit, delete, or react to a message in a chat where the user is not a member.
2. A user cannot get a media file by ID or by hash without membership in a chat that contains it.
3. A banned or restricted user cannot do the blocked action through the REST API or the WebSocket.
4. A WebSocket connection with an old `TokenVersion` closes.
5. Stored XSS: send every formatting entity with HTML and script payloads. Nothing executes.
6. Upload of an executable file renamed to `.jpg` is rejected.
7. Path traversal in upload names and download IDs is rejected.
8. Link preview cannot reach `127.0.0.1`, `192.168.x.x`, `10.x.x.x`, `169.254.169.254`, or IPv6 local addresses, also through redirects and DNS rebinding.
9. TURN credentials expire. An expired credential cannot relay traffic.
10. A user cannot join a call in a chat where the user is not a member.
11. Rate limits: messages (20 per second per user), uploads, login attempts, invite link joins.
12. Bot tokens are stored only as hashes. A bot cannot read chats where it is not a member.
13. CSRF protection on all state-changing REST endpoints.
14. WebSocket `Origin` header check.
15. The server never logs message text or secret chat ciphertext.
16. An uploaded Lottie sticker that contains an expression (JavaScript) is rejected, and no expression ever runs on the client.
17. The client never loads a sticker, emoji, effect, or wallpaper animation from an external URL. The Content Security Policy blocks it.


## 11. Testing

- Go unit tests for every service. Use table tests.
- Integration tests against a test PostgreSQL database.
- `go test -race ./...` must pass.
- WebSocket load test: 500 clients, each sends 1 message per second for 10 minutes. Record latency p50, p95, p99.
- Browser end-to-end tests with Playwright: two browser contexts (two users) for chat flows and a call flow with fake media devices (`--use-fake-device-for-media-stream`).
- Frame-rate and memory tests for the animations (Section 8.14).
- Reduced-motion test: run the Playwright chat flows with `reducedMotion: 'reduce'` and confirm that no element moves more than 0 px during a transition.
