package model

import "time"

// QueueTaskStatus 评估队列任务的执行状态。
type QueueTaskStatus string

const (
	QueueTaskQueued  QueueTaskStatus = "queued"  // 待执行，按 Position 升序派发。
	QueueTaskRunning QueueTaskStatus = "running" // 执行中，禁止停止/调整。
	QueueTaskDone    QueueTaskStatus = "done"    // 执行完成（成功或失败均落库后定稿）。
	QueueTaskStopped QueueTaskStatus = "stopped" // 用户停止，不再派发。
)

// ModelEvalQueueTask 评估队列任务：把「开始评估」从即时并发改为顺序调度，
// 新增评估排在队尾，不中断执行中的评估。
type ModelEvalQueueTask struct {
	ID             int64           `json:"id" gorm:"primaryKey;autoIncrement"`
	ChannelID      int             `json:"channel_id"`
	ChannelModelID int             `json:"channel_model_id"`
	ChannelName    string          `json:"channel_name" gorm:"type:text"`
	ChannelType    ChannelProvider `json:"channel_type" gorm:"size:64"`
	ModelName      string          `json:"model_name" gorm:"size:512"`
	Status         QueueTaskStatus `json:"status" gorm:"size:16;index:idx_eval_queue_status"`
	Error          string          `json:"error" gorm:"type:text"`
	// EvalID 软引用本次任务写入的评估历史记录，不建外键。
	EvalID      int64     `json:"eval_id"`
	Position    int       `json:"position" gorm:"not null;index:idx_eval_queue_position"`
	CreatedAt   time.Time `json:"created_at" gorm:"index"`
	StartedAt   time.Time `json:"started_at" gorm:"index"`
	CompletedAt time.Time `json:"completed_at"`
}