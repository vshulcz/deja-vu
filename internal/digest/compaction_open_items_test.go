package digest

import (
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The packet's open-item section took a line opening with gap:, unverified:,
// missing: or blocked:. Measured over 72137 assistant lines on a real store, that
// shape appears once; the same shape with the words people use appears 28 times,
// and every one of the fourteen sampled was genuinely what was left to do.
func TestWhatIsLeftToDoReachesThePacket(t *testing.T) {
	for _, line := range []string{
		"Осталось: сбросить счётчик дропнутых попыток на дашборде.",
		"Что осталось: две дальние формулировки и прогон под Zed.",
		"**Открытый вопрос — OpenClaw плагин на ClawHub (#3047).**",
		"Still open: the dashboard counts the dropped attempts.",
		"Not verified: the exporter under a cold cache.",
		"TODO: reset the counter before the next release.",
		"gap: nobody checked the replica path",
	} {
		got := ExtractCompactionContext(openItemSession(line), ExtractOptions{})
		if len(got.Gaps) == 0 {
			t.Errorf("%q did not reach the packet as an open item", line)
		}
	}
}

// And the shape is what keeps it precise: "осталось" in the middle of a sentence
// is usually arithmetic about something else.
func TestAMidSentenceWordIsNotAnOpenItem(t *testing.T) {
	for _, line := range []string{
		"Из 17 джобов осталось 12, остальные запаузены.",
		"Непроверенных конфигурационных гипотез не осталось.",
		"Всё остальное на доставке остаётся FAILED, получателя нет.",
		"The queue still has work left to do after the drain.",
	} {
		got := ExtractCompactionContext(openItemSession(line), ExtractOptions{})
		if len(got.Gaps) > 0 {
			t.Errorf("%q was read as an open item: %q", line, got.Gaps[0].Text)
		}
	}
}

// One turn usually says both — what was settled and what is left — and the packet
// charged its budget twice for the same sentence.
func TestAnOpenItemIsNotRepeatedInsideTheConclusion(t *testing.T) {
	const settled = "Fixed and the suite is green."
	const left = "Осталось: сбросить счётчик дропнутых попыток на дашборде."
	got := ExtractCompactionContext(openItemSession(settled+"\n\n"+left), ExtractOptions{})
	if len(got.Gaps) != 1 {
		t.Fatalf("the open item did not reach the packet: %+v", got.Gaps)
	}
	for _, c := range got.Conclusions {
		if c.Text != settled && c.Text != "" {
			t.Errorf("the conclusion carries the open item again: %q", c.Text)
		}
	}
}

func openItemSession(assistant string) model.Session {
	now := time.Now().UTC()
	return model.Session{
		Harness: "claude", ID: "s1", Project: "app",
		Messages: []model.Message{
			{Role: "user", Text: "the invoice exporter retries without a pause", Time: now},
			{Role: "assistant", Text: assistant, Time: now.Add(time.Minute)},
		},
	}
}
