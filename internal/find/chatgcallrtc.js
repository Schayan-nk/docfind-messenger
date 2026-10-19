// chatgcallrtc.js: one group call's WebRTC (docs/chat/SPEC.md 6.9). The page
// has a single connection to docfind's media server (internal/calls/sfu.go),
// which makes every offer: its first three transceivers are what this page
// sends (microphone, camera, screen); every other one is a colleague's
// stream, named "<user>|<kind>". Turning the camera on or sharing the screen
// is a replaceTrack, never a new negotiation.
import { S } from '/chatstate.js';

const AUDIO = { echoCancellation: true, noiseSuppression: true, autoGainControl: true };
// Every picture goes through docfind to everyone else in the call: kept
// small, so a call of ten does not fill the office's link.
const CAM_BPS = 350000, SCREEN_BPS = 1200000;
function cap(sender, bps) {
  try {
    const p = sender.getParameters();
    if (!p.encodings || !p.encodings.length) return;
    p.encodings[0].maxBitrate = bps;
    sender.setParameters(p).catch(() => { });
  } catch { }
}
const relayOnly = () => { try { return localStorage.getItem('docfind.calls.relay') === '1'; } catch { return false; } };

export class GroupCall extends EventTarget {
  constructor(info, ice, video) {
    super();
    Object.assign(this, { id: info.id, chat: info.chat, info, ice: ice || [] });
    this.mic = true; this.cam = !!video; this.screen = false; this.hand = false; this.held = false;
    this.remote = new Map(); // user -> { mic, cam, screen } MediaStreams
    this.pending = []; this.closed = false; this.sent = false;
  }
  emit(type, detail) { this.dispatchEvent(new CustomEvent(type, { detail })); }

  // media: the microphone (and the camera), asked for at the press that
  // joined; a video call on a device without a camera goes on as voice.
  async media() {
    let s;
    try { s = await navigator.mediaDevices.getUserMedia({ audio: AUDIO, video: this.cam ? { width: { ideal: 640 }, height: { ideal: 480 } } : false }); }
    catch (e) { if (!this.cam || (e && e.name === 'NotAllowedError')) throw e; s = await navigator.mediaDevices.getUserMedia({ audio: AUDIO }); }
    if (this.closed) { s.getTracks().forEach(t => t.stop()); throw new Error('closed'); }
    this.local = s; this.cam = s.getVideoTracks().length > 0;
    return s;
  }

  open() {
    const pc = this.pc = new RTCPeerConnection({ iceServers: this.ice, iceTransportPolicy: relayOnly() ? 'relay' : 'all', bundlePolicy: 'max-bundle' });
    pc.onicecandidate = e => { if (e.candidate) this.signal({ cand: e.candidate.toJSON() }); };
    pc.ontrack = e => {
      const id = (e.streams[0] && e.streams[0].id) || '';
      const [user, kind] = id.split('|');
      if (!user || !kind || user === S.me) return;
      const r = this.remote.get(user) || {};
      r[kind] = e.streams[0]; this.remote.set(user, r);
      e.track.onunmute = () => this.emit('remote', { user, kind });
      e.track.onended = () => this.emit('remote', { user, kind });
      this.emit('remote', { user, kind });
    };
    pc.onconnectionstatechange = () => {
      const st = pc.connectionState;
      this.emit('state', st);
      clearTimeout(this.giveUpT);
      if (st === 'failed' || st === 'disconnected') this.giveUpT = setTimeout(() => this.emit('give-up'), 30000);
    };
    return pc;
  }

