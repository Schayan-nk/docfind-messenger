package calls

import "github.com/pion/webrtc/v4"

// What the media server sends and how: its codecs, and the stream in each
// participant's connection that carries each of the others.

// codecs are Opus and VP8 only: every browser has both, and each further
// codec would be listed again in every stream's part of every offer, which
// grows with each person in the call.
func codecs(m *webrtc.MediaEngine) error {
	video := []webrtc.RTCPFeedback{{Type: "goog-remb"}, {Type: "ccm", Parameter: "fir"}, {Type: "nack"}, {Type: "nack", Parameter: "pli"}}
	for _, c := range []struct {
		kind webrtc.RTPCodecType
		p    webrtc.RTPCodecParameters
	}{
		{webrtc.RTPCodecTypeAudio, webrtc.RTPCodecParameters{RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2, SDPFmtpLine: "minptime=10;useinbandfec=1"}, PayloadType: 111}},
		{webrtc.RTPCodecTypeVideo, webrtc.RTPCodecParameters{RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8, ClockRate: 90000, RTCPFeedback: video}, PayloadType: 96}},
		{webrtc.RTPCodecTypeVideo, webrtc.RTPCodecParameters{RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeRTX, ClockRate: 90000, SDPFmtpLine: "apt=96"}, PayloadType: 97}},
	} {
		if err := m.RegisterCodec(c.p, c.kind); err != nil {
			return err
		}
	}
	return nil
}

// carry puts t on a stream of its own in pc, reusing one somebody who left
// had. Never pc.AddTrack: pion would take one of the page's own three
// (it learns which way a stream goes only when the other side offers, and
// here the server always offers), and the page only sends on those.
func (s *SFU) carry(pc *webrtc.PeerConnection, t *webrtc.TrackLocalStaticRTP) (*webrtc.RTPSender, error) {
	for i, tr := range pc.GetTransceivers() {
		if i < len(Kinds) || tr.Kind() != t.Kind() || tr.Sender() != nil || tr.Direction() != webrtc.RTPTransceiverDirectionInactive {
			continue
		}
		snd, err := s.api.NewRTPSender(t, pc.SCTP().Transport())
		if err != nil {
			return nil, err
		}
		if err := tr.SetSender(snd, t); err != nil {
			_ = snd.Stop()
			return nil, err
		}
		return snd, nil
	}
	tr, err := pc.AddTransceiverFromTrack(t, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionSendonly})
	if err != nil {
		return nil, err
	}
	return tr.Sender(), nil
}
