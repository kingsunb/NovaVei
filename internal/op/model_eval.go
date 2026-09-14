package op

import (
	"context"
	"strings"

	"github.com/kingsunb/NovaVeil/internal/db"
	"github.com/kingsunb/NovaVeil/internal/model"
)

type ModelEvalFilter struct {
	ChannelID int
	ModelName string
	Outcome   string
	Query     string
	Page      int
	PageSize  int
}

type ModelEvalPage struct {
	Items    []model.ModelEvalSummary `json:"items"`
	Total    int64                    `json:"total"`
	Page     int                      `json:"page"`
	PageSize int                      `json:"page_size"`
}

func ModelEvalCreate(ctx context.Context, record *model.ModelEval) error {
	if len(record.Content) > model.ModelEvalMaxContentBytes {
		record.Content = truncateUTF8Bytes(record.Content, model.ModelEvalMaxContentBytes)
		record.ContentTruncated = true
	}
	record.Error = truncateUTF8Bytes(redactSensitiveText(record.Error), 4096)
	return db.GetDB().WithContext(ctx).Create(record).Error
}

func ModelEvalGet(ctx context.Context, id int64) (*model.ModelEval, error) {
	var record model.ModelEval
	if err := db.GetDB().WithContext(ctx).First(&record, id).Error; err != nil {
		return nil, err
	}
	return &record, nil
}

func ModelEvalList(ctx context.Context, filter ModelEvalFilter) (*ModelEvalPage, error) {
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.PageSize < 1 {
		filter.PageSize = 20
	}
	filter.PageSize = min(filter.PageSize, 100)
	query := db.GetDB().WithContext(ctx).Model(&model.ModelEval{})
	if filter.ChannelID > 0 {
		query = query.Where("channel_id = ?", filter.ChannelID)
	}
	if filter.ModelName != "" {
		query = query.Where("model_name = ?", filter.ModelName)
	}
	if filter.Outcome != "" {
		query = query.Where("outcome = ?", filter.Outcome)
	}
	if search := strings.TrimSpace(filter.Query); search != "" {
		search = "%" + strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(strings.ToLower(search)) + "%"
		query = query.Where("(LOWER(channel_name) LIKE ? ESCAPE '!' OR LOWER(model_name) LIKE ? ESCAPE '!')", search, search)
	}
	page := &ModelEvalPage{
		Items:    make([]model.ModelEvalSummary, 0),
		Page:     filter.Page,
		PageSize: filter.PageSize,
	}
	if err := query.Count(&page.Total).Error; err != nil {
		return nil, err
	}
	// 不把原始 HTML 随分页列表传输，避免大量动画回复挤占内存与带宽。
	err := query.Omit("prompt", "content").Order("created_at DESC, id DESC").
		Offset((filter.Page - 1) * filter.PageSize).Limit(filter.PageSize).Find(&page.Items).Error
	if err != nil {
		return nil, err
	}
	return page, nil
}
