package model

import "testing"

// TestModelEvalContentOutcome 覆盖评审回复包裹标记的判定逻辑：
// 只有 <<<RESULT>>> 与 <<<END>>> 之间存在非空白内容才算 ok，否则 violation。
// 该函数被 scheduler.executeTask 用来对上游回复自动定级，是评审功能的核心判定。
func TestModelEvalContentOutcome(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    ModelEvalOutcome
	}{
		{"空内容", "", ModelEvalViolation},
		{"无开始标记", "some html without markers", ModelEvalViolation},
		{"仅有开始标记无结束标记", ModelEvalResultStart + "<html></html>", ModelEvalViolation},
		{"标记间有内容", ModelEvalResultStart + "<html><body>x</body></html>" + ModelEvalResultEnd, ModelEvalOK},
		{"标记间仅空白", ModelEvalResultStart + "   \n\t  " + ModelEvalResultEnd, ModelEvalViolation},
		{"标记间空字符串", ModelEvalResultStart + ModelEvalResultEnd, ModelEvalViolation},
		{"标记前后有噪声但中间有效", "noise before" + ModelEvalResultStart + "<svg/>" + ModelEvalResultEnd + "noise after", ModelEvalOK},
		{"开始标记出现两次以第一个为准", ModelEvalResultStart + ModelEvalResultStart + "<x/>" + ModelEvalResultEnd, ModelEvalOK},
		{"结束标记在开始标记之前", ModelEvalResultEnd + ModelEvalResultStart + "<x/>", ModelEvalViolation},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ModelEvalContentOutcome(tc.content)
			if got != tc.want {
				t.Fatalf("ModelEvalContentOutcome(%q) = %q, want %q", tc.content, got, tc.want)
			}
		})
	}
}
