package index

import "testing"

// deja notices when someone asks the same thing again, by folding a question to
// a stem and matching stems. A stem needs five words, and Chinese, Japanese and
// Korean write no separator between them, so a question in those scripts was one
// word however much it asked and never got a stem at all.
func TestQuestionStemAcrossScripts(t *testing.T) {
	for name, q := range map[string]string{
		"english": "why does the scheduler run tasks twice",
		"chinese": "调度器为什么会重复执行任务",
		"korean":  "스케줄러가작업을두번실행하는이유는무엇입니까",
		"russian": "почему планировщик выполняет задачи дважды",
	} {
		if questionStem(q) == "" {
			t.Errorf("%s: no stem, so asking this twice is never noticed", name)
		}
		if _, ok := askedHashOf(q + "？"); !ok {
			t.Errorf("%s: not tracked as an asking at all", name)
		}
	}
}

// The bar still has to keep trivia out: "ok" and "继续" repeat in every session
// and mean nothing.
func TestShortAskingsStillHaveNoStem(t *testing.T) {
	for name, q := range map[string]string{
		"english short": "why not",
		"chinese short": "为什么",
		"russian short": "а почему",
	} {
		if got := questionStem(q); got != "" {
			t.Errorf("%s: got stem %q, want none", name, got)
		}
	}
}

// The same question asked twice folds to the same stem, which is the whole
// point of having one.
func TestSameChineseQuestionFoldsTogether(t *testing.T) {
	a, ok1 := askedHashOf("调度器为什么会重复执行任务？")
	b, ok2 := askedHashOf("调度器为什么会重复执行任务？")
	if !ok1 || !ok2 || a != b {
		t.Errorf("the same question hashed to %d and %d (ok %v, %v)", a, b, ok1, ok2)
	}
}
