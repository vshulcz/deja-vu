package digest

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The prose gate keeps pasted JSON and CLI walls out of digests by counting
// whitespace-separated words. Chinese, Japanese and Korean are written without
// spaces, so a whole paragraph counts as one word and every answer over 80 bytes
// in those scripts was thrown away as a dump.
func TestCJKAnswersAreProse(t *testing.T) {
	for name, line := range map[string]string{
		"chinese":  "我们把调度器移到了单独的进程" + strings.Repeat("在权衡了各种选择之后", 12) + "然后又撤销了。",
		"japanese": "スケジューラを別のプロセスに移動しました" + strings.Repeat("いくつかの選択肢を検討した結果", 8) + "その後元に戻しました。",
		"korean":   "스케줄러를 별도의 프로세스로 옮겼습니다" + strings.Repeat("여러 선택지를 검토한 끝에", 8) + "그 후 되돌렸습니다.",
	} {
		if !looksLikeProse(line) {
			t.Errorf("%s: an answer written in this script reads as a pasted dump", name)
		}
	}
}

// A session written in Chinese must produce the conclusions an English one of
// the same shape does.
func TestConclusionsSurviveInCJK(t *testing.T) {
	cjk := model.Session{Messages: []model.Message{
		{Role: "assistant", Text: "我们把重试次数限制为三次" + strings.Repeat("在权衡了各种选择之后", 12) + "并且保持不变。"},
		{Role: "assistant", Text: "我们把调度器移到了单独的进程" + strings.Repeat("在权衡了各种选择之后", 12) + "然后又撤销了。"},
	}}
	if got := Conclusions(cjk, 4000, 2); len(got) != 2 {
		t.Errorf("got %d conclusions from a Chinese session, want 2: %q", len(got), got)
	}
}

// The gate still has to do its job: a pasted listing is not prose, in any
// script.
func TestPastedDumpsAreStillNotProse(t *testing.T) {
	for name, line := range map[string]string{
		"json":    `{"path":"/tmp/a","size":12,"mode":420,"mtime":1699999999,"hash":"deadbeefdeadbeefdeadbeef","owner":"root"}`,
		"listing": "drwxr-xr-x 5 root root 4096 Jan 1 00:00 /usr/lib/x86_64-linux-gnu/libfoo.so.1.2.3.4.5.6.7.8.9",
		// The case the CJK allowance opens: structured output whose values are
		// Chinese. The characters are there; the line is still a dump.
		"cjk json": `{"路径":"/tmp/数据库/主表","模式":420,"大小":12,"哈希":"deadbeefdeadbeef","拥有者":"root","时间":1699999999}`,
	} {
		// The two gates MessageText applies together: a dump has to be caught
		// by one of them.
		if looksLikeProse(line) && !noiseLine(line) {
			t.Errorf("%s: a pasted dump got through both gates", name)
		}
	}
}
