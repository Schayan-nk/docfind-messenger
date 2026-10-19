// chatcallrtc.js: one call's WebRTC (docs/chat/SPEC.md 6.8). The server only
// passes the two browsers' offer, answer and ICE candidates between them
// (call_signal); the media goes browser to browser or through docfind's own
// TURN relay. Both sides have three transceivers from the start (the voice,
// the camera, the shared screen: phase 8, a stream of its own so the camera
// can stay on), so turning the camera on, switching it or sharing the screen
// is a replaceTrack, never a new negotiation. Only the caller makes offers
// (the first one, and an ICE restart when the network changes).
import { S } from '/chatstate.js';

// The four emoji both people see (SPEC 6.8): SHA-256 of the two DTLS
// fingerprints in sorted order, 6 bits each from the first 3 bytes.
const EMOJI = ['🐶', '🐱', '🦊', '🐻', '🐼', '🐨', '🐯', '🦁', '🐮', '🐷', '🐸', '🐵', '🐔', '🐧', '🐦', '🦉',
  '🐢', '🐍', '🐙', '🦋', '🐌', '🐞', '🐝', '🦀', '🐬', '🐳', '🌵', '🌲', '🌻', '🌹', '🍁', '🍄',
  '🍎', '🍋', '🍉', '🍇', '🍓', '🍒', '🥝', '🥕', '🌽', '🍞', '🧀', '🍕', '🎂', '🍩', '☕', '🍵',
  '⚽', '🏀', '🎾', '🎲', '🎸', '🎺', '🚲', '🚗', '✈️', '🚀', '⛵', '⏰', '💡', '🔑', '🎁', '⭐'];

const RESTART_AFTER = 2500;   // disconnected this long: restart ICE
const GIVE_UP_AFTER = 30000;  // SPEC 6.8: no media for 30 s ends the call
const AUDIO = { echoCancellation: true, noiseSuppression: true, autoGainControl: true };
const relayOnly = () => { try { return localStorage.getItem('docfind.calls.relay') === '1'; } catch { return false; } };

export class Call extends EventTarget {
  // info: the server's CallInfo; ice: the RTCPeerConnection iceServers.
  constructor(info, ice) {
    super();
    Object.assign(this, { id: info.id, info, outgoing: info.outgoing, video: !!info.video, ice: ice || [] });
    this.state = info.outgoing ? (info.state === 'requesting' ? 'requesting' : 'ringing') : 'incoming';
    this.mic = true; this.cam = false; this.screen = false; this.facing = 'user';
    this.peerMedia = { mic: true, cam: false, screen: false };
    this.pending = []; // remote candidates that came before the remote description
    this.remote = new MediaStream();       // the other person's voice and camera
    this.remoteScreen = new MediaStream(); // their shared screen
    this.closed = false;
  }
  emit(type, detail) { this.dispatchEvent(new CustomEvent(type, { detail })); }
  setState(s) { if (this.state === s || this.closed) return; this.state = s; this.emit('state', s); }

  // media: the microphone (and the camera for a video call), asked for at
  // the press that started or answered the call.
  // A video call on a device without a working camera goes on as voice. A
  // call put down while the browser was still asking lets go of what it got.
  async media() {
    let s;
    try {
      s = await navigator.mediaDevices.getUserMedia({ audio: AUDIO, video: this.video ? { facingMode: this.facing, width: { ideal: 1280 }, height: { ideal: 720 } } : false });
    } catch (e) {
      if (!this.video || (e && e.name === 'NotAllowedError')) throw e;
      s = await navigator.mediaDevices.getUserMedia({ audio: AUDIO });
    }
    if (this.closed) { for (const t of s.getTracks()) t.stop(); throw new Error('closed'); }
    this.local = s;
    this.cam = s.getVideoTracks().length > 0;
    for (const t of s.getAudioTracks()) t.enabled = this.mic;
    return s;
  }

