package op

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kingsunb/NovaVeil/internal/db"
	"github.com/kingsunb/NovaVeil/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newEvalRecord 构造一条可落库的评估记录，仅填充列表/详情需要的快照字段。
func newEvalRecord(channelID int, modelName string, outcome model.ModelEvalOutcome, content string) *model.ModelEval {
	return &model.ModelEval{
		ModelEvalSummary: model.ModelEvalSummary{
			ChannelID:   channelID,
			ChannelName: "test-channel",
			ModelName:   modelName,
			Outcome:     outcome,
			CreatedAt:   time.Now(),
			CompletedAt: time.Now(),
		},
		Prompt:  model.ModelEvalPrompt,
		Content: content,
	}
}

// cleanupEvals 删除指定主键集合的评估记录，保证共享测试库不串味。
func cleanupEvals(t *testing.T, ids ...int64) {
	t.Helper()
	if len(ids) == 0 {
		return
	}
	require.NoError(t, db.GetDB().Where("id IN ?", ids).Delete(&model.ModelEval{}).Error)
}

// TestModelEvalCreateTruncatesOversizedContent 验证超过 1MiB 的回复被截断到上限内
// 并置 ContentTruncated=true，避免大回复撑爆 SQLite/MySQL 文本列。
func TestModelEvalCreateTruncatesOversizedContent(t *testing.T) {
	ctx := context.Background()
	oversized := strings.Repeat("a", model.ModelEvalMaxContentBytes+1)
	rec := newEvalRecord(950001, "truncate-model", model.ModelEvalOK, oversized)
	require.NoError(t, ModelEvalCreate(ctx, rec))
	t.Cleanup(func() { cleanupEvals(t, rec.ID) })

	assert.True(t, rec.ContentTruncated, "超限内容必须标记截断")
	assert.LessOrEqual(t, len(rec.Content), model.ModelEvalMaxContentBytes, "截断后不得超 1MiB")
}

// TestModelEvalCreateKeepsSmallContentUntouched 验证未超限内容不截断、不置标记。
func TestModelEvalCreateKeepsSmallContentUntouched(t *testing.T) {
	ctx := context.Background()
	body := "<html>" + model.ModelEvalResultStart + "<svg/>" + model.ModelEvalResultEnd + "</html>"
	rec := newEvalRecord(950002, "small-model", model.ModelEvalOK, body)
	require.NoError(t, ModelEvalCreate(ctx, rec))
	t.Cleanup(func() { cleanupEvals(t, rec.ID) })

	assert.False(t, rec.ContentTruncated)
	assert.Equal(t, body, rec.Content)
}

// TestModelEvalCreateRedactsAndTruncatesError 验证错误信息中的 Bearer/字段密钥被脱敏，
// 且超长错误被截断到 4096 字节内。
func TestModelEvalCreateRedactsAndTruncatesError(t *testing.T) {
	ctx := context.Background()
	rec := newEvalRecord(950003, "redact-model", model.ModelEvalError, "")
	rec.Error = "upstream 401: Bearer sk-live-abcdef123 and api_key: sk-secret-xyz"
	require.NoError(t, ModelEvalCreate(ctx, rec))
	t.Cleanup(func() { cleanupEvals(t, rec.ID) })

	assert.Contains(t, rec.Error, "Bearer [REDACTED]")
	assert.Contains(t, rec.Error, "api_key: [REDACTED]")
	assert.NotContains(t, rec.Error, "sk-live-abcdef123")
	assert.NotContains(t, rec.Error, "sk-secret-xyz")
}

func TestModelEvalCreateRedactsLongError(t *testing.T) {
	ctx := context.Background()
	rec := newEvalRecord(950004, "redact-long-model", model.ModelEvalError, "")
	rec.Error = "Bearer sk-" + strings.Repeat("x", 8000)
	require.NoError(t, ModelEvalCreate(ctx, rec))
	t.Cleanup(func() { cleanupEvals(t, rec.ID) })

	assert.LessOrEqual(t, len(rec.Error), 4096, "错误摘要截断到 4096 字节内")
	assert.Contains(t, rec.Error, "[REDACTED]")
}

// TestModelEvalGetRoundtrip 验证写入后可按主键读回完整记录（含 Content）。
func TestModelEvalGetRoundtrip(t *testing.T) {
	ctx := context.Background()
	rec := newEvalRecord(950005, "get-model", model.ModelEvalOK, "hello-eval")
	require.NoError(t, ModelEvalCreate(ctx, rec))
	t.Cleanup(func() { cleanupEvals(t, rec.ID) })

	got, err := ModelEvalGet(ctx, rec.ID)
	require.NoError(t, err)
	assert.Equal(t, rec.ID, got.ID)
	assert.Equal(t, "hello-eval", got.Content)
	assert.Equal(t, model.ModelEvalOK, got.Outcome)
}

// TestModelEvalListPaginationAndFilter 验证分页默认值/上限、按渠道/模型/结果过滤与搜索。
func TestModelEvalListPaginationAndFilter(t *testing.T) {
	ctx := context.Background()
	var ids []int64
	t.Cleanup(func() { cleanupEvals(t, ids...) })

	mk := func(suffix string, chID int, model string, outcome model.ModelEvalOutcome) {
		rec := newEvalRecord(chID, model, outcome, "x")
		rec.ChannelName = "list-chan-" + suffix
		require.NoError(t, ModelEvalCreate(ctx, rec))
		ids = append(ids, rec.ID)
	}
	// 5 条 ok + 1 条 error，分属两个渠道/两个模型名。
	for i := 0; i < 5; i++ {
		mk("a", 950010, "list-model-a", model.ModelEvalOK)
	}
	mk("b", 950011, "list-model-b", model.ModelEvalError)

	// 默认分页：page=1, pageSize=20。
	page, err := ModelEvalList(ctx, ModelEvalFilter{})
	require.NoError(t, err)
	assert.Equal(t, 1, page.Page)
	assert.Equal(t, 20, page.PageSize)
	assert.Equal(t, int64(6), page.Total)

	// pageSize 上限 100。
	big, err := ModelEvalList(ctx, ModelEvalFilter{PageSize: 500})
	require.NoError(t, err)
	assert.Equal(t, 100, big.PageSize)

	// 按渠道过滤。
	byCh, err := ModelEvalList(ctx, ModelEvalFilter{ChannelID: 950010})
	require.NoError(t, err)
	assert.Equal(t, int64(5), byCh.Total)

	// 按模型过滤。
	byModel, err := ModelEvalList(ctx, ModelEvalFilter{ModelName: "list-model-b"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), byModel.Total)

	// 按结果过滤。
	byOutcome, err := ModelEvalList(ctx, ModelEvalFilter{Outcome: "error"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), byOutcome.Total)

	// 搜索渠道名（LIKE，需命中）。
	byQuery, err := ModelEvalList(ctx, ModelEvalFilter{Query: "list-chan-b"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), byQuery.Total)

	// 搜索含 LIKE 通配元字符的模型名，验证转义不会误匹配全部。
	byWildcard, err := ModelEvalList(ctx, ModelEvalFilter{Query: "%"})
	require.NoError(t, err)
	assert.Equal(t, int64(0), byWildcard.Total, "%% 应被转义为字面量，不命中任何记录")
}
