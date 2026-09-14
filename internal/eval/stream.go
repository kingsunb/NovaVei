package eval

import (
	"context"

	"github.com/charmbracelet/log"
	"github.com/kingsunb/NovaVeil/internal/model"
	"github.com/kingsunb/NovaVeil/internal/op"
)

func (s *Scheduler) Subscribe() ([]model.ModelEvalQueueTask, chan []model.ModelEvalQueueTask) {
	snapshot, err := op.ModelEvalQueueList(context.Background())
	if err != nil {
		log.Warnf("eval queue list: %v", err)
		snapshot = []model.ModelEvalQueueTask{}
	}
	ch := make(chan []model.ModelEvalQueueTask, 1)
	s.subMu.Lock()
	s.subs[ch] = struct{}{}
	s.subMu.Unlock()
	return snapshot, ch
}

func (s *Scheduler) Unsubscribe(ch chan []model.ModelEvalQueueTask) {
	if s == nil || ch == nil {
		return
	}
	s.subMu.Lock()
	defer s.subMu.Unlock()
	if _, ok := s.subs[ch]; ok {
		delete(s.subs, ch)
		close(ch)
	}
}

func (s *Scheduler) Publish() {
	if s == nil {
		return
	}
	snapshot, err := op.ModelEvalQueueList(context.Background())
	if err != nil {
		log.Warnf("eval queue list: %v", err)
		return
	}
	s.subMu.Lock()
	defer s.subMu.Unlock()
	for ch := range s.subs {
		select {
		case ch <- snapshot:
		default:
			delete(s.subs, ch)
			close(ch)
		}
	}
}