  open() {
    const pc = this.pc = new RTCPeerConnection({ iceServers: this.ice, iceTransportPolicy: relayOnly() ? 'relay' : 'all', bundlePolicy: 'max-bundle' });
    pc.onicecandidate = e => { if (e.candidate) this.signal({ cand: e.candidate.toJSON() }); };
    pc.ontrack = e => {
      // The third transceiver is the screen (on the answering side this runs
      // before screenT is known, so by position).
      const screen = pc.getTransceivers().indexOf(e.transceiver) === 2;
      const into = screen ? this.remoteScreen : this.remote;
      if (!into.getTracks().includes(e.track)) into.addTrack(e.track);
      const kind = screen ? 'screen' : e.track.kind;
      e.track.onunmute = () => this.emit('remote', kind);
      this.emit('remote', kind);
    };
    pc.onconnectionstatechange = () => this.watch();
    pc.oniceconnectionstatechange = () => this.watch();
    return pc;
  }

  // The caller: both transceivers, then the offer once the call is answered.
  async offer(restart = false) {
    const pc = this.pc || this.open();
    // One offer at a time: a restart waits until the last one is answered.
    if (restart && pc.signalingState !== 'stable') return;
    if (!this.audioT) {
      const a = this.local.getAudioTracks()[0], v = this.local.getVideoTracks()[0] || null;
      this.audioT = pc.addTransceiver(a, { direction: 'sendrecv', streams: [this.local] });
      this.videoT = pc.addTransceiver('video', { direction: 'sendrecv', streams: [this.local] });
      this.screenT = pc.addTransceiver('video', { direction: 'sendrecv' });
      if (v) await this.videoT.sender.replaceTrack(v);
    }
    const o = await pc.createOffer({ iceRestart: restart });
    await pc.setLocalDescription(o);
    this.signal({ sdp: { type: o.type, sdp: o.sdp } });
  }

  // What the other browser said.
  async onSignal(d) {
    if (this.closed || !d) return;
    const pc = this.pc || this.open();
    if (d.sdp && d.sdp.type === 'offer') {
      await pc.setRemoteDescription(d.sdp);
      if (!this.audioT) {
        const [ta, tv, ts] = pc.getTransceivers();
        this.audioT = ta; this.videoT = tv; this.screenT = ts;
        ta.direction = 'sendrecv'; tv.direction = 'sendrecv'; if (ts) ts.direction = 'sendrecv';
        await ta.sender.replaceTrack(this.local.getAudioTracks()[0] || null);
        await tv.sender.replaceTrack(this.local.getVideoTracks()[0] || null);
        ta.sender.setStreams && ta.sender.setStreams(this.local);
      }
      const a = await pc.createAnswer();
      await pc.setLocalDescription(a);
      this.signal({ sdp: { type: a.type, sdp: a.sdp } });
      this.flush(); this.fingerprint();
    } else if (d.sdp && d.sdp.type === 'answer') {
      if (pc.signalingState !== 'have-local-offer') return;
      await pc.setRemoteDescription(d.sdp);
      this.flush(); this.fingerprint();
    } else if (d.cand) {
      if (pc.remoteDescription) { try { await pc.addIceCandidate(d.cand); } catch { } }
      else this.pending.push(d.cand);
    }
  }
  async flush() { const p = this.pending; this.pending = []; for (const c of p) { try { await this.pc.addIceCandidate(c); } catch { } } }
  signal(d) { S.net.send('call_signal', { call: this.id, d }); }
  tell() { S.net.send('call_media', { call: this.id, d: { mic: this.mic, cam: this.cam, screen: this.screen } }); }

