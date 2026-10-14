package hosttest

import "testing"

func TestConfig_RejectsUnknownFields(t *testing.T) {
	h := New(t).SetConfig(map[string]any{"pagez": []string{"a"}})
	var cfg struct {
		Pages []string `json:"pages"`
	}
	if err := h.Config(&cfg); err == nil {
		t.Error("Config accepted an unknown field, which the real host rejects")
	}
}
