package model

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestUserStatusJSONShape 钉死 /user/status 与 /user/login 响应形状:
// web-next 探活成功后按 username 回填 Topbar 显示, 缺字段只能回落假 admin;
// 契约变更必须同步 web-next/src/lib/types.ts 的 UserStatus。
func TestUserStatusJSONShape(t *testing.T) {
	data, err := json.Marshal(UserStatus{Username: "ops", MustChangePassword: true})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := map[string]any{"username": "ops", "must_change_password": true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("UserStatus JSON 形状漂移: got %v, want %v", got, want)
	}
}
