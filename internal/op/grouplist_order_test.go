package op

import (
	"testing"

	"github.com/kingsunb/NovaVeil/internal/model"
)

func TestGroupListOrdersByDisplayOrderThenName(t *testing.T) {
	t.Cleanup(groupCache.Clear)
	groupCache.Clear()

	for _, g := range []model.Group{
		{ID: 1, Name: "zeta", DisplayOrder: 2},
		{ID: 2, Name: "alpha"},
		{ID: 3, Name: "mid", DisplayOrder: 1},
		{ID: 4, Name: "beta"},
	} {
		groupCache.Set(g.ID, g)
	}

	got := GroupList()
	want := []int{3, 1, 2, 4} // 自定义序(1,2)在前，未自定义的按名称 alpha/beta
	for i, id := range want {
		if got[i].ID != id {
			t.Fatalf("position %d: want group %d, got %d (order: %v)", i, id, got[i].ID, groupIDs(got))
		}
	}
}

func TestGroupListFallsBackToNameWhenNoCustomOrder(t *testing.T) {
	t.Cleanup(groupCache.Clear)
	groupCache.Clear()

	for _, g := range []model.Group{
		{ID: 1, Name: "zeta"},
		{ID: 2, Name: "alpha"},
		{ID: 3, Name: "mid"},
	} {
		groupCache.Set(g.ID, g)
	}

	got := GroupList()
	want := []int{2, 3, 1}
	for i, id := range want {
		if got[i].ID != id {
			t.Fatalf("position %d: want group %d, got %d (order: %v)", i, id, got[i].ID, groupIDs(got))
		}
	}
}

func groupIDs(groups []model.Group) []int {
	ids := make([]int, len(groups))
	for i, g := range groups {
		ids[i] = g.ID
	}
	return ids
}
