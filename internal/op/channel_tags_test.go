package op

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/kingsunb/NovaVei/internal/db"
	"github.com/kingsunb/NovaVei/internal/model"
)

func TestNormalizeChannelTags(t *testing.T) {
	cases := []struct {
		name  string
		input []string
		want  []string
	}{
		{"nil 保持 nil", nil, nil},
		{"空切片保持空切片", []string{}, []string{}},
		{
			"trim 去空白并剔除空串",
			[]string{"  free  ", "\t稳定\n", "   ", ""},
			[]string{"free", "稳定"},
		},
		{
			"trim 后去重且保持首次出现顺序",
			[]string{"b", " a ", "a", "c", "b", " c"},
			[]string{"b", "a", "c"},
		},
		{"全部为空白时返回空切片", []string{" ", "\t", ""}, []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeChannelTags(tc.input)
			if tc.want == nil {
				if got != nil {
					t.Fatalf("normalizeChannelTags(%v) = %v, want nil", tc.input, got)
				}
				return
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("normalizeChannelTags(%v) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

// createTagSortChannel 创建带标签与排序值的渠道。
func createTagSortChannel(t *testing.T, name string, tags []string, sortOrder int) model.Channel {
	t.Helper()
	channel := model.Channel{
		Name:    name,
		Type:    model.ChannelProviderOpenAI,
		BaseURL: "https://example.invalid",
		Tags:    tags,
		Sort:    sortOrder,
	}
	if err := ChannelCreate(&channel, context.Background()); err != nil {
		t.Fatalf("ChannelCreate: %v", err)
	}
	return channel
}

func assertTags(t *testing.T, got, want []string, what string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}

func TestChannelTagsAndSortPersistRoundtrip(t *testing.T) {
	ctx := context.Background()
	channel := createTagSortChannel(t, fmt.Sprintf("tags-roundtrip-%d", time.Now().UnixNano()),
		[]string{"  free ", "", "稳定", "free"}, 7)

	// 创建即归一化: trim + 去空串 + 去重。
	assertTags(t, channel.Tags, []string{"free", "稳定"}, "created tags")

	var stored model.Channel
	if err := db.GetDB().First(&stored, channel.ID).Error; err != nil {
		t.Fatalf("load stored channel: %v", err)
	}
	if stored.Sort != 7 {
		t.Fatalf("stored sort = %d, want 7", stored.Sort)
	}
	assertTags(t, stored.Tags, []string{"free", "稳定"}, "stored tags")

	newTags := []string{" paid ", "x", ""}
	newSort := 3
	if _, err := ChannelUpdate(&model.ChannelUpdateRequest{ID: channel.ID, Tags: &newTags, Sort: &newSort}, ctx); err != nil {
		t.Fatalf("ChannelUpdate: %v", err)
	}

	stored = model.Channel{}
	if err := db.GetDB().First(&stored, channel.ID).Error; err != nil {
		t.Fatalf("reload stored channel: %v", err)
	}
	if stored.Sort != 3 {
		t.Fatalf("sort not persisted, got %d, want 3", stored.Sort)
	}
	assertTags(t, stored.Tags, []string{"paid", "x"}, "updated stored tags")

	updated, err := ChannelGet(channel.ID)
	if err != nil {
		t.Fatalf("ChannelGet: %v", err)
	}
	if updated.Sort != 3 {
		t.Fatalf("cached sort = %d, want 3", updated.Sort)
	}
	assertTags(t, updated.Tags, []string{"paid", "x"}, "cached tags")

	// nil 不覆盖既有值。
	unchangedSort := stored.Sort
	unchangedTags := slices.Clone(stored.Tags)
	newName := "renamed-tags"
	if _, err := ChannelUpdate(&model.ChannelUpdateRequest{ID: channel.ID, Name: &newName}, ctx); err != nil {
		t.Fatalf("ChannelUpdate without tags/sort: %v", err)
	}
	stored = model.Channel{}
	if err := db.GetDB().First(&stored, channel.ID).Error; err != nil {
		t.Fatalf("reload after no-op update: %v", err)
	}
	if stored.Sort != unchangedSort {
		t.Fatalf("nil sort must not overwrite, got %d, want %d", stored.Sort, unchangedSort)
	}
	assertTags(t, stored.Tags, unchangedTags, "tags after no-op update")

	// 空数组表示清除全部标签。
	clearTags := []string{}
	if _, err := ChannelUpdate(&model.ChannelUpdateRequest{ID: channel.ID, Tags: &clearTags}, ctx); err != nil {
		t.Fatalf("ChannelUpdate clear tags: %v", err)
	}
	stored = model.Channel{}
	if err := db.GetDB().First(&stored, channel.ID).Error; err != nil {
		t.Fatalf("reload after clear: %v", err)
	}
	if len(stored.Tags) != 0 {
		t.Fatalf("cleared tags must be empty, got %v", stored.Tags)
	}
}

// TestChannelTagsDeepCopyIsolation 验证缓存与对外副本的 Tags 深拷贝隔离;
// 在 -race 下由并发读写放大共享底层数组的检测灵敏度。
func TestChannelTagsDeepCopyIsolation(t *testing.T) {
	ctx := context.Background()
	channel := createTagSortChannel(t, fmt.Sprintf("tags-isolation-%d", time.Now().UnixNano()), []string{"a", "b"}, 1)

	// 直接改写源切片不得影响 cacheableChannel 副本。
	src := []string{"a", "b"}
	cloned := cacheableChannel(model.Channel{Tags: src})
	cloned.Tags[0] = "mutated"
	if src[0] != "a" {
		t.Fatal("cacheableChannel must clone Tags slice")
	}

	// 改写快照不得污染缓存。
	snapshot, err := ChannelGet(channel.ID)
	if err != nil {
		t.Fatalf("ChannelGet: %v", err)
	}
	if len(snapshot.Tags) > 0 {
		snapshot.Tags[0] = "poison"
	}
	again, err := ChannelGet(channel.ID)
	if err != nil {
		t.Fatalf("ChannelGet again: %v", err)
	}
	if len(again.Tags) > 0 && again.Tags[0] == "poison" {
		t.Fatal("snapshot mutation polluted cache")
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					ch, err := ChannelGet(channel.ID)
					if err != nil {
						t.Errorf("concurrent ChannelGet: %v", err)
						return
					}
					if len(ch.Tags) > 0 {
						ch.Tags[0] = "race-probe"
					}
					list := ChannelList()
					for j := range list {
						if len(list[j].Tags) > 0 {
							list[j].Tags[0] = "race-probe"
						}
					}
				}
			}
		}()
	}
	newTags := []string{"concurrent"}
	if _, err := ChannelUpdate(&model.ChannelUpdateRequest{ID: channel.ID, Tags: &newTags}, ctx); err != nil {
		t.Fatalf("ChannelUpdate during concurrent reads: %v", err)
	}
	close(stop)
	wg.Wait()

	final, err := ChannelGet(channel.ID)
	if err != nil {
		t.Fatalf("final ChannelGet: %v", err)
	}
	assertTags(t, final.Tags, []string{"concurrent"}, "final cached tags")
}