  // watch follows the connection: connected (the duration starts),
  // reconnecting (the caller restarts ICE), and gives up after 30 s.
  watch() {
    const pc = this.pc; if (!pc || this.closed) return;
    const s = pc.connectionState || pc.iceConnectionState;
    if (s === 'connected' || pc.iceConnectionState === 'connected' || pc.iceConnectionState === 'completed') {
      clearTimeout(this.restartT); clearTimeout(this.giveUpT); this.restartT = this.giveUpT = null;
      // Once connected, each side says what it sends (mic, camera, screen):
      // until it does, the other shows the avatar, not the picture.
      if (!this.since) { this.since = Date.now(); S.net.send('call_state', { call: this.id, state: 'connected' }); this.tell(); this.startStats(); }
      this.setState('connected');
      return;
    }
    if (s === 'disconnected' || s === 'failed') {
      if (this.since) this.setState('reconnecting');
      if (!this.restartT) this.restartT = setTimeout(() => { this.restartT = null; this.restart(); }, s === 'failed' ? 0 : RESTART_AFTER);
      if (!this.giveUpT) this.giveUpT = setTimeout(() => this.emit('give-up'), GIVE_UP_AFTER);
    }
  }
  // restart asks for new network paths (ICE restart): the caller offers, the
  // callee asks the caller to (call_media restart).
  restart() {
    if (this.closed || !this.pc) return;
    if (this.outgoing) this.offer(true).catch(() => { });
    else S.net.send('call_media', { call: this.id, d: { mic: this.mic, cam: this.cam, screen: this.screen, restart: true } });
  }
  onPeerMedia(d) {
    if (!d) return;
    if (d.restart && this.outgoing) { this.restart(); return; }
    this.peerMedia = { mic: d.mic !== false, cam: !!d.cam, screen: !!d.screen };
    this.emit('peer-media', this.peerMedia);
  }

  // ------------------------------------------------------------ controls
  setMic(on) { this.mic = on; for (const t of (this.local ? this.local.getAudioTracks() : [])) t.enabled = on; this.tell(); this.emit('local'); }
  async setCam(on) {
    if (on === this.cam) return;
    const old = this.local.getVideoTracks()[0];
    if (on) {
      const s = await navigator.mediaDevices.getUserMedia({ video: { facingMode: this.facing, width: { ideal: 1280 }, height: { ideal: 720 } } });
      if (this.closed) { s.getTracks().forEach(x => x.stop()); return; }
      const t = s.getVideoTracks()[0];
      if (old) { this.local.removeTrack(old); old.stop(); }
      this.local.addTrack(t);
      await this.videoT?.sender.replaceTrack(t);
      this.video = true;
    } else {
      if (old) { this.local.removeTrack(old); old.stop(); }
      await this.videoT?.sender.replaceTrack(null);
    }
    this.cam = on; this.tell(); this.emit('local');
    if (on) { try { window.DocfindAndroid && window.DocfindAndroid.callStart && window.DocfindAndroid.callStart(true); } catch { } }
  }
  async flipCam() {
    if (!this.cam) return;
    this.facing = this.facing === 'user' ? 'environment' : 'user';
    const old = this.local.getVideoTracks()[0];
    const s = await navigator.mediaDevices.getUserMedia({ video: { facingMode: { exact: this.facing } } }).catch(() => navigator.mediaDevices.getUserMedia({ video: { facingMode: this.facing } }));
    const t = s.getVideoTracks()[0];
    if (old) { this.local.removeTrack(old); old.stop(); }
    this.local.addTrack(t);
    await this.videoT?.sender.replaceTrack(t);
    this.emit('local', 'flip');
  }
  // The screen goes in its own stream (phase 8); the camera is untouched.
  async shareScreen() {
    const s = await navigator.mediaDevices.getDisplayMedia({ video: { frameRate: 15 }, audio: false });
    if (this.closed) { s.getTracks().forEach(x => x.stop()); return; } // the call ended while the browser asked
    const t = s.getVideoTracks()[0];
    t.contentHint = 'detail';
    this.screenTrack = t; this.screen = true;
    t.onended = () => this.stopScreen(); // the browser's own «stop sharing»
    await this.screenT?.sender.replaceTrack(t);
    this.video = true; this.tell(); this.emit('local', 'screen');
  }
  async stopScreen() {
    if (!this.screen) return;
    this.screen = false;
    if (this.screenTrack) { this.screenTrack.onended = null; this.screenTrack.stop(); this.screenTrack = null; }
    await this.screenT?.sender.replaceTrack(null);
    this.tell(); this.emit('local', 'screen');
  }
  // Speaker or earpiece, where the browser lets a page choose its output.
  async outputs() {
    if (!('setSinkId' in HTMLMediaElement.prototype) || !navigator.mediaDevices.enumerateDevices) return [];
    return (await navigator.mediaDevices.enumerateDevices()).filter(d => d.kind === 'audiooutput');
  }

