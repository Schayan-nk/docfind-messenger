package chat

import (
	"context"
	"encoding/json"
	"math"
	"time"
)

// Locations (SPEC 6.4): a fixed point, or a live one for 15 minutes, an hour
// or eight hours. A live location is one message whose point its sender moves
// (location_update) until the time runs out or they stop it; every device of
// every member follows through "extra" updates.

// LocationReq is a "send_location" frame.
type LocationReq struct {
	Chat    int64   `json:"chat"`
	CID     string  `json:"cid"`
	Topic   int64   `json:"topic"`
	Silent  bool    `json:"silent"`
	ReplyTo int64   `json:"reply_to"`
	Lat     float64 `json:"lat"`
	Lng     float64 `json:"lng"`
	Acc     float64 `json:"acc"`
	Live    int     `json:"live"` // seconds: 0, 900, 3600 or 28800
}

// Place is what a location message carries.
type Place struct {
	Lat     float64    `json:"lat"`
	Lng     float64    `json:"lng"`
	Acc     int        `json:"acc,omitempty"` // metres
	Until   *time.Time `json:"live_until,omitempty"`
	Stopped bool       `json:"stopped,omitempty"`
	Updated *time.Time `json:"updated,omitempty"`
}

var liveFor = map[int]bool{0: true, 900: true, 3600: true, 28800: true}

// placeLimit keeps a live location from moving more than once every two
// seconds, per sender.
var placeLimit = newLimiter(0.5, 3)

func cleanPlace(lat, lng, acc float64) (Place, error) {
	if math.IsNaN(lat) || math.IsNaN(lng) || math.IsInf(lat, 0) || math.IsInf(lng, 0) || lat < -90 || lat > 90 || lng < -180 || lng > 180 || (lat == 0 && lng == 0) {
		return Place{}, ErrBadPlace
	}
	if math.IsNaN(acc) || acc < 0 {
		acc = 0
	}
	round := func(v float64) float64 { return math.Round(v*1e6) / 1e6 }
	return Place{Lat: round(lat), Lng: round(lng), Acc: int(math.Min(acc, 100000))}, nil
}

// SendLocation sends a fixed or a live location.
func (s *Service) SendLocation(ctx context.Context, user string, r LocationReq, except *conn) (*Message, error) {
	p, err := cleanPlace(r.Lat, r.Lng, r.Acc)
	if err != nil || !liveFor[r.Live] {
		return nil, ErrBadPlace
	}
	if r.Live > 0 {
		t := s.now().Add(time.Duration(r.Live) * time.Second).UTC().Truncate(time.Second)
		p.Until = &t
	}
	extra, _ := json.Marshal(p)
	return s.sendKind(ctx, user, r.Chat, r.Topic, r.CID, r.Silent, r.ReplyTo, ActSendMedia, Outgoing{Kind: "location", Extra: extra}, except)
}

// MoveLocation moves a live location, or stops it. Only its sender may, and
// only while it is live.
func (s *Service) MoveLocation(ctx context.Context, user string, chatID, msgID int64, lat, lng, acc float64, stop bool) error {
	if _, err := s.Store.Role(ctx, chatID, user); err != nil {
		return err
	}
	m, err := s.messageIn(ctx, chatID, user, msgID)
	if err != nil {
		return err
	}
	if m.Kind != "location" || m.Sender != user {
		return ErrForbidden
	}
	var p Place
	if json.Unmarshal(m.Extra, &p) != nil {
		return ErrForbidden
	}
	now := s.now().UTC().Truncate(time.Second)
	if p.Until == nil || p.Stopped || !now.Before(*p.Until) {
		return ErrNotLive
	}
	if stop {
		p.Stopped, p.Until = true, &now
	} else {
		np, err := cleanPlace(lat, lng, acc)
		if err != nil {
			return err
		}
		if !placeLimit.allow(user) {
			return ErrRate
		}
		p.Lat, p.Lng, p.Acc = np.Lat, np.Lng, np.Acc
	}
	p.Updated = &now
	extra, _ := json.Marshal(p)
	up, err := s.Store.SetExtra(ctx, chatID, msgID, extra)
	if err != nil {
		return err
	}
	if members, err := s.Store.Members(ctx, chatID); err == nil {
		s.deliver(members, "update", up, nil)
	}
	return nil
}
