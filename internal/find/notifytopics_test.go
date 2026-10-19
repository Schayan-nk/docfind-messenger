package find

import (
	"encoding/json"
	"testing"
)

func topicsOf(t *testing.T, body []byte) map[string]bool {
	t.Helper()
	var got struct {
		Topics []struct {
			ID string `json:"id"`
			On bool   `json:"on"`
		} `json:"topics"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, tp := range got.Topics {
		out[tp.ID] = tp.On
	}
	return out
}