  // ------------------------------------------------------------ levels and quality
  // level(stream) is 0..1 for the voice blobs (an AnalyserNode on the track).
  analyser(stream) {
    try {
      this.ac = this.ac || new (window.AudioContext || window.webkitAudioContext)();
      const an = this.ac.createAnalyser(); an.fftSize = 256;
      this.ac.createMediaStreamSource(stream).connect(an);
      const buf = new Uint8Array(an.fftSize);
      return () => { an.getByteTimeDomainData(buf); let s = 0; for (const v of buf) s += (v - 128) ** 2; return Math.min(1, Math.sqrt(s / buf.length) / 30); };
    } catch { return () => 0; }
  }
  startStats() {
    clearInterval(this.statsT);
    let lastLost = 0, lastGot = 0;
    this.statsT = setInterval(async () => {
      if (!this.pc || this.closed) return;
      let rtt = 0, lost = 0, got = 0, relay = false;
      const stats = await this.pc.getStats().catch(() => null); if (!stats) return;
      const byId = new Map(); stats.forEach(r => byId.set(r.id, r));
      stats.forEach(r => {
        if (r.type === 'candidate-pair' && (r.nominated || r.selected) && r.state === 'succeeded') {
          rtt = r.currentRoundTripTime || rtt;
          const lc = byId.get(r.localCandidateId); relay = relay || (lc && lc.candidateType === 'relay');
        }
        if (r.type === 'inbound-rtp' && r.kind === 'audio') { lost += r.packetsLost || 0; got += r.packetsReceived || 0; }
      });
      const dl = lost - lastLost, dg = got - lastGot; lastLost = lost; lastGot = got;
      const loss = dg + dl > 0 ? dl / (dg + dl) : 0;
      // 4 bars: under 150 ms and 2 % loss; 1 bar: over 600 ms or 15 %.
      const bars = rtt > 0.6 || loss > 0.15 ? 1 : rtt > 0.35 || loss > 0.07 ? 2 : rtt > 0.15 || loss > 0.02 ? 3 : 4;
      this.emit('quality', { bars, rtt, loss, relay });
    }, 2000);
  }

  // The four emoji, once both fingerprints are known.
  async fingerprint() {
    if (this.emoji || !this.pc.localDescription || !this.pc.remoteDescription) return;
    const fp = sdp => ((/a=fingerprint:(\S+ \S+)/i.exec(sdp || '') || [])[1] || '').toUpperCase();
    const both = [fp(this.pc.localDescription.sdp), fp(this.pc.remoteDescription.sdp)];
    if (!both[0] || !both[1]) return;
    both.sort();
    const h = new Uint8Array(await crypto.subtle.digest('SHA-256', new TextEncoder().encode(both.join('|'))));
    const n = (h[0] << 16) | (h[1] << 8) | h[2];
    this.emoji = [18, 12, 6, 0].map(sh => EMOJI[(n >> sh) & 63]);
    this.emit('emoji', this.emoji);
  }

  close() {
    if (this.closed) return;
    this.closed = true;
    clearInterval(this.statsT); clearTimeout(this.restartT); clearTimeout(this.giveUpT);
    try { this.pc && this.pc.close(); } catch { }
    for (const t of (this.local ? this.local.getTracks() : [])) t.stop();
    if (this.screenTrack) this.screenTrack.stop();
    try { this.ac && this.ac.close(); } catch { }
  }
}
