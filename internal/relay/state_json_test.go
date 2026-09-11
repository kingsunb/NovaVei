package relay

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRequestStateJSONMasksAPIKeyAndAddsDurationMS(t *testing.T) {
	secret := "sk-live-very-secret-ABCD"
	state := RequestState{
		ID:        7,
		Status:    StatusSuccess,
		StartedAt: time.Unix(100, 0).UTC(),
		Duration:  1500 * time.Millisecond,
		Model:     "demo",
		ClientIP:  "203.0.113.10",
		APIKey:    secret,
		Attempts:  []AttemptRecord{},
	}

	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal RequestState: %v", err)
	}
	body := string(encoded)
	if strings.Contains(body, secret) {
		t.Fatalf("serialized state leaked full API key: %s", body)
	}

	var got struct {
		APIKey     string `json:"api_key"`
		Duration   int64  `json:"duration"`
		DurationMS int64  `json:"duration_ms"`
	}
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("unmarshal serialized state: %v", err)
	}
	if got.APIKey != "...ABCD" {
		t.Fatalf("api_key = %q, want masked tail ...ABCD", got.APIKey)
	}
	if got.Duration != int64(1500*time.Millisecond) {
		t.Fatalf("duration = %d, want legacy nanoseconds %d", got.Duration, int64(1500*time.Millisecond))
	}
	if got.DurationMS != 1500 {
		t.Fatalf("duration_ms = %d, want 1500", got.DurationMS)
	}
}

func TestRequestStateJSONMasksRunningAPIKey(t *testing.T) {
	secret := "sk-live-running-secret-WXYZ"
	state := RequestState{
		ID:        8,
		Status:    StatusRunning,
		StartedAt: time.Now().UTC(),
		Duration:  0,
		Model:     "demo-running",
		ClientIP:  "203.0.113.99",
		APIKey:    secret,
		Attempts:  []AttemptRecord{},
	}

	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal running RequestState: %v", err)
	}
	body := string(encoded)
	if strings.Contains(body, secret) {
		t.Fatalf("running serialized state leaked full API key: %s", body)
	}

	var got struct {
		APIKey     string `json:"api_key"`
		DurationMS int64  `json:"duration_ms"`
	}
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("unmarshal running serialized state: %v", err)
	}
	if got.APIKey != "...WXYZ" {
		t.Fatalf("running api_key = %q, want masked tail ...WXYZ", got.APIKey)
	}
	if got.DurationMS != 0 {
		t.Fatalf("running duration_ms = %d, want 0", got.DurationMS)
	}
}

func TestMaskAPIKey(t *testing.T) {
	cases := map[string]string{
		"":                         "",
		"...ABCD":                  "...ABCD",
		"sk-short":                 "...hort",
		"sk-live-very-secret-ABCD": "...ABCD",
	}
	for input, want := range cases {
		if got := maskAPIKey(input); got != want {
			t.Errorf("maskAPIKey(%q) = %q, want %q", input, got, want)
		}
	}
}

// TestRequestStateJSONAttemptsNeverNull 技术债回归: attempts 契约一致。
// 修复前 MarshalJSON 构造了非 nil 的 attempts 局部值, 却仍序列化 r.Attempts(可能 nil),
// 导致 nil 时输出 "attempts":null 而非 "attempts":[]。前端按数组迭代时 null 会报错。
func TestRequestStateJSONAttemptsNeverNull(t *testing.T) {
	// nil attempts 必须序列化为 []。
	emptyState := RequestState{
		ID:        42,
		Status:    StatusSuccess,
		StartedAt: time.Unix(100, 0).UTC(),
		Model:     "demo",
		ClientIP:  "203.0.113.20",
		Attempts:  nil,
	}
	encoded, err := json.Marshal(emptyState)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(encoded), `"attempts":null`) {
		t.Fatalf("nil attempts 不应序列化为 null: %s", encoded)
	}
	var got struct {
		Attempts []AttemptRecord `json:"attempts"`
	}
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Attempts == nil {
		t.Fatalf("解码后 attempts 不应为 nil, 应为空切片: %s", encoded)
	}
	if len(got.Attempts) != 0 {
		t.Fatalf("空 attempts 应解码为长度 0 切片, 实际 %d: %s", len(got.Attempts), encoded)
	}

	// 非 nil attempts 正常序列化。
	populatedState := RequestState{
		ID:        43,
		Status:    StatusFailed,
		StartedAt: time.Unix(200, 0).UTC(),
		Model:     "demo2",
		ClientIP:  "203.0.113.21",
		Attempts: []AttemptRecord{
			{Seq: 1, ChannelID: 7, ChannelName: "ch", Model: "m", Outcome: AttemptFailed, ErrClass: ErrClassTimeout, ErrBrief: "boom"},
		},
	}
	encoded, err = json.Marshal(populatedState)
	if err != nil {
		t.Fatalf("marshal populated: %v", err)
	}
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("unmarshal populated: %v", err)
	}
	if len(got.Attempts) != 1 || got.Attempts[0].Seq != 1 || got.Attempts[0].Outcome != AttemptFailed {
		t.Fatalf("非 nil attempts 序列化/反序列化不符: %+v", got.Attempts)
	}
}
