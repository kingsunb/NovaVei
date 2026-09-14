package op

import (
	"context"
	"strings"

	"github.com/kingsunb/NovaVeil/internal/db"
	"github.com/kingsunb/NovaVeil/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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
	// 历史与累计次数同事务写入；裁剪历史或清空失败记录不回退累计次数。
	return db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(record).Error; err != nil {
			return err
		}
		stats := model.ModelEvalStats{
			ChannelID:  record.ChannelID,
			ModelName:  record.ModelName,
			TotalCount: 1,
		}
		if record.Outcome == model.ModelEvalOK {
			stats.SuccessCount = 1
		}
		return tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "channel_id"}, {Name: "model_name"}},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"total_count":   gorm.Expr("? + ?", clause.Column{Table: clause.CurrentTable, Name: "total_count"}, 1),
				"success_count": gorm.Expr("? + ?", clause.Column{Table: clause.CurrentTable, Name: "success_count"}, stats.SuccessCount),
			}),
		}).Create(&stats).Error
	})
}

func ModelEvalStatsList(ctx context.Context) ([]model.ModelEvalStats, error) {
	stats := make([]model.ModelEvalStats, 0)
	if err := db.GetDB().WithContext(ctx).Order("channel_id ASC, model_name ASC").Find(&stats).Error; err != nil {
		return nil, err
	}
	return stats, nil
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