  // What the media server says: an offer (answered at once) or a candidate,
  // one at a time: a second offer must not start before the first is answered.
  onSignal(d) {
    this.chain = (this.chain || Promise.resolve()).then(() => this.handle(d)).catch(() => { });
    return this.chain;
  }
  async handle(d) {
    if (this.closed || !d) return;
    const pc = this.pc || this.open();
    if (d.sdp && d.sdp.type === 'offer') {
      await pc.setRemoteDescription(d.sdp);
      if (!this.wired) {
        this.wired = true;
        const [ta, tc, ts] = pc.getTransceivers();
        for (const t of [ta, tc, ts]) if (t) t.direction = 'sendonly';
        await ta.sender.replaceTrack(this.local.getAudioTracks()[0] || null);
        await tc.sender.replaceTrack(this.local.getVideoTracks()[0] || null);
        this.camT = tc; this.screenT = ts;
        for (const t of this.local.getAudioTracks()) t.enabled = this.mic && !this.held;
      }
      const a = await pc.createAnswer();
      await pc.setLocalDescription(a);
      this.signal({ sdp: { type: a.type, sdp: a.sdp } });
      if (!this.capped && this.camT) { this.capped = true; cap(this.camT.sender, CAM_BPS); cap(this.screenT.sender, SCREEN_BPS); }
      for (const c of this.pending.splice(0)) { try { await pc.addIceCandidate(c); } catch { } }
      if (!this.sent) { this.sent = true; this.tell(); }
    } else if (d.cand) {
      if (pc.remoteDescription) { try { await pc.addIceCandidate(d.cand); } catch { } }
      else this.pending.push(d.cand);
    }
  }
  signal(d) { S.net.send('gcall_signal', { call: this.id, d }); }
  tell() { S.net.send('gcall_media', { call: this.id, mic: this.mic && !this.held, cam: this.cam, screen: this.screen, hand: this.hand }); }

  // ------------------------------------------------------------ controls
  setMic(on) {
    if (on && this.held) return false; // an admin holds it back
    this.mic = on;
    for (const t of (this.local ? this.local.getAudioTracks() : [])) t.enabled = on;
    this.tell(); this.emit('local'); return true;
  }
  setHeld(on) { this.held = on; if (on) this.setMic(false); else this.emit('local'); }
  setHand(on) { this.hand = on; this.tell(); this.emit('local'); }
  async setCam(on) {
    if (on === this.cam) return;
    const old = this.local.getVideoTracks()[0];
    if (on) {
      const s = await navigator.mediaDevices.getUserMedia({ video: { width: { ideal: 640 }, height: { ideal: 480 } } });
      if (this.closed) { s.getTracks().forEach(t => t.stop()); return; }
      const t = s.getVideoTracks()[0];
      if (old) { this.local.removeTrack(old); old.stop(); }
      this.local.addTrack(t);
      await this.camT?.sender.replaceTrack(t);
    } else {
      if (old) { this.local.removeTrack(old); old.stop(); }
      await this.camT?.sender.replaceTrack(null);
    }
    this.cam = on; this.tell(); this.emit('local');
  }
  async shareScreen() {
    const s = await navigator.mediaDevices.getDisplayMedia({ video: { frameRate: 15 }, audio: false });
    if (this.closed) { s.getTracks().forEach(t => t.stop()); return; }
    const t = s.getVideoTracks()[0]; t.contentHint = 'detail';
    this.screenTrack = t; this.screen = true;
    t.onended = () => this.stopScreen();
    await this.screenT?.sender.replaceTrack(t);
    this.tell(); this.emit('local');
  }
  async stopScreen() {
    if (!this.screen) return;
    this.screen = false;
    if (this.screenTrack) { this.screenTrack.onended = null; this.screenTrack.stop(); this.screenTrack = null; }
    await this.screenT?.sender.replaceTrack(null);
    this.tell(); this.emit('local');
  }

  // level(stream) for the speaking rings (an AnalyserNode per voice).
  analyser(stream) {
    try {
      this.ac = this.ac || new (window.AudioContext || window.webkitAudioContext)();
      const an = this.ac.createAnalyser(); an.fftSize = 256;
      this.ac.createMediaStreamSource(stream).connect(an);
      const buf = new Uint8Array(an.fftSize);
      return () => { an.getByteTimeDomainData(buf); let s = 0; for (const v of buf) s += (v - 128) ** 2; return Math.min(1, Math.sqrt(s / buf.length) / 30); };
    } catch { return () => 0; }
  }

  close() {
    if (this.closed) return;
    this.closed = true;
    clearTimeout(this.giveUpT);
    try { this.pc && this.pc.close(); } catch { }
    for (const t of (this.local ? this.local.getTracks() : [])) t.stop();
    if (this.screenTrack) this.screenTrack.stop();
    try { this.ac && this.ac.close(); } catch { }
  }
}
