package op

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/kingsunb/NovaVei/internal/db"
	"github.com/kingsunb/NovaVei/internal/model"
)

// TestChannelSortAllowsDuplicateZeroNegative 验证渠道排序值允许重复、零值与负值，
// 且更新能正确持久化与缓存同步。对应用户反馈：排序值不应要求唯一，应按数值大小排序，
// 同值按名称兜底，容许负数。
func TestChannelSortAllowsDuplicateZeroNegative(t *testing.T) {
	ctx := context.Background()
	ts := time.Now().UnixNano()

	// 创建三个渠道，初始排序值各为 5。
	c1 := createTagSortChannel(t, fmt.Sprintf("sort-a-%d", ts), nil, 5)
	c2 := createTagSortChannel(t, fmt.Sprintf("sort-b-%d", ts), nil, 5)
	c3 := createTagSortChannel(t, fmt.Sprintf("sort-c-%d", ts), nil, 5)

	// 创建即持久化。
	for _, c := range []model.Channel{c1, c2, c3} {
		var stored model.Channel
		if err := db.GetDB().First(&stored, c.ID).Error; err != nil {
			t.Fatalf("load channel %d: %v", c.ID, err)
		}
		if stored.Sort != 5 {
			t.Fatalf("channel %d sort = %d, want 5", c.ID, stored.Sort)
		}
	}

	// 全部设为 0 —— 允许重复零值。
	zero := 0
	for _, id := range []int{c1.ID, c2.ID, c3.ID} {
		if _, err := ChannelUpdate(&model.ChannelUpdateRequest{ID: id, Sort: &zero}, ctx); err != nil {
			t.Fatalf("update sort to 0 for channel %d: %v", id, err)
		}
	}
	for _, id := range []int{c1.ID, c2.ID, c3.ID} {
		var stored model.Channel
		if err := db.GetDB().First(&stored, id).Error; err != nil {
			t.Fatalf("load channel %d: %v", id, err)
		}
		if stored.Sort != 0 {
			t.Fatalf("channel %d sort = %d, want 0 (duplicate zero must be allowed)", id, stored.Sort)
		}
	}

	// 全部设为 1 —— 允许重复非零值。
	one := 1
	for _, id := range []int{c1.ID, c2.ID, c3.ID} {
		if _, err := ChannelUpdate(&model.ChannelUpdateRequest{ID: id, Sort: &one}, ctx); err != nil {
			t.Fatalf("update sort to 1 for channel %d: %v", id, err)
		}
	}
	for _, id := range []int{c1.ID, c2.ID, c3.ID} {
		var stored model.Channel
		if err := db.GetDB().First(&stored, id).Error; err != nil {
			t.Fatalf("load channel %d: %v", id, err)
		}
		if stored.Sort != 1 {
			t.Fatalf("channel %d sort = %d, want 1 (duplicate non-zero must be allowed)", id, stored.Sort)
		}
	}

	// 允许负值。
	neg := -10
	if _, err := ChannelUpdate(&model.ChannelUpdateRequest{ID: c1.ID, Sort: &neg}, ctx); err != nil {
		t.Fatalf("update sort to -10: %v", err)
	}
	var stored model.Channel
	if err := db.GetDB().First(&stored, c1.ID).Error; err != nil {
		t.Fatalf("load channel %d: %v", c1.ID, err)
	}
	if stored.Sort != -10 {
		t.Fatalf("negative sort not persisted, got %d, want -10", stored.Sort)
	}
	// 缓存也同步。
	cached, err := ChannelGet(c1.ID)
	if err != nil {
		t.Fatalf("ChannelGet: %v", err)
	}
	if cached.Sort != -10 {
		t.Fatalf("cached sort = %d, want -10", cached.Sort)
	}

	// 一部分 0 一部分 1 —— 混合重复值。
	if _, err := ChannelUpdate(&model.ChannelUpdateRequest{ID: c2.ID, Sort: &zero}, ctx); err != nil {
		t.Fatalf("update c2 sort to 0: %v", err)
	}
	if _, err := ChannelUpdate(&model.ChannelUpdateRequest{ID: c3.ID, Sort: &one}, ctx); err != nil {
		t.Fatalf("update c3 sort to 1: %v", err)
	}
	// 此时 c1=-10, c2=0, c3=1，全部允许，无唯一冲突。
	for _, tc := range []struct {
		id  int
		exp int
	}{
		{c1.ID, -10},
		{c2.ID, 0},
		{c3.ID, 1},
	} {
		var s model.Channel
		if err := db.GetDB().First(&s, tc.id).Error; err != nil {
			t.Fatalf("load channel %d: %v", tc.id, err)
		}
		if s.Sort != tc.exp {
			t.Fatalf("channel %d sort = %d, want %d", tc.id, s.Sort, tc.exp)
		}
	}
}

// TestChannelSortZeroRoundtrip 验证排序值在 0 ↔ 非零 之间往返切换能正确持久化，
// 回归 BUG-004（sort 零值不保存）。
func TestChannelSortZeroRoundtrip(t *testing.T) {
	ctx := context.Background()
	ts := time.Now().UnixNano()
	c := createTagSortChannel(t, fmt.Sprintf("sort-zero-%d", ts), nil, 7)

	// 7 → 0
	zero := 0
	if _, err := ChannelUpdate(&model.ChannelUpdateRequest{ID: c.ID, Sort: &zero}, ctx); err != nil {
		t.Fatalf("update 7->0: %v", err)
	}
	var stored model.Channel
	if err := db.GetDB().First(&stored, c.ID).Error; err != nil {
		t.Fatalf("load: %v", err)
	}
	if stored.Sort != 0 {
		t.Fatalf("7->0 failed, got %d", stored.Sort)
	}

	// 0 → 3
	three := 3
	if _, err := ChannelUpdate(&model.ChannelUpdateRequest{ID: c.ID, Sort: &three}, ctx); err != nil {
		t.Fatalf("update 0->3: %v", err)
	}
	if err := db.GetDB().First(&stored, c.ID).Error; err != nil {
		t.Fatalf("load: %v", err)
	}
	if stored.Sort != 3 {
		t.Fatalf("0->3 failed, got %d", stored.Sort)
	}

	// 3 → 0（再次归零）
	if _, err := ChannelUpdate(&model.ChannelUpdateRequest{ID: c.ID, Sort: &zero}, ctx); err != nil {
		t.Fatalf("update 3->0: %v", err)
	}
	if err := db.GetDB().First(&stored, c.ID).Error; err != nil {
		t.Fatalf("load: %v", err)
	}
	if stored.Sort != 0 {
		t.Fatalf("3->0 failed, got %d", stored.Sort)
	}

	// 0 → -5（零到负）
	neg := -5
	if _, err := ChannelUpdate(&model.ChannelUpdateRequest{ID: c.ID, Sort: &neg}, ctx); err != nil {
		t.Fatalf("update 0->-5: %v", err)
	}
	if err := db.GetDB().First(&stored, c.ID).Error; err != nil {
		t.Fatalf("load: %v", err)
	}
	if stored.Sort != -5 {
		t.Fatalf("0->-5 failed, got %d", stored.Sort)
	}
}
