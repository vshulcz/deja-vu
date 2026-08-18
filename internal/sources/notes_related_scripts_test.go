package sources

import "testing"

// Two notes are related when they share words. Chinese, Japanese and Korean
// write no separator between them, so a whole note was one word and no two such
// notes could share the three the bar asks for.
func TestNoteWordSetSeesCJKWords(t *testing.T) {
	a := noteWordSet("调度器移到了单独的进程并且限制了重试次数")
	b := noteWordSet("调度器移到了单独的进程后来又调整了一次")
	shared := 0
	for w := range a {
		if b[w] {
			shared++
		}
	}
	if shared < 3 {
		t.Errorf("two notes about the same thing share %d words, which is under the bar for relating them", shared)
	}
}

// Notes about different things must not become related, or the bar means
// nothing.
func TestNoteWordSetKeepsUnrelatedCJKApart(t *testing.T) {
	// Not a trivially distant pair: both are ordinary sentences carrying the
	// grammatical characters every Chinese sentence carries — 的, 了, 我们 — so
	// the bar has to survive shared function words, not just shared topics.
	a := noteWordSet("我们把调度器移到了单独的进程")
	b := noteWordSet("我们把日志的格式换成了结构化输出")
	shared := 0
	for w := range a {
		if b[w] {
			shared++
		}
	}
	if shared >= 3 {
		t.Errorf("two unrelated notes share %d words", shared)
	}
}
