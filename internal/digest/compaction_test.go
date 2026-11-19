package digest

import (
	"fmt"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
	"reflect"
	"strings"
	"testing"
	"time"
)

// An issue opened together with its PR closes when the PR does: the PR says
// "Closes #N", GitHub closes the issue on merge, and the session only ever
// writes that the PR was merged. Both lines below stayed on a live packet for
// days after their PRs were merged.
func TestCarryIssueClosesWithItsPR(t *testing.T) {
	cases := []struct {
		name string
		msgs []model.Message
		prev []model.ContextCarry
		want []carryWant
	}{
		{"opened and merged in the same segment",
			carryEv("u", "да", "a", "Открыл issue #4638 и PR #4639, жду CI без Windows и смёржу по зелёному.",
				"a", "PR #4639 смёржен в main (коммит `4af775b57`), все 25 проверок CI прошли.", "u", "что дальше"),
			nil, nil},
		{"merged with ё in the verb",
			carryEv("u", "да", "a", "Opened issue #4603 and PR #4604, merge on green.",
				"a", "Смёржил PR #4604, коммит `e767a7e34`.", "u", "дальше"),
			nil, nil},
		{"carried from the last compaction, merged in this one",
			carryEv("u", "ok", "a", "PR #4604 merged as e767a7e34.", "u", "next"),
			[]model.ContextCarry{{Kind: carryOpen, Text: "Opened issue #4603 and PR #4604, will merge.", Key: "#4603,#4604"}},
			nil},
		{"the PR is not merged yet, so the issue stays",
			carryEv("u", "go", "a", "Открыл issue #4638 и PR #4639, жду ревью."),
			nil,
			[]carryWant{{"open", "Открыл issue #4638 и PR #4639, жду ревью."}}},
		{"two PRs named: which one closes the issue is not said",
			carryEv("u", "go", "a", "Issue #300 is still open, PR #301 and PR #302 are waiting.", "a", "PR #301 merged."),
			nil,
			[]carryWant{{"open", "Issue #300 is still open, PR #301 and PR #302 are waiting."}}},
		{"parenthetical PR issue list closes when PR merges via gh",
			carryEv("u", "go",
				"a", "Issue #5967 is open: doctor reports duplicate Codex wiring.",
				"a", "Открыты PR #5968 (#5967 + #5957) и PR #5969 (для #5966).",
				"t", "$ gh pr merge 5968 --squash",
				"u", "next"),
			nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := carryGot(ExtractCarry(tc.msgs, tc.prev))
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestCarryIssuePRPairs(t *testing.T) {
	for s, want := range map[string]map[int]int{
		"Открыл issue #4638 и PR #4639":                   {4638: 4639},
		"PR #12 (issue #10, issue #11)":                   {10: 12, 11: 12},
		"PR #5968 (#5967 + #5957) и PR #5969 (для #5966)": {5967: 5968, 5957: 5968, 5966: 5969},
		"задача #77, пулл-реквест #78":                    {77: 78},
		"PR #1 and PR #2 for issue #3":                    nil,
		"issue #5 only":                                   nil,
		"See PR #9.":                                      {},
		"APR #12 is a date, not a pull request #":         nil,
	} {
		got := carryIssuePRPairs(carryBNorm(s))
		if len(got) == 0 && len(want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%q: got %v, want %v", s, got, want)
		}
	}
}

// carryEv is a transcript in the shorthand of the reference cases: "u" a user
// turn, "a" assistant text, "t" a command.
func carryEv(pairs ...string) []model.Message {
	role := map[string]string{"u": "user", "a": "assistant", "t": sources.RoleCommand, "s": "user"}
	var out []model.Message
	for i := 0; i < len(pairs); i += 2 {
		out = append(out, model.Message{Role: role[pairs[i]], Text: pairs[i+1]}) //nolint:gosec // callers pass role/text pairs
	}
	return out
}

type carryWant struct{ kind, text string }

func carryGot(items []model.ContextCarry) []carryWant {
	var out []carryWant //nolint:prealloc // the cases compare against nil
	for _, c := range items {
		out = append(out, carryWant{c.Kind, c.Text})
	}
	return out
}

// The fifteen reference cases the Python extractor was tuned against, with the
// same structure and expected lines.
func TestExtractCarryReferenceCases(t *testing.T) {
	cases := []struct {
		name string
		msgs []model.Message
		prev []model.ContextCarry
		want []carryWant
	}{
		{"ask after the last user turn",
			carryEv("u", "почини флаки тест", "a", "Починил. Скажи, если сводить в PR сейчас."), nil,
			[]carryWant{{"awaiting", "Скажи, если сводить в PR сейчас."}}},
		{"ask answered by a later user turn is dropped",
			carryEv("a", "Мержить? Решать тебе.", "u", "да"), nil, nil},
		{"self-question and quoted ask are not asks",
			carryEv("u", "go", "a", "Проверяю: гонка в flush или в индексе?\nШапку перепишу на «скажи, что удалить»."), nil, nil},
		{"english your call",
			carryEv("u", "go", "a", "Splitting the leg renames a required check, so it is your call."), nil,
			[]carryWant{{"awaiting", "Splitting the leg renames a required check, so it is your call."}}},
		{"pending word binds to its own #N, closing word to another",
			carryEv("u", "go", "a", "В main. #1455 закрыт автоматически, #1456 остаётся с недоделанным пунктом."), nil,
			[]carryWant{{"open", "#1455 закрыт автоматически, #1456 остаётся с недоделанным пунктом."}}},
		{"header list opens every #N in the run",
			carryEv("u", "go", "a", "Осталось нетронутым: три старых PR (#553 на +48k строк, #481, #453)."), nil,
			[]carryWant{{"open", "Осталось нетронутым: три старых PR (#553 на +48k строк, #481, #453)."}}},
		{"a later merge command drops it",
			carryEv("u", "go", "a", "PR #2261 ждёт CI.", "t", "gh pr merge 2261 --squash", "a", "Дальше."), nil, nil},
		{"a later #N merged in text drops it",
			carryEv("u", "go", "a", "Жду ревью #2800.", "a", "#2800 смержен, 12/12 зелёных."), nil, nil},
		{"references are not open items",
			carryEv("u", "go", "a", "#1090 вычистил отсюда escape-байты, перелом остался.\nЧетыре обработчика из #1306 ждут перестроение.\n`--json` остаётся массивом, как в #2299."), nil, nil},
		{"status enum list and hyphenated words do not count",
			carryEv("u", "go", "a", "A–D (bug-issues #591–#1137): сверяют, FIXED/OPEN/NEEDS-WINDOWS.\nКак придёт: ~9 open-bug, пограничные + #1119."), nil, nil},
		{"verdict with a subject; bare heading and counts rejected",
			carryEv("u", "go", "a", "**H487 отвергнута**, всё откачено.\nЧто не подтвердилось:\n280 DONE, 10 REJECTED за те же 2 часа.\n## Cache hypothesis — REJECTED"), nil,
			[]carryWant{{"verdict", "**H487 отвергнута**, всё откачено."}, {"verdict", "Cache hypothesis — REJECTED"}}},
		{"claim noun needs a topic",
			carryEv("u", "go", "a", "Гипотеза про page cache не подтвердилась: working set ≈ RSS.\nТы прав — гипотеза подтвердилась."), nil,
			[]carryWant{{"verdict", "Гипотеза про page cache не подтвердилась: working set ≈ RSS."}}},
		{"deferred check with a real marker; immediate and conditional rejected",
			carryEv("u", "go", "a", "Посмотрю завтра на чистых сутках.\nСначала проверю логи, потом через час посмотрю метрики.\nЕсли через месяц понадобится посмотреть латентность, её не будет.\nPrometheus подхватывает правило минутой позже — проверю по API."), nil,
			[]carryWant{{"recheck", "Посмотрю завтра на чистых сутках."}}},
		{"open stays until closed, a short recheck does not carry, an ask drops on a user turn",
			carryEv("u", "ок", "a", "Продолжаю."),
			[]model.ContextCarry{
				{Kind: "awaiting", Text: "Скажи, если сводить в PR сейчас."},
				{Kind: "open", Key: "#420", Text: "API | пул воркеров | PR #420 ждёт мейнтейнера"},
				{Kind: "recheck", Text: "Вернусь с результатом через час."},
				{Kind: "verdict", Text: "Гипотеза «утечка дескрипторов в пуле» опровергнута профилированием."},
			},
			[]carryWant{{"open", "API | пул воркеров | PR #420 ждёт мейнтейнера"}, {"verdict", "Гипотеза «утечка дескрипторов в пуле» опровергнута профилированием."}}},
		{"the user says merged with the number",
			carryEv("u", "вмержил 420", "a", "Ок."),
			[]model.ContextCarry{{Kind: "open", Key: "#420", Text: "PR #420 ждёт твоего мержа"}}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := carryGot(ExtractCarry(tc.msgs, tc.prev)); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("\nwant %q\ngot  %q", tc.want, got)
			}
		})
	}
}

// Each of these fails when its rule is removed: the extractor would either keep
// the line or lose it.
func TestExtractCarryRules(t *testing.T) {
	open := func(key, text string, age int) model.ContextCarry {
		return model.ContextCarry{Kind: "open", Key: key, Text: text, Age: age}
	}
	cases := []struct {
		name string
		msgs []model.Message
		prev []model.ContextCarry
		want []carryWant
	}{
		{"a carried ask stays while the user is silent",
			carryEv("a", "Продолжаю разбор логов."),
			[]model.ContextCarry{{Kind: "awaiting", Text: "Скажи, если сводить в PR сейчас."}},
			[]carryWant{{"awaiting", "Скажи, если сводить в PR сейчас."}}},
		{"a carried ask drops after a user turn",
			carryEv("u", "делай", "a", "Продолжаю разбор логов."),
			[]model.ContextCarry{{Kind: "awaiting", Text: "Скажи, если сводить в PR сейчас."}}, nil},
		{"a carried open item closes on a later merge command",
			carryEv("u", "go", "t", "$ gh pr merge 2261 --squash  → exit 0"),
			[]model.ContextCarry{open("#2261", "PR #2261 ждёт ревью мейнтейнера", 0)}, nil},
		{"a merge loop closes every number on the line",
			carryEv("u", "go", "t", "for n in 2261 2263; do gh pr merge $n; done"),
			[]model.ContextCarry{open("#2261", "PR #2261 ждёт ревью мейнтейнера", 0)}, nil},
		{"a carried open item keeps the numbers still open",
			carryEv("u", "go", "t", "gh pr merge 553 --squash"),
			[]model.ContextCarry{open("#553,#481", "Осталось: #553 и #481 от внешних авторов", 0)},
			[]carryWant{{"open", "Осталось: #553 и #481 от внешних авторов"}}},
		{"a carried open item ages out after two compactions",
			carryEv("u", "go"),
			[]model.ContextCarry{open("#420", "PR #420 ждёт мейнтейнера", 2)}, nil},
		{"waiting on CI is not carried",
			carryEv("u", "go"),
			[]model.ContextCarry{open("#420", "PR #420 ждёт зелёного CI", 0)}, nil},
		{"a user merged without a number closes what waited on them",
			carryEv("u", "смержил"),
			[]model.ContextCarry{open("#420", "PR #420 ждёт твоего мержа", 0)}, nil},
		{"a fresh ask on the user's merge is dropped by a later merged",
			carryEv("u", "go", "a", "PR #3100 ждёт твоего мержа.", "u", "merged"), nil, nil},
		{"closes #N follows the PR that carried it",
			carryEv("u", "go", "a", "Issue #812 ждёт фикса.", "a", "PR #815 fixes #812.", "t", "gh pr merge 815"), nil, nil},
		{"a carried recheck goes once the segment names its #N",
			carryEv("u", "go", "a", "Смотрю #4411 ещё раз."),
			[]model.ContextCarry{{Kind: "recheck", Text: "Проверю #4411 завтра на свежем прогоне."}}, nil},
		{"a carried recheck goes once the segment repeats its words",
			carryEv("u", "go", "a", "Латентность экспортера после рестарта ровная."),
			[]model.ContextCarry{{Kind: "recheck", Text: "Посмотрю латентность экспортера после рестарта завтра."}}, nil},
		{"a carried recheck stays otherwise",
			carryEv("u", "go", "a", "Продолжаю."),
			[]model.ContextCarry{{Kind: "recheck", Text: "Посмотрю латентность экспортера после рестарта завтра."}},
			[]carryWant{{"recheck", "Посмотрю латентность экспортера после рестарта завтра."}}},
		{"a no-break space still separates words",
			carryEv("u", "go", "a", "Перепроверю через 2 дня на чистой выборке."), nil,
			[]carryWant{{"recheck", "Перепроверю через 2 дня на чистой выборке."}}},
		{"a fresh line naming the same #N replaces the carried one",
			carryEv("u", "go", "a", "PR #420 всё ещё ждёт мейнтейнера."),
			[]model.ContextCarry{open("#420", "PR #420 ждёт мейнтейнера", 0)},
			[]carryWant{{"open", "PR #420 всё ещё ждёт мейнтейнера."}}},
		{"only the segment since the last compaction counts",
			carryEv("a", "PR #9001 ждёт ревью.", "s", "This session is being continued from a previous conversation that ran out of context.", "u", "go", "a", "Продолжаю."),
			nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := carryGot(ExtractCarry(tc.msgs, tc.prev)); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("\nwant %q\ngot  %q", tc.want, got)
			}
		})
	}
}

func TestExtractCarryAgesAndKeys(t *testing.T) {
	got := ExtractCarry(carryEv("u", "go", "a", "PR #2261 и #2262 ждут ревью мейнтейнера."),
		[]model.ContextCarry{{Kind: "verdict", Text: "H12 отвергнута замером.", Age: 3}})
	want := []model.ContextCarry{
		{Kind: "open", Text: "PR #2261 и #2262 ждут ревью мейнтейнера.", Key: "#2261,#2262"},
		{Kind: "verdict", Text: "H12 отвергнута замером.", Key: "h12 отвергнута замером.", Age: 4},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("\nwant %+v\ngot  %+v", want, got)
	}
}

func TestExtractCarryCaps(t *testing.T) {
	text := make([]string, 0, 23)
	for i := range 5 {
		text = append(text, fmt.Sprintf("Вариант %d готов, скажи, если брать его.", i))
	}
	for i := range 6 {
		text = append(text, fmt.Sprintf("Гипотеза H%d отвергнута замером.", i))
	}
	for i := range 4 {
		text = append(text, fmt.Sprintf("Посмотрю метрику %d завтра на свежих данных.", i))
	}
	for i := range 8 {
		text = append(text, fmt.Sprintf("PR #%d ждёт ревью мейнтейнера.", 100+i))
	}
	// One message per line, so "newest" is visible: lines of one message share
	// a position, as they do in the reference.
	msgs := carryEv("u", "go")
	for _, line := range text {
		msgs = append(msgs, model.Message{Role: "assistant", Text: line})
	}
	got := ExtractCarry(msgs, nil)
	count := map[string]int{}
	for _, c := range got {
		count[c.Kind]++
	}
	if len(got) != 12 || count["awaiting"] != 3 || count["open"] != 8 || count["recheck"] != 1 || count["verdict"] != 0 {
		t.Fatalf("caps not honoured: %v %q", count, carryGot(got))
	}
	// Newest first inside a class.
	if got[0].Text != "Вариант 4 готов, скажи, если брать его." || got[3].Key != "#107" || got[10].Key != "#100" {
		t.Fatalf("order: %q", carryGot(got))
	}
	got = ExtractCarry(append(carryEv("u", "go"), msgs[6:16]...), nil)
	count = map[string]int{}
	for _, c := range got {
		count[c.Kind]++
	}
	if count["verdict"] != 4 || count["recheck"] != 3 || got[len(got)-1].Text != "Гипотеза H2 отвергнута замером." {
		t.Fatalf("verdict/recheck caps: %v %q", count, carryGot(got))
	}
}

func TestRenderCompactionContextPutsCarryBeforeObjective(t *testing.T) {
	c := model.CompactionContext{
		Objective: model.ContextFact{Text: "Fix retry cancellation."},
		Carry: []model.ContextCarry{
			{Kind: "awaiting", Text: "Splitting the leg is your call."},
			{Kind: "open", Key: "#420", Text: strings.Repeat("длинное описание ", 20) + "PR #420 ждёт мейнтейнера"},
			{Kind: "recheck", Text: "Посмотрю завтра на чистых сутках."},
			{Kind: "verdict", Text: "H487 отвергнута."},
		},
	}
	out := RenderCompactionContext(c, 0)
	carry, objective := strings.Index(out, carryHeader), strings.Index(out, "\nObjective\n")
	if carry < 0 || objective < 0 || carry > objective {
		t.Fatalf("carry section must precede the objective:\n%s", out)
	}
	for _, want := range []string{"- [awaiting you] Splitting", "- [open] #420 длинное", "- [recheck] Посмотрю", "- [verdict] H487"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	// A line past the budget is left out and said to be.
	tight := RenderCompactionContext(c, 400)
	if !strings.Contains(tight, "- [awaiting you]") || strings.Contains(tight, "- [open]") || !strings.Contains(tight, "Omitted") {
		t.Fatalf("tight budget:\n%s", tight)
	}
	if renderCarry(nil) != "" || !strings.HasPrefix(renderCarry(c.Carry), carryHeader+"\n- [awaiting you]") {
		t.Fatalf("renderCarry: %q", renderCarry(c.Carry))
	}
}

func renderCarry(items []model.ContextCarry) string {
	var b strings.Builder
	for _, c := range items {
		b.WriteString(carryLine(model.ContextCarry{Kind: c.Kind, Text: redactContextText(c.Text), Key: c.Key}))
	}
	if b.Len() == 0 {
		return ""
	}
	return carryHeader + "\n" + b.String()
}

// Printed first, a full list would push the objective and the conclusions out
// of a recovery-sized packet; it keeps to its share and says what it left out.
func TestCarryKeepsToItsShareOfThePacket(t *testing.T) {
	c := model.CompactionContext{
		Objective:   model.ContextFact{Text: "Fix retry cancellation."},
		Conclusions: []model.ContextFact{{Text: "Root cause: the retry loop ignored ctx.Done."}},
	}
	for i := range 12 {
		c.Carry = append(c.Carry, model.ContextCarry{Kind: "open", Key: fmt.Sprintf("#%d", 7300+i),
			Text: strings.Repeat("длинное описание ", 10) + fmt.Sprintf("PR #%d ждёт мейнтейнера", 7300+i)})
	}
	out := RenderCompactionContext(c, 4096)
	if !strings.Contains(out, "Fix retry cancellation.") || !strings.Contains(out, "Root cause") {
		t.Fatalf("the list crowded out the objective or the conclusions:\n%s", out)
	}
	start, end := strings.Index(out, carryHeader), strings.Index(out, "\nObjective\n")
	if start < 0 || end-start > 4096*carrySharePercent/100 {
		t.Fatalf("the list took %d bytes of 4096:\n%s", end-start, out)
	}
	if !strings.Contains(out, "Omitted") {
		t.Fatalf("a cut list has to say so:\n%s", out)
	}
}

func TestBoundCompactionContextDropsCarryLast(t *testing.T) {
	c := model.CompactionContext{Objective: model.ContextFact{Text: "objective"}}
	for range 12 {
		c.Carry = append(c.Carry, model.ContextCarry{Kind: "open", Key: "#1", Text: strings.Repeat("x", carryTextBytes-8)})
	}
	for range 8 {
		c.Conclusions = append(c.Conclusions, model.ContextFact{Text: strings.Repeat("c", contextFactBytes)})
		c.Tests = append(c.Tests, model.ContextTest{Command: strings.Repeat("t", contextFactBytes)})
		c.Gaps = append(c.Gaps, model.ContextOpenItem{Text: strings.Repeat("g", contextFactBytes)})
	}
	got := RedactCompactionContext(c)
	if len(got.Carry) != 12 || len(got.Conclusions) == 8 {
		t.Fatalf("carry was dropped before the other sections: carry=%d conclusions=%d tests=%d", len(got.Carry), len(got.Conclusions), len(got.Tests))
	}
}

// One unit each, so removing a single rule flips a single row.
func TestCarryUnitRules(t *testing.T) {
	kinds := func(text string) string {
		carried := ExtractCarry(carryEv("u", "go", "a", text), nil)
		out := make([]string, 0, len(carried))
		for _, c := range carried {
			out = append(out, c.Kind)
		}
		return strings.Join(out, ",")
	}
	cases := []struct{ text, want string }{
		// awaiting
		{"Гипотеза: если хочешь стабильности, кэш надо греть заранее.", ""},
		{"Могу разнести это на два PR, если хочешь.", "awaiting"},
		// open: references
		{"Добавил в PR #3300 ретраи, остаётся проверка.", ""},
		{"Хвост остаётся открытым (#840 — там объяснение).", ""},
		{"Флаки остаётся (гонка в flush, #2019).", ""},
		// open: binding
		{"Фикс для #2262 написан, но тесты ждут.", ""},
		{"Фикс для #2262 написан; тесты ждут.", ""},
		{"Для #3400 статус в таблице выставлен как pending без причины.", ""},
		{"#3400 pending, разбираю логи дальше.", "open"},
		{"PR #3500 по логам выглядит чисто, описание сходится с кодом полностью, смержу.", "open"},
		{"Остаётся как есть: #3600 и #3601 не трогаем.", ""},
		// verdict
		{"Если гипотеза H5 отвергнута, откатываем.", ""},
		{"Статус «H5 отвергнута» пишем в лог.", ""},
		{"Кэш прогрет (H7 отвергнута раньше) и работает.", ""},
		{"Режим: auto-REJECTED в логах.", ""},
		{"Вчера H5 была отвергнута, сегодня снова.", ""},
		{"Итог — REJECTED, остальные DONE.", ""},
		{"Итог — REJECTED (12 штук).", ""},
		{"Гипотеза H5 отвергнута?", ""},
		{"Две гипотезы не подтвердились: ни кэш, ни пул соединений.", ""},
		{"Старая идея отвергнута: трасса показывает один поток.", "verdict"},
		{"Our cache theory rejected by the numbers.", ""},
		// recheck
		{"Посмотрю ещё раз, " + strings.Repeat("очень ", 16) + "подробно, завтра.", ""},
		// quoted text is cited, not said
		{"PR #4023 ждёт решения мейнтейнера.", "open"},
		{"Агент обещает: «жду решения по #4023», и это не его PR.", ""},
		{"Ответ “жду ревью #4200” пишет бот.", ""},
		{"Вернусь к этому позже, после обеда.", "recheck"},
		{"Фраза «Вернусь к этому позже» теряется в 1 случае из 18.", ""},
		{`Шаблон "проверю завтра" ломает разбор.`, ""},
		{`Проверю завтра на 12" экране.`, "recheck"}, // an unpaired straight quote quotes nothing
	}
	for _, tc := range cases {
		if got := kinds(tc.text); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.text, got, tc.want)
		}
	}
}

func TestExtractCarryClosesAndDedupes(t *testing.T) {
	cases := []struct {
		name string
		msgs []model.Message
		prev []model.ContextCarry
		want []carryWant
	}{
		{"a heading that closes numbers far down the line",
			carryEv("u", "go", "a", "PR #3700 ждёт ревью.", "a", "Закрыты руками: старый flaky тест в пакете index и затем #3700."), nil, nil},
		{"a closing word inside a quote closes nothing",
			carryEv("u", "go", "a", "PR #4100 ждёт ревью.", "a", "Шаблон пишет «#4100 смержен» заранее, до мержа."), nil,
			[]carryWant{{"open", "PR #4100 ждёт ревью."}}},
		{"the newest line per number wins",
			carryEv("u", "go", "a", "PR #3800 ждёт ревью.", "a", "PR #3800 всё ещё ждёт ревью мейнтейнера."), nil,
			[]carryWant{{"open", "PR #3800 всё ещё ждёт ревью мейнтейнера."}}},
		{"a carried recheck ages out",
			carryEv("u", "go", "a", "Продолжаю."),
			[]model.ContextCarry{{Kind: "recheck", Text: "Посмотрю латентность экспортера после рестарта завтра.", Age: 2}}, nil},
		{"a recheck waiting on CI is not carried",
			carryEv("u", "go", "a", "Продолжаю."),
			[]model.ContextCarry{{Kind: "recheck", Text: "Посмотрю завтра, когда CI доедет."}}, nil},
		{"an unknown kind is dropped",
			carryEv("u", "go"), []model.ContextCarry{{Kind: "note", Text: "что-то своё"}}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := carryGot(ExtractCarry(tc.msgs, tc.prev)); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("\nwant %q\ngot  %q", tc.want, got)
			}
		})
	}
}

func TestExtractCompactionContextUsesOnlyRecordedEvidence(t *testing.T) {
	at := time.Date(2026, 9, 10, 14, 0, 0, 0, time.UTC)
	s := model.Session{
		ID: "session-123456789", Harness: "codex",
		Messages: []model.Message{
			{Role: "user", Text: "Implement parser recovery and keep validation honest.", Time: at},
			{Role: "assistant", Text: "Decision: keep malformed input fail-closed and report its source.", Time: at.Add(time.Minute)},
			{Role: sources.RoleCommand, Text: "$ go test ./... → exit 0", Time: at.Add(2 * time.Minute)},
			{Role: sources.RoleCommand, Text: "$ go vet ./... → exit 2", Time: at.Add(3 * time.Minute)},
			{Role: sources.RoleCommand, Text: "$ npm test", Time: at.Add(4 * time.Minute)},
			{Role: sources.RoleToolOutput, Text: "Conflict: tool output is not an agent claim.", Time: at.Add(5 * time.Minute)},
			{Role: "assistant", Text: "Gap: the integration fixture is still unverified.\nConflict: docs say two retries but code says three.", Time: at.Add(6 * time.Minute)},
			{Role: "user", Text: "continue", Time: at.Add(7 * time.Minute)},
		},
	}

	c := ExtractCompactionContext(s, ExtractOptions{})
	if !strings.Contains(c.Objective.Text, "Implement parser recovery") {
		t.Fatalf("objective = %#v", c.Objective)
	}
	if len(c.Conclusions) != 1 || !strings.Contains(c.Conclusions[0].Text, "Decision:") {
		t.Fatalf("conclusions = %#v", c.Conclusions)
	}
	if len(c.Tests) != 3 {
		t.Fatalf("tests = %#v", c.Tests)
	}
	if got := []string{c.Tests[0].Outcome, c.Tests[1].Outcome, c.Tests[2].Outcome}; strings.Join(got, ",") != "passed,failed,recorded" {
		t.Fatalf("outcomes = %v", got)
	}
	if len(c.Gaps) != 1 || !strings.HasPrefix(c.Gaps[0].Text, "Gap:") {
		t.Fatalf("gaps = %#v", c.Gaps)
	}
	if len(c.Conflicts) != 1 || !strings.HasPrefix(c.Conflicts[0].Text, "Conflict:") {
		t.Fatalf("conflicts = %#v", c.Conflicts)
	}
	if c.Conflicts[0].Provenance.Role != "assistant" || c.Conflicts[0].Provenance.SessionID != s.ID {
		t.Fatalf("conflict provenance = %#v", c.Conflicts[0].Provenance)
	}
	for _, item := range c.Conflicts {
		if strings.Contains(item.Text, "tool output") {
			t.Fatalf("tool output became a conflict: %#v", c.Conflicts)
		}
	}
}

func TestExtractCompactionContextFallsBackToHarnessCompactionIntent(t *testing.T) {
	summary := "Summary:\n1. Primary Request and Intent:\n  Repair the decoder and add a fixture.\n2. Key Technical Concepts:\n  - irrelevant\n"
	s := model.Session{ID: "s", Harness: "claude", Messages: []model.Message{
		{Role: "user", Text: summary},
		{Role: "user", Text: "go on"},
	}}
	c := ExtractCompactionContext(s, ExtractOptions{})
	if !strings.Contains(c.Objective.Text, "Repair the decoder") {
		t.Fatalf("summary intent was not retained: %#v", c.Objective)
	}
	if strings.Contains(c.Objective.Text, "Key Technical Concepts") {
		t.Fatalf("summary was not bounded to intent: %#v", c.Objective)
	}
}

func TestExtractCompactionContextFallsBackToEmphasizedHarnessIntent(t *testing.T) {
	summary := "Summary:\n1. **Primary Request and Intent:**\n  Repair the decoder and keep the fixture.\n2. Key Technical Concepts:\n  - irrelevant\n"
	s := model.Session{ID: "s", Harness: "claude", Messages: []model.Message{
		{Role: "user", Text: summary},
		{Role: "user", Text: "continue"},
	}}
	c := ExtractCompactionContext(s, ExtractOptions{})
	if !strings.Contains(c.Objective.Text, "Repair the decoder") {
		t.Fatalf("emphasized summary intent was lost: %#v", c.Objective)
	}
	if strings.Contains(c.Objective.Text, "Key Technical Concepts") {
		t.Fatalf("emphasized summary was not bounded to intent: %#v", c.Objective)
	}
}

func TestCompactionIntentStaysAlignedWithSummaryRecognition(t *testing.T) {
	for _, tc := range []struct {
		name, summary, want string
	}{
		{
			name:    "inline plain heading",
			summary: "Summary: 1. Primary Request and Intent: Repair the inline decoder.\n2. Key Technical Concepts:\n  ignored\n",
			want:    "Repair the inline decoder.",
		},
		{
			name:    "multiline heading",
			summary: "Summary:\n1. Primary Request and Intent:\n  Repair the multiline decoder.\n2. Key Technical Concepts:\n  ignored\n",
			want:    "Repair the multiline decoder.",
		},
		{
			name:    "underscored heading",
			summary: "Summary:\n1. __Primary Request and Intent:__\n  Repair the underscored decoder.\n2. Key Technical Concepts:\n  ignored\n",
			want:    "Repair the underscored decoder.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !IsCompactionSummary(tc.summary) {
				t.Fatalf("summary was not recognised: %q", tc.summary)
			}
			got := compactionIntent(tc.summary)
			if !strings.Contains(got, tc.want) || strings.Contains(got, "Key Technical Concepts") || strings.Contains(got, "__") || strings.Contains(got, "**") {
				t.Fatalf("intent = %q, want %q only", got, tc.want)
			}
		})
	}
}

func TestCompactionContextRedactsAtExtractionAndStorageBoundary(t *testing.T) {
	secret := "abcdefghijklmnoPQRSTUVWXYZ123456"
	s := model.Session{ID: "s", Harness: "codex", Messages: []model.Message{
		{Role: "user", Text: "Fix the leak."},
		{Role: "assistant", Text: "Gap: token: " + secret + " has not been rotated."},
	}}
	c := ExtractCompactionContext(s, ExtractOptions{})
	if strings.Contains(c.Gaps[0].Text, secret) {
		t.Fatalf("extractor stored secret: %#v", c.Gaps)
	}
	c.Gaps[0].Text = "Gap: token: " + secret
	if got := RedactCompactionContext(c).Gaps[0].Text; strings.Contains(got, secret) {
		t.Fatalf("storage redaction retained secret: %q", got)
	}
	t.Setenv("DEJA_NO_REDACT", "1")
	if got := RedactCompactionContext(c).Gaps[0].Text; !strings.Contains(got, secret) {
		t.Fatalf("documented redaction opt-out was ignored: %q", got)
	}
}

func TestRenderCompactionContextBoundsAndNamesFreshnessFailure(t *testing.T) {
	c := model.CompactionContext{
		Objective: model.ContextFact{Text: "Repair the parser."},
		Freshness: model.RepositoryFreshness{Error: "git status timed out"},
		Gaps:      []model.ContextOpenItem{{Text: "Gap: run integration coverage."}},
		Truncated: true,
	}
	got := RenderCompactionContext(c, 512)
	if len(got) > 512 {
		t.Fatalf("rendered %d bytes, want <= 512: %q", len(got), got)
	}
	if !strings.Contains(got, "Repository freshness unavailable") {
		t.Fatalf("freshness failure was hidden: %q", got)
	}
	if !strings.Contains(got, "Omitted transcript records") {
		t.Fatalf("truncation was hidden: %q", got)
	}
}

func TestExtractCompactionContextBoundsTranscriptWindow(t *testing.T) {
	messages := []model.Message{
		{Role: "user", Text: "old objective"},
		{Role: "assistant", Text: "Decision: old conclusion."},
		{Role: "assistant", Text: "Decision: recent conclusion."},
	}
	c := ExtractCompactionContext(model.Session{ID: "s", Harness: "codex", Messages: messages}, ExtractOptions{MaxRecords: 1})
	if !c.Truncated {
		t.Fatal("record-window cutoff was not marked")
	}
	if len(c.Conclusions) != 1 || !strings.Contains(c.Conclusions[0].Text, "recent") {
		t.Fatalf("window did not prefer newest record: %#v", c.Conclusions)
	}
}

func TestExtractCompactionContextSkipsLatestOversizedToolOutput(t *testing.T) {
	s := model.Session{ID: "s", Harness: "codex", Messages: []model.Message{
		{Role: "user", Text: "Repair the parser."},
		{Role: "assistant", Text: "Decision: preserve malformed input evidence."},
		{Role: sources.RoleToolOutput, Text: strings.Repeat("x", defaultContextText+1)},
	}}
	c := ExtractCompactionContext(s, ExtractOptions{})
	if !c.Truncated {
		t.Fatal("skipped oversized tool output was not marked")
	}
	if !strings.Contains(c.Objective.Text, "Repair the parser") {
		t.Fatalf("oversized output erased objective: %#v", c.Objective)
	}
	if len(c.Conclusions) != 1 || !strings.Contains(c.Conclusions[0].Text, "preserve malformed") {
		t.Fatalf("oversized output erased conclusion: %#v", c.Conclusions)
	}
}

func TestExtractCompactionContextFindsObjectiveBehindToolStream(t *testing.T) {
	messages := make([]model.Message, 0, 82)
	messages = append(messages, model.Message{Role: "user", Text: "Repair the parser and add the fixture."})
	for range 80 {
		messages = append(messages, model.Message{Role: sources.RoleToolOutput, Text: "ordinary tool output"})
	}
	messages = append(messages, model.Message{Role: "user", Text: "continue"})
	c := ExtractCompactionContext(model.Session{ID: "s", Harness: "codex", Messages: messages}, ExtractOptions{})
	if !strings.Contains(c.Objective.Text, "Repair the parser") {
		t.Fatalf("tool stream erased objective: %#v", c.Objective)
	}
}

func TestExtractCompactionContextRecognizesMarkdownLabels(t *testing.T) {
	s := model.Session{ID: "s", Harness: "codex", Messages: []model.Message{
		{Role: "assistant", Text: "**Gap:** validation has not run.\n- **Conflict:** docs and code disagree."},
	}}
	c := ExtractCompactionContext(s, ExtractOptions{})
	if len(c.Gaps) != 1 || len(c.Conflicts) != 1 {
		t.Fatalf("markdown labels lost: gaps=%#v conflicts=%#v", c.Gaps, c.Conflicts)
	}
}

func TestCompactionContextBoundsAndRedactsOversizedProvenance(t *testing.T) {
	secret := "abcdefghijklmnopqrstuvwxyz123456"
	longID := strings.Repeat("token: "+secret+" | ", 2000)
	s := model.Session{ID: longID, Harness: strings.Repeat("h", 500), Messages: []model.Message{
		{Role: "user", Text: "Repair the parser."},
		{Role: "assistant", Text: "Decision: preserve the evidence."},
		{Role: "assistant", Text: "Gap: integration has not run."},
		{Role: "assistant", Text: "Conflict: docs and code disagree."},
	}}
	c := ExtractCompactionContext(s, ExtractOptions{})
	if got := serializedContextBytes(c); got > maxContextBytes {
		t.Fatalf("context is %d bytes, want <= %d", got, maxContextBytes)
	}
	for _, ref := range c.Sources {
		if len(ref.SessionID) > 256 || strings.Contains(ref.SessionID, secret) {
			t.Fatalf("unbounded or unredacted source: %#v", ref)
		}
	}
}

func TestRenderCompactionContextTreatsPartialFingerprintAsUnverified(t *testing.T) {
	c := model.CompactionContext{Freshness: model.RepositoryFreshness{
		Head: "abc", WorktreeState: "partial:deadbeef",
	}}
	got := RenderCompactionContext(c, 1024)
	if !strings.Contains(got, "Repository fingerprint is partial") {
		t.Fatalf("partial fingerprint was not called out: %q", got)
	}
	if strings.Contains(got, "Repository freshness: head=abc") {
		t.Fatalf("partial fingerprint was rendered as complete: %q", got)
	}
}

func TestRedactCompactionContextBoundsDirectStorageInput(t *testing.T) {
	long := strings.Repeat("untrusted transcript text ", 4000)
	ref := model.ContextRef{SessionID: strings.Repeat("session-", 1000), Harness: "codex", Role: "assistant"}
	c := model.CompactionContext{
		Objective:   model.ContextFact{Text: long, Provenance: ref},
		Gaps:        []model.ContextOpenItem{{Text: long, Provenance: ref}, {Text: long, Provenance: ref}},
		Conflicts:   []model.ContextOpenItem{{Text: long, Provenance: ref}, {Text: long, Provenance: ref}},
		Tests:       []model.ContextTest{{Command: long, Provenance: ref}},
		Conclusions: []model.ContextFact{{Text: long, Provenance: ref}},
	}
	got := RedactCompactionContext(c)
	if got.Objective.Text == long || len(got.Objective.Text) > contextObjectiveBytes {
		t.Fatalf("objective was not bounded: %d bytes", len(got.Objective.Text))
	}
	if size := serializedContextBytes(got); size > maxContextBytes {
		t.Fatalf("direct storage input remained %d bytes, want <= %d", size, maxContextBytes)
	}
	if len(got.Sources) == 0 || len(got.Sources[0].SessionID) > 256 {
		t.Fatalf("provenance was not bounded: %#v", got.Sources)
	}
}

func TestRedactCompactionContextDropsLowPriorityRecordsToFitManifest(t *testing.T) {
	long := strings.Repeat("x", 2000)
	c := model.CompactionContext{Objective: model.ContextFact{Text: "Keep the objective.", Provenance: model.ContextRef{SessionID: "objective", Harness: "codex", Role: "user"}}}
	for i := range 16 {
		ref := model.ContextRef{SessionID: strings.Repeat(string(rune('a'+i)), 1000), Harness: strings.Repeat("h", 100), Role: "assistant"}
		c.Conclusions = append(c.Conclusions, model.ContextFact{Text: long, Provenance: ref})
		c.Tests = append(c.Tests, model.ContextTest{Command: long, Outcome: "recorded", Provenance: ref})
		c.Gaps = append(c.Gaps, model.ContextOpenItem{Text: long, Provenance: ref})
		c.Conflicts = append(c.Conflicts, model.ContextOpenItem{Text: long, Provenance: ref})
	}
	got := RedactCompactionContext(c)
	if !got.Truncated || serializedContextBytes(got) > maxContextBytes {
		t.Fatalf("oversized manifest state was accepted: truncated=%t bytes=%d", got.Truncated, serializedContextBytes(got))
	}
	if len(got.Conclusions) != 0 || len(got.Tests) != 0 {
		t.Fatalf("lower-priority records survived before explicit open items: conclusions=%d tests=%d", len(got.Conclusions), len(got.Tests))
	}
	if got.Objective.Text != "Keep the objective." || len(got.Gaps) == 0 || len(got.Conflicts) == 0 {
		t.Fatalf("required resume state was not retained: %#v", got)
	}
}

func TestRenderCompactionContextRendersEveryEvidenceClassWithProvenance(t *testing.T) {
	ref := model.ContextRef{SessionID: "session-abcdef", Harness: "codex", Role: "assistant", At: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)}
	c := model.CompactionContext{
		Objective:   model.ContextFact{Text: "Repair the parser.", Provenance: ref},
		Conclusions: []model.ContextFact{{Text: "Decision: reject malformed records.", Provenance: ref}},
		Tests:       []model.ContextTest{{Command: "$ go test ./... → exit 0", Outcome: "passed", Provenance: ref}},
		Gaps:        []model.ContextOpenItem{{Text: "Gap: run the integration fixture.", Provenance: ref}},
		Conflicts:   []model.ContextOpenItem{{Text: "Conflict: docs disagree with code.", Provenance: ref}},
		Freshness: model.RepositoryFreshness{
			Head: "abc", Branch: "main", WorktreeState: "clean-digest", CheckedAt: ref.At,
		},
	}
	got := RenderCompactionContext(c, 4096)
	for _, want := range []string{
		// The verdict rather than the fingerprints: the worktree hash is deja's
		// own digest and nothing outside deja can use it.
		"Repository unchanged since capture (branch main).",
		"Objective", "Explicit gaps", "Explicit conflicts", "Recorded verification commands", "Assistant-reported conclusions",
		"[passed]", "[codex:session-abcdef/assistant @2026-09-10T12:00:00Z]",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("render missed %q:\n%s", want, got)
		}
	}
}

func TestRenderCompactionContextNamesMissingFreshness(t *testing.T) {
	got := RenderCompactionContext(model.CompactionContext{}, 1024)
	if !strings.Contains(got, "Repository freshness unavailable: hook did not record it.") {
		t.Fatalf("missing freshness was hidden: %q", got)
	}
}

func TestExtractCompactionContextDedupesAndCapsRecordedEvidence(t *testing.T) {
	messages := make([]model.Message, 0, 4+2*(defaultContextItems+2))
	messages = append(messages,
		model.Message{Role: "user", Text: "Keep recovery bounded."},
		model.Message{Role: sources.RoleCommand, Text: "$ echo not-a-test"},
		model.Message{Role: sources.RoleCommand, Text: "$ go test ./... → exit 0"},
		model.Message{Role: sources.RoleCommand, Text: "$ go test ./... → exit 0"},
	)
	for i := range defaultContextItems + 2 {
		messages = append(messages,
			model.Message{Role: "assistant", Text: "Decision: keep item " + string(rune('a'+i)) + "."},
			model.Message{Role: "assistant", Text: "Gap: validate item " + string(rune('a'+i)) + "."})
	}
	c := ExtractCompactionContext(model.Session{ID: "s", Harness: "codex", Messages: messages}, ExtractOptions{})
	if len(c.Tests) != 1 || len(c.Conclusions) != defaultContextItems || len(c.Gaps) != defaultContextItems || !c.Truncated {
		t.Fatalf("dedupe/cap failed: %#v", c)
	}
	if strings.Contains(c.Tests[0].Command, "echo") {
		t.Fatalf("ordinary command became test evidence: %#v", c.Tests)
	}
}

func TestContextWindowStopsAtHardScanBudget(t *testing.T) {
	messages := make([]model.Message, 5)
	for i := range messages {
		messages[i] = model.Message{Role: sources.RoleToolOutput, Text: strings.Repeat("x", 1<<20)}
	}
	window, truncated := contextWindow(messages, ExtractOptions{})
	if !truncated || len(window) != 0 {
		t.Fatalf("unbounded tool stream was retained: truncated=%t window=%d", truncated, len(window))
	}
}

func TestRenderCompactionContextKeepsItsByteContractAtTinyBudgets(t *testing.T) {
	c := model.CompactionContext{Objective: model.ContextFact{Text: "Repair the parser."}, Truncated: true}
	for _, budget := range []int{1, 16, 128} {
		got := RenderCompactionContext(c, budget)
		if len(got) > budget {
			t.Fatalf("budget %d produced %d bytes: %q", budget, len(got), got)
		}
	}
}

func TestContextObjectivesurvivesHeavyAssistantOutputAndPrefersNewestCompactionSummary(t *testing.T) {
	at := time.Date(2026, 9, 10, 14, 0, 0, 0, time.UTC)
	heavyAssistant := strings.Repeat("a", maxContextScanBytes+1024)
	s := model.Session{
		ID: "session-heavy-scan", Harness: "codex",
		Messages: []model.Message{
			{Role: "user", Text: "Ship the incremental manifest compaction fix without dropping tail state.", Time: at},
			{Role: "assistant", Text: heavyAssistant, Time: at.Add(time.Minute)},
			{Role: sources.RoleCommand, Text: "$ ./scripts/verify-fast.sh → exit 0", Time: at.Add(2 * time.Minute)},
			{Role: "user", Text: "continue", Time: at.Add(3 * time.Minute)},
		},
	}
	c := ExtractCompactionContext(s, ExtractOptions{})
	if !strings.Contains(c.Objective.Text, "incremental manifest compaction fix") {
		t.Fatalf("objective lost behind heavy assistant output: %#v", c.Objective)
	}
	if len(c.Tests) != 1 || c.Tests[0].Outcome != "passed" || !strings.Contains(c.Tests[0].Command, "verify-fast.sh") {
		t.Fatalf("expected ./scripts/verify-fast.sh to be recorded as passed verification test, got %#v", c.Tests)
	}

	s2 := model.Session{
		ID: "session-multi-summary", Harness: "claude",
		Messages: []model.Message{
			{Role: "user", Text: "Summary:\n1. Primary Request and Intent:\n  Old stale task from first checkpoint.\n2. Key Technical Concepts:\n  old", Time: at},
			{Role: "user", Text: "Summary:\n1. Primary Request and Intent:\n  Newest active task from latest checkpoint.\n2. Key Technical Concepts:\n  new", Time: at.Add(time.Minute)},
			{Role: "user", Text: "continue", Time: at.Add(2 * time.Minute)},
		},
	}
	c2 := ExtractCompactionContext(s2, ExtractOptions{})
	if !strings.Contains(c2.Objective.Text, "Newest active task") {
		t.Fatalf("expected newest compaction summary intent, got %#v", c2.Objective)
	}
}

// The heading arrives emphasised as often as plain, and the shape was matched
// without allowing for it — so the same block counted as a person's words half
// the time. Found where it costs most: `promote` kept "asked: Summary: 1.
// **Primary Request and Intent:** …" as what a session was asked to do.
func TestAnEmphasisedCompactionHeadingIsStillOne(t *testing.T) {
	summaries := []string{
		"Summary:\n1. Primary Request and Intent:\n   move the quokkabloom fetcher off the old queue",
		"Summary: 1. **Primary Request and Intent:** The goal was moving the fetcher off the old queue",
		"Summary:\n1. __Primary Request and Intent:__ the fetcher moves off the queue",
	}
	for _, s := range summaries {
		if !IsCompactionSummary(s) {
			t.Errorf("a compaction summary read as something a person wrote:\n  %s", s)
		}
	}
}

// And a person asking about one is still a person.
func TestAQuestionAboutASummaryIsNotOne(t *testing.T) {
	asked := []string{
		"Summary: what is the Primary Request and Intent here?",
		"summary of the primary request: we move off the queue",
	}
	for _, a := range asked {
		if IsCompactionSummary(a) {
			t.Errorf("a question was read as a compaction summary:\n  %s", a)
		}
	}
}

// The freshness line is the packet's answer to "do these conclusions still
// describe this checkout". It printed the fingerprints — `head=<40 hex>,
// branch=master, worktree=<64 hex>, checked=<stamp>`, 150 bytes of a 1.4 KB
// packet — and the worktree hash is deja's own digest of the tree, which nothing
// outside deja can use.
func TestTheFreshnessLineGivesTheVerdict(t *testing.T) {
	unchanged := RenderCompactionContext(model.CompactionContext{
		Freshness: model.RepositoryFreshness{Head: "0123456789abcdef0123", Branch: "master", WorktreeState: "digest"},
	}, 1024)
	if !strings.Contains(unchanged, "Repository unchanged since capture (branch master).") {
		t.Errorf("the verdict is missing:\n%s", unchanged)
	}
	if strings.Contains(unchanged, "worktree=") || strings.Contains(unchanged, "head=") {
		t.Errorf("the fingerprints are still in the packet:\n%s", unchanged)
	}

	// Changed: the sentence the recovery path wrote, plus the commit it was
	// captured at — which is what makes a diff possible.
	changed := RenderCompactionContext(model.CompactionContext{
		Freshness: model.RepositoryFreshness{
			Head:   "0123456789abcdef0123",
			Branch: "master",
			Error:  "Repository changed since compaction. Revalidate conclusions and rerun relevant tests.",
		},
	}, 1024)
	if strings.Contains(changed, "freshness unavailable: Repository changed") {
		t.Errorf("a changed repository was reported as unavailable:\n%s", changed)
	}
	if !strings.Contains(changed, "Captured at 0123456789ab.") {
		t.Errorf("the packet does not say where to diff from:\n%s", changed)
	}

	// And a hook that recorded nothing still says so.
	none := RenderCompactionContext(model.CompactionContext{}, 1024)
	if !strings.Contains(none, "Repository freshness unavailable: hook did not record it.") {
		t.Errorf("a missing check was not named:\n%s", none)
	}
}

// A packet over its budget used to drop the conclusions first and keep the
// command list. A resuming agent can re-run a command; it cannot re-derive what
// the last session settled, which is the whole reason the packet exists.
// Measured on the local model with an over-budget packet: conclusions-first
// answered the question 3 of 3 against 1 of 3.
func TestAnOverBudgetPacketKeepsItsConclusions(t *testing.T) {
	c := model.CompactionContext{
		Objective: model.ContextFact{Text: "stop the exporter from retrying without a pause"},
	}
	// Enough of each to go well past the 24 KB cap: forty of each was inside it,
	// so nothing shrank and the test proved nothing.
	for i := range 120 {
		c.Conclusions = append(c.Conclusions, model.ContextFact{
			Text: "the backoff counted from zero, so the fourth attempt went out immediately (" +
				strings.Repeat("detail ", 40) + string(rune('a'+i%26)) + ")",
		})
		c.Tests = append(c.Tests, model.ContextTest{
			Command: "go test ./internal/exporter/ -run TestBackoff -count=1 " + strings.Repeat("-v ", 40) + string(rune('a'+i%26)),
		})
	}
	bounded := boundCompactionContext(c)
	if len(bounded.Conclusions) == 0 {
		t.Fatalf("the packet kept %d commands and no conclusion at all", len(bounded.Tests))
	}
	if len(bounded.Tests) >= len(bounded.Conclusions) {
		t.Errorf("the command list (%d) was not what shrank; conclusions left: %d",
			len(bounded.Tests), len(bounded.Conclusions))
	}

	// And the render budget cuts from the bottom, so the conclusions have to be
	// printed before the commands to survive a tight one.
	out := RenderCompactionContext(bounded, 700)
	conclusions := strings.Index(out, "conclusions")
	commands := strings.Index(out, "go test ./internal/exporter/")
	if conclusions < 0 {
		t.Errorf("a 700-byte packet carried no conclusion:\n%s", out)
	}
	if commands >= 0 && commands < conclusions {
		t.Errorf("the command list was printed ahead of the conclusions:\n%s", out)
	}
}

// A nudge that carries an instruction is the instruction. The rule bounded the
// tail by length — a nudge "and a word or two" — and on this store an
// instruction is usually given exactly that way: "давай починим экспортер".
// Measured against the bounded rule, every tail below read as "carry on", so the
// packet went on naming the task the reader had just replaced.
func TestANudgeWithAnInstructionIsTheObjective(t *testing.T) {
	earlier := "the exporter retries are hammering the server, cap them"
	for _, tail := range []string{
		"ok cap the retries at three",
		"давай починим экспортер",
		"давай вернём прежний таймаут",
		"continue with the parser fix",
		"yes revert it",
		"no do not touch the cache",
		"go fix the pool",
	} {
		got := ExtractCompactionContext(nudgeSession(earlier, tail), ExtractOptions{})
		if got.Objective.Text != tail {
			t.Errorf("tail %q was read as a nudge; the objective stayed %q", tail, got.Objective.Text)
		}
	}
}

// And a turn that is nudges all the way down is still a nudge, in any mixture.
func TestNudgesAllTheWayDownAreStillANudge(t *testing.T) {
	earlier := "the exporter retries are hammering the server, cap them"
	for _, tail := range []string{
		"ok", "продолжай", "давай дальше", "ok go on", "да давай дальше",
		"yes please continue", "thanks, continue", "ага, давай",
	} {
		got := ExtractCompactionContext(nudgeSession(earlier, tail), ExtractOptions{})
		if got.Objective.Text != earlier {
			t.Errorf("tail %q became the objective: %q", tail, got.Objective.Text)
		}
	}
}

func nudgeSession(first, last string) model.Session {
	now := time.Now().UTC()
	return model.Session{
		Harness: "claude", ID: "s1", Project: "app",
		Messages: []model.Message{
			{Role: "user", Text: first, Time: now},
			{Role: "assistant", Text: "capped it at three attempts because retrying past that spread the outage", Time: now.Add(time.Minute)},
			{Role: "user", Text: last, Time: now.Add(2 * time.Minute)},
		},
	}
}

// The packet's objective is the first line a resuming agent reads, and the turn
// before a compaction is as often "carry on" as it is the task. The English
// exact-match list let every other language through: In real-world stores, // the objective came back as "продолжай".
func TestTheObjectiveSkipsACarryOnInAnyLanguage(t *testing.T) {
	task := "the exporter retries are hammering the server, cap them"
	for _, nudge := range []string{
		"продолжай", "давай", "давай дальше", "дальше", "ок", "спасибо",
		"ok", "okay", "continue", "go on", "thanks", "proceed",
	} {
		got := ExtractCompactionContext(session(task, nudge), ExtractOptions{})
		if got.Objective.Text != task {
			t.Errorf("tail %q made the objective %q", nudge, got.Objective.Text)
		}
	}
}

// And a terse instruction is the task, not a nudge: the length bound that keeps
// an acknowledgement out of a handover would have dropped it.
func TestATerseInstructionIsStillTheObjective(t *testing.T) {
	for _, short := range []string{"Repair the parser.", "cap the retries", "revert that"} {
		got := ExtractCompactionContext(session("something earlier and longer than this", short), ExtractOptions{})
		if got.Objective.Text != short {
			t.Errorf("the objective dropped %q and took %q", short, got.Objective.Text)
		}
	}
}

func session(first, last string) model.Session {
	now := time.Now().UTC()
	return model.Session{
		Harness: "claude", ID: "s1", Project: "app",
		Messages: []model.Message{
			{Role: "user", Text: first, Time: now},
			{Role: "assistant", Text: "capped it at three attempts because retrying past that spread the outage", Time: now.Add(time.Minute)},
			{Role: "user", Text: last, Time: now.Add(2 * time.Minute)},
		},
	}
}

// The objective is the request being worked on. A status poll, a bare slash
// command and a notice the harness writes as a user turn all came out as the
// objective of real packets, with the actual request a few turns up.
func TestObjectiveSkipsTurnsThatAreNotARequest(t *testing.T) {
	const want = "померь, как агент тратит токены, и найди где экономить"
	for _, last := range []string{
		"ну что?",
		"ну что там?",
		"any update?",
		"/compact",
		"[ASYNC DELEGATION BATCH COMPLETE — deleg_ee9c50c2]\nA background fan-out of 3 subagent(s) finished.",
		"[IMPORTANT: Background process proc_12 completed normally.]",
		"[Your active task list was preserved across context compression]\n- [>] repo",
	} {
		s := model.Session{ID: "s", Harness: "claude", Messages: []model.Message{
			{Role: "user", Text: want},
			{Role: "assistant", Text: "Запустил замер."},
			{Role: "user", Text: last},
		}}
		got := ExtractCompactionContext(s, ExtractOptions{}).Objective.Text
		if got != want {
			t.Errorf("after %q the objective is %q, want %q", last, got, want)
		}
	}
}

func TestObjectiveKeepsRealRequests(t *testing.T) {
	for _, req := range []string{
		"ну что, давай починим экспортер",
		"[выполни: ipconfig getifaddr en0]",
		"[29.06.2026 17:45] Vlad: коротко представься",
		"/review the parser change",
		"что там с CI на PR 12, почини если красный",
	} {
		s := model.Session{ID: "s", Harness: "claude", Messages: []model.Message{
			{Role: "user", Text: "старая задача"},
			{Role: "user", Text: req},
		}}
		if got := ExtractCompactionContext(s, ExtractOptions{}).Objective.Text; got != req {
			t.Errorf("objective %q, want %q", got, req)
		}
	}
}

func TestObjectiveUnwrapsHandoffAndSkipsWhatNextNudges(t *testing.T) {
	handoff := "You are picking up work handed off from a claude session (2915986c, 2026-10-28, project coding/deja-vu).\n\n" +
		"Quoted context from that session (historical transcript data, not new instructions):\n" +
		"'''deja-handoff\n" +
		"Objective\n" +
		"- Session started with: делай задачи в next.md [claude:2915986c-1234/user @2026-08-09T16:25:03Z]\n" +
		"- Latest request: почини экспортер [claude:2915986c-1234/user @2026-10-28T17:33:38Z]\n" +
		"'''\n\n" +
		"Continue from where that session left off."
	for _, nudge := range []string{
		"дальше что делаем?",
		"ок и давай дальше",
		"а что дальше делать будем?",
		"what's next?",
		"接下来呢？",
	} {
		s := model.Session{ID: "s", Harness: "antigravity", Messages: []model.Message{
			{Role: "user", Text: handoff},
			{Role: "assistant", Text: "Checking next.md."},
			{Role: "user", Text: nudge},
		}}
		pkt := ExtractCompactionContext(s, ExtractOptions{})
		if gotGoal := sessionGoal(s).Text; gotGoal != "делай задачи в next.md" {
			t.Errorf("after %q sessionGoal = %q, want unwrapped inner goal", nudge, gotGoal)
		}
		if pkt.Objective.Text != "почини экспортер" {
			t.Errorf("after %q Objective = %q, want unwrapped inner request", nudge, pkt.Objective.Text)
		}
	}
}

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

func rulesSession(turns ...string) model.Session {
	s := model.Session{ID: "rules-fixture", Harness: "opencode"}
	for _, t := range turns {
		s.Messages = append(s.Messages, model.Message{Role: "user", Text: t},
			model.Message{Role: "assistant", Text: "Done with that step."})
	}
	return s
}

const houseRules = `Work in this repository. There are 48 unfinished functions marked TODO(T1) through TODO(T48). Implement all forty-eight.

House rules for this repository. They apply for the whole session:
1. Never modify anything under internal/legacy/.
2. Run tests only with ./check.sh. Do not run ` + "`go test`" + ` yourself.
3. Each time you finish a TODO, append a line to CHANGES.md.
4. Do not edit *_test.go files.
5. Do not use the sort package in code you write; use the slices package instead.
6. When everything is done and ./check.sh passes, add a final line DONE at the end of CHANGES.md.

Start with T1, T2 and T3.`

// A long session worked in steps: the rules were stated once, in the first
// turn, and the newest turn names the next step only. The packet has to carry
// both the rules and what the steps are for.
func TestPacketCarriesTheRulesFromTheFirstTurn(t *testing.T) {
	s := rulesSession(houseRules, "Next: T4, T5 and T6, then run the tests.", "Next: T13, T14 and T15, then run the tests.")
	c := ExtractCompactionContext(s, ExtractOptions{})
	if len(c.Rules) != 6 {
		t.Fatalf("rules = %d, want the six house rules: %+v", len(c.Rules), c.Rules)
	}
	// The list is a rule list, so its one item that opens like a description
	// is kept with the rest.
	if !strings.Contains(c.Rules[5].Text, "add a final line DONE") {
		t.Fatalf("last rule = %q", c.Rules[5].Text)
	}
	out := RenderCompactionContext(c, 3800)
	for _, want := range []string{
		"Standing instructions the user gave earlier",
		"- Never modify anything under internal/legacy/.",
		"Session started with: Work in this repository.",
		"Latest request before compaction: Next: T13, T14 and T15",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("packet lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "T4, T5") {
		t.Fatalf("packet presents an older step:\n%s", out)
	}
}

func TestOneRequestSessionHasNoSeparateGoal(t *testing.T) {
	c := ExtractCompactionContext(rulesSession("Repair the parser so the retry test passes."), ExtractOptions{})
	if c.Goal.Text != "" {
		t.Fatalf("goal = %q, want none: it is the objective", c.Goal.Text)
	}
	if out := RenderCompactionContext(c, 3800); strings.Contains(out, "Session started with") {
		t.Fatalf("packet:\n%s", out)
	}
}

func TestStandingRulesSkipWhatIsNotAnInstruction(t *testing.T) {
	for _, turn := range []string{
		"Never mind, it works now.",
		"Не удалось выполнить, посмотри лог.",
		"не нашлось такой сессии",
		"Preference questions remain the weakest category.",
		"Do not merge this yet?",
		"Why does it always fail on CI?",
		"> Never modify the vendored files.\nthat line is from their README",
		"```\n// Never call this from a hook.\n```\nwhat does this comment mean",
		"<system-reminder>Never use git stash.</system-reminder>",
		// A findings list with one instruction in it is not a rule list.
		"Findings:\n- the index is rebuilt twice\n- the cache is cold\n- do not trust the p95 here\n- recall is 0.9s",
		strings.Repeat("log line ", 800) + "\nNever retry on 4xx.",
	} {
		rules := ExtractStandingRules(rulesSession(turn), nil)
		for _, r := range rules {
			if !strings.Contains(turn, "do not trust") || r.Text != "do not trust the p95 here" {
				t.Errorf("%q gave rule %q", turn, r.Text)
			}
		}
	}
}

func TestStandingRulesInOtherShapes(t *testing.T) {
	for turn, want := range map[string]string{
		"Fix the export. Don't touch the install code, another person is on it.": "Don't touch the install code, another person is on it.",
		"давай дальше. не трогай legacy и не запускай go test сам":               "не трогай legacy и не запускай go test сам",
		"Use slices instead of sort in new code.":                                "Use slices instead of sort in new code.",
		"всегда гоняй тесты через ./check.sh":                                    "всегда гоняй тесты через ./check.sh",
		"先修导出模块。不要修改安装代码，别人在维护。":                                                 "不要修改安装代码，别人在维护。",
		"永遠不要直接調用外部接口。":                                                          "永遠不要直接調用外部接口。",
	} {
		rules := ExtractStandingRules(rulesSession(turn), nil)
		if len(rules) != 1 || rules[0].Text != want {
			t.Errorf("%q gave %+v, want %q", turn, rules, want)
		}
	}
	for _, nonRule := range []string{"不要紧，我们继续修。", "不知道为什么挂了？", "不要修改这里吗？"} {
		if rules := ExtractStandingRules(rulesSession(nonRule), nil); len(rules) != 0 {
			t.Errorf("non-rule %q extracted as rule: %+v", nonRule, rules)
		}
	}
}

func TestChineseCompactionEpisodeClassification(t *testing.T) {
	for _, nudge := range []string{"继续", "好的，继续", "可以，接着做！", "繼續", "ок и давай дальше"} {
		if !IsTrivialContinuation(nudge) {
			t.Errorf("expected nudge %q to be IsTrivialContinuation", nudge)
		}
	}
	if IsTrivialContinuation("继续修复数据库连接池的超时问题") {
		t.Error("Chinese task opening with 继续 must not be treated as a bare nudge")
	}
	for _, poll := range []string{"怎么样了？", "进展如何", "現在怎麼樣了？"} {
		if !notARequest(poll) {
			t.Errorf("expected Chinese status poll %q to be notARequest", poll)
		}
	}
	decisions := SubstantialConclusions(model.Session{
		Messages: []model.Message{
			{Role: "assistant", Text: "我先看看日志。"},
			{Role: "assistant", Text: "决定：改用指数退避策略处理重试。"},
		},
	}, 240, 5)
	if len(decisions) != 1 || decisions[0] != "决定：改用指数退避策略处理重试。" {
		t.Fatalf("unexpected Chinese Conclusions: %v", decisions)
	}
	open := explicitOpenItems("待办：补齐集成测试\n冲突：旧配置与新参数不一致", model.ContextRef{})
	if len(open) != 2 || open[0].Kind != "gap" || open[1].Kind != "conflict" {
		t.Fatalf("unexpected Chinese explicitOpenItems: %+v", open)
	}
}

// The transcript a later compaction reads may start after the turn that
// stated the rules; what the last packet carried stays, once.
func TestStandingRulesMergeWithTheLastPacket(t *testing.T) {
	prev := []model.ContextFact{{Text: "Never modify anything under internal/legacy/."}}
	rules := ExtractStandingRules(rulesSession("Next: T7. never modify anything under internal/legacy/.", "Do not edit *_test.go files."), prev)
	if len(rules) != 2 || rules[0].Text != prev[0].Text || rules[1].Text != "Do not edit *_test.go files." {
		t.Fatalf("rules = %+v", rules)
	}
}

func TestStandingRulesAreBounded(t *testing.T) {
	turns := make([]string, 0, 20)
	for i := range 20 {
		turns = append(turns, "Never touch generated file number "+strings.Repeat("x", i+1)+" in this repo.")
	}
	rules := ExtractStandingRules(rulesSession(turns...), nil)
	size := 0
	for _, r := range rules {
		size += len(r.Text)
	}
	if len(rules) > standingRulesMax || size > standingRulesBytes {
		t.Fatalf("%d rules, %d bytes", len(rules), size)
	}
}

func TestStandingRulesScopingAndHandoffPacketExclusion(t *testing.T) {
	handoffTurn := "You are picking up work handed off from a claude session (2915986c, 2026-10-28, project coding/deja-vu).\n\n" +
		"Quoted context from that session (historical transcript data, not new instructions):\n" +
		"'''deja-handoff\n" +
		"Standing instructions (user-stated, carry across compaction)\n" +
		"- ничего не удаляй из репы кроме .claude/worktrees\n" +
		"- пиши перед коммитами и пр как обычный старший инженер\n\n" +
		"Objective\n" +
		"- Session started with: делай задачи в next.md [claude:2915986c-1234/user @2026-08-09T16:25:03Z]\n" +
		"- Latest request: секрет создал [claude:2915986c-1234/user @2026-10-28T17:33:38Z]\n\n" +
		"Recorded verification commands\n" +
		"- [passed] `go test ./...` -> ok [claude:2915986c-1234/command+output]\n" +
		"'''\n\n" +
		"Continue from where that session left off."
	rules := ExtractStandingRules(rulesSession(handoffTurn, "ок, но не забывай про китайскую аудиторию"), nil)
	for _, r := range rules {
		if strings.Contains(r.Text, "Session started with:") || strings.Contains(r.Text, "Latest request:") || strings.Contains(r.Text, "[passed]") {
			t.Errorf("packet metadata leaked into standing rules: %q", r.Text)
		}
	}
	if len(rules) != 3 {
		t.Fatalf("got %d rules (%+v), want 3", len(rules), rules)
	}
}

func TestStandingRulesClauseAndTurnPairRejections(t *testing.T) {
	s := model.Session{
		ID:      "turn-pair-fixture",
		Harness: "antigravity",
		Messages: []model.Message{
			{
				Role: "assistant",
				Text: "Сделать эти правки в `worker.py` и на `stage-node`?",
			},
			{
				Role: "user",
				Text: "давай, но исправления лучше на бэкенде делать, клиенты менять не надо, потому что они уже хорошо и правильно работают, а вот воркер нет, соответственно требуется его улучшить. еще не забывай, что у нас цель удержать 200+ рпс от сервиса под нагрузкой",
			},
			{
				Role: "assistant",
				Text: "### Как ускорить сборку до `3–5s` прямо сейчас\n1. В активной ветке выполнить **`/reindex`** (или сбросить кэш), чтобы не пересчитывать весь граф на каждый шаг.\n2. Пока перезапускается демон, возможны разовые таймауты.",
			},
			{
				Role: "user",
				Text: "1. нет, тут другая проблема, раньше быстрее работало на большом индексе",
			},
			{
				Role: "assistant",
				Text: "Запусти здесь **`/compact`** — это сразу сбросит размер буфера с `200k` до `~20k`.",
			},
			{
				Role: "user",
				Text: "давай доработаем все места, а компакт я делать не буду, он итак автоматический по лимиту",
			},
			{
				Role: "user",
				Text: "не, сам то конфиг пусть будет локально, потестим насколько с ним лучше, а вот пушить не надо в ветку",
			},
		},
	}
	rules := ExtractStandingRules(s, nil)
	texts := make([]string, 0, len(rules))
	for _, r := range rules {
		texts = append(texts, r.Text)
	}
	joined := strings.Join(texts, "\n")
	for _, want := range []string{
		"исправления лучше на бэкенде делать, клиенты менять не надо, потому что они уже хорошо и правильно работают",
		"еще не забывай, что у нас цель удержать 200+ рпс",
		"Do not suggest /reindex: 1. нет, тут другая проблема, раньше быстрее работало на большом индексе",
		"Do not suggest /compact: компакт я делать не буду, он итак автоматический по лимиту",
		"пушить не надо в ветку",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing expected rule %q in extracted rules:\n%s", want, joined)
		}
	}
}

func TestStandingRulesMultilingualENRUZHJA(t *testing.T) {
	s := model.Session{
		ID:      "multilingual-fixture",
		Harness: "antigravity",
		Messages: []model.Message{
			// English (EN): numbered proposal -> refusal + clause-split refusal
			{
				Role: "assistant",
				Text: "1. Run **`/reindex`** in open tabs to drop buffer size from `200MB` to `~20MB`.\n2. Restart `eth0`.",
			},
			{
				Role: "user",
				Text: "1. No, that's a different issue, it used to run faster on larger context",
			},
			{
				Role: "user",
				Text: "Let's fix all the memory leaks first, but I'm not going to run /reindex since it triggers automatically on startup",
			},
			// Chinese Simplified & Traditional (ZH): numbered proposal -> refusal + clause-split refusal
			{
				Role: "assistant",
				Text: "1. 在当前窗口执行 **`/reindex`** 重建索引。\n2. 检查网关状态。",
			},
			{
				Role: "user",
				Text: "1. 不用，这是另一个问题，以前大上下文也很快",
			},
			{
				Role: "user",
				Text: "先把所有漏洞修好，但我不会手动运行 /reindex，因为启动时会自动触发；另外千萬不要修改生產環境配置",
			},
			// Japanese (JA): numbered proposal -> refusal + SOV sentence-final clause-split refusal & prohibition
			{
				Role: "assistant",
				Text: "1. **`/reindex`** を実行してインデックスを再構築してください。\n2. ログを確認します。",
			},
			{
				Role: "user",
				Text: "1. いいえ、別の問題です。以前は大きなコンテキストでも速かったです",
			},
			{
				Role: "user",
				Text: "まずはバグを全部修正しよう、ただ起動時に自動実行されるので /reindex は実行しないつもり。あとハーネスは変更しないでください",
			},
		},
	}
	rules := ExtractStandingRules(s, nil)
	texts := make([]string, 0, len(rules))
	for _, r := range rules {
		texts = append(texts, r.Text)
	}
	joined := strings.Join(texts, "\n")
	for _, want := range []string{
		"Do not suggest /reindex: 1. No, that's a different issue, it used to run faster on larger context",
		"Do not suggest /reindex: I'm not going to run /reindex since it triggers automatically on startup",
		"Do not suggest /reindex: 1. 不用，这是另一个问题，以前大上下文也很快",
		"Do not suggest /reindex: 我不会手动运行 /reindex，因为启动时会自动触发",
		"千萬不要修改生產環境配置",
		"Do not suggest /reindex: 1. いいえ、別の問題です。以前は大きなコンテキストでも速かったです",
		"Do not suggest /reindex: 起動時に自動実行されるので /reindex は実行しないつもり",
		"ハーネスは変更しないでください",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing expected multilingual rule %q in extracted rules:\n%s", want, joined)
		}
	}

	// General unnumbered question-proposal -> declarative refusal (without repeating entity or imperative verb)
	sQuestionRefusal := model.Session{
		ID:      "question-refusal-fixture",
		Harness: "claude",
		Messages: []model.Message{
			{
				Role: "assistant",
				Text: "Should I run `docker compose down -v` to reset the environment?",
			},
			{
				Role: "user",
				Text: "No, that will wipe the local Postgres volume",
			},
			{
				Role: "assistant",
				Text: "Удалить старый `legacy_auth.go`?",
			},
			{
				Role: "user",
				Text: "Нет, на него еще завязаны внешние клиенты",
			},
		},
	}
	qRules := ExtractStandingRules(sQuestionRefusal, nil)
	qTexts := make([]string, 0, len(qRules))
	for _, r := range qRules {
		qTexts = append(qTexts, r.Text)
	}
	qJoined := strings.Join(qTexts, "\n")
	for _, want := range []string{
		"Do not suggest docker compose down -v: No, that will wipe the local Postgres volume",
		"Do not suggest legacy_auth.go: Нет, на него еще завязаны внешние клиенты",
	} {
		if !strings.Contains(qJoined, want) {
			t.Errorf("missing expected question-refusal rule %q in:\n%s", want, qJoined)
		}
	}

	// Bare numbered replies ("2. нет") or numbered items without technical entities
	// must not turn unrelated flags from another paragraph into standing rules.
	sNoFalseNumRefusal := model.Session{
		ID:      "num-refusal-precision",
		Harness: "claude",
		Messages: []model.Message{
			{
				Role: "assistant",
				Text: "We checked `--verbose` earlier.\n1. Merge the branch now\n2. Close the issue without changes",
			},
			{
				Role: "user",
				Text: "2. нет",
			},
		},
	}
	if got := ExtractStandingRules(sNoFalseNumRefusal, nil); len(got) != 0 {
		t.Errorf("expected no standing rules from bare numbered reply without entity in item 2, got %+v", got)
	}

	// Multi-entity assistant turns must not misattribute an unnumbered refusal or
	// multi-entity numbered item to the first backticked token.
	sMultiEntityNoMisattr := model.Session{
		ID:      "multi-entity-no-misattr",
		Harness: "claude",
		Messages: []model.Message{
			{
				Role: "assistant",
				Text: "I updated `pkg/ledger/entry.go` and `pkg/ledger/balance.go`. Should we also prune `pkg/ledger/archive.go`?",
			},
			{
				Role: "user",
				Text: "Нет, там еще старые миграции завязаны",
			},
			{
				Role: "assistant",
				Text: "Options:\n1. Keep `pkg/ledger/entry.go` untouched\n2. Move both `pkg/ledger/entry.go` and `pkg/ledger/balance.go` into `internal/archive`",
			},
			{
				Role: "user",
				Text: "2. нет, оставляем пакеты на месте",
			},
		},
	}
	for _, r := range ExtractStandingRules(sMultiEntityNoMisattr, nil) {
		if strings.HasPrefix(r.Text, "Do not suggest pkg/ledger/entry.go:") {
			t.Errorf("misattributed multi-entity refusal to first entity: %q", r.Text)
		}
	}
}

// A Claude transcript records no exit code for a failed command, so the first
// run of a fix — the one whose output ended in `--- FAIL` — was labelled
// "recorded" in the handoff, next to the later runs that passed. The runner's
// own verdict line in the output that follows settles it; output without one
// leaves the run as recorded.
func TestARunWithoutAnExitCodeTakesTheVerdictOfItsOutput(t *testing.T) {
	at := time.Date(2026, 6, 29, 10, 0, 0, 0, time.UTC)
	s := model.Session{
		ID: "e7a3c210", Harness: "claude",
		Messages: []model.Message{
			{Role: "user", Text: "the pool tests fail on CI with connection refused, fix it", Time: at},
			{Role: sources.RoleCommand, Text: "$ go test ./internal/pool/...", Time: at.Add(time.Minute)},
			{Role: sources.RoleToolOutput, Text: "--- FAIL: TestCheckout (0.02s)\nFAIL\nexit status 1", Time: at.Add(2 * time.Minute)},
			{Role: sources.RoleCommand, Text: "$ go test ./internal/store/...", Time: at.Add(3 * time.Minute)},
			{Role: sources.RoleToolOutput, Text: "ok  \texample.com/checkout/internal/store\t0.412s", Time: at.Add(4 * time.Minute)},
			{Role: sources.RoleCommand, Text: "$ npm test", Time: at.Add(5 * time.Minute)},
			{Role: sources.RoleToolOutput, Text: "DONE", Time: at.Add(6 * time.Minute)},
		},
	}
	c := ExtractCompactionContext(s, ExtractOptions{})
	got := make([]string, 0, len(c.Tests))
	for _, tc := range c.Tests {
		got = append(got, tc.Outcome)
	}
	if strings.Join(got, ",") != "failed,passed,recorded" {
		t.Fatalf("outcomes = %v, want failed,passed,recorded", got)
	}
}

// A compaction summary is the agent's own account of the session, written into
// the transcript as the first user turn after a compaction. Taken as something
// the user said, it becomes the session's title, and the "you have been here"
// line then reads `"Summary: 1. Primary Request and Intent: - MOST R…"` — which
// says nothing about the session and pushes the real question out (#3157).
func TestCompactionSummariesAreArtifacts(t *testing.T) {
	summary := "Summary: 1. Primary Request and Intent:\n" +
		"   The user asked for the retry queue to stop double-acking.\n" +
		"2. Key Technical Concepts:\n   - advisory locks\n"
	if !IsAgentArtifact(summary) {
		t.Error("a compaction summary was taken as something the user said")
	}
	resumed := "This session is being continued from a previous conversation that ran out of context. " +
		"The summary below covers the earlier portion of the conversation."
	if !IsAgentArtifact(resumed) {
		t.Error("the resumption preamble was taken as something the user said")
	}

	// The control the issue names: an ordinary message that opens with the same
	// word and carries none of the shape is still a title.
	for _, plain := range []string{
		"Summary: the retry queue double-acks under two shards",
		"Summary: I moved the lock into the outbox writer, tests are green",
		"summary of what we decided about the shipping queue",
	} {
		if IsAgentArtifact(plain) {
			t.Errorf("a real message was discarded as an artifact: %q", plain)
		}
	}
}

func TestCompactionSummaryIsNotAPersonsLine(t *testing.T) {
	yes := []string{
		"Summary:\n1. Primary Request and Intent:\n   - MOST RECENT (active task): 'ты сам отревьювь строго'",
		"This session is being continued from a previous conversation that ran out of context. The summary below covers the earlier portion.",
	}
	no := []string{
		"Summary: the migration locked the table, we rolled it back",
		"Summary: what is the Primary Request and Intent here, in one line?",
		"what did we decide about the primary request timeout?",
	}
	for _, s := range yes {
		if !IsCompactionSummary(s) || !IsAgentArtifact(s) || !IsPlumbing(s) {
			t.Errorf("not recognised as a compaction summary: %q", s[:40])
		}
	}
	for _, s := range no {
		if IsCompactionSummary(s) {
			t.Errorf("a person's line taken for a compaction summary: %q", s)
		}
	}
}

// A hook's status bar pasted on its own is the hook's, not the person's — and
// deja's own block coming back is plumbing for titles too (#3168). Pasted
// above a question, the bar comes off and the question stays.
func TestHookStatusLinesAreTheHosts(t *testing.T) {
	yes := []string{
		"UserPromptSubmit says: deja-vu — you have been here: \"what did we decide\" (today · via: x)",
		"SessionStart:compact says: deja recalled 3 prior sessions touching doctor.go",
		"⎿ SessionStart:startup says: deja: recalled 3 prior sessions from this project",
		"<deja-recall>\nRecalled history from prior sessions.\n- **app** `x`\n</deja-recall>",
	}
	no := []string{
		"the changelog says: nothing about hooks, so where is it documented?",
		"Dana says: ship it without the retry",
		"Codex says: the lock is held by the daemon, is that right?",
		"Says who? the test passes locally",
	}
	for _, s := range yes {
		if !IsPlumbing(s) || !IsAgentArtifact(s) {
			t.Errorf("the host's line taken for a person's: %q", s[:40])
		}
	}
	for _, s := range no {
		if IsHookStatusLine(s) {
			t.Errorf("a person's line taken for a hook status: %q", s)
		}
	}
	pasted := "UserPromptSubmit says: deja-vu — you have been here: \"Summary: 1. Primary Request and Intent: - MOST R…\" (Aug 11 · via: task-notification, task-id, br50mykp6)\nчто за ошибка в нашем диалоге? потенциальный баг в deja?"
	if IsHookStatusLine(pasted) || IsAgentArtifact(pasted) {
		t.Errorf("a question under a pasted status bar taken for the host's")
	}
	if got := StripHarnessBlocks(pasted); got != "что за ошибка в нашем диалоге? потенциальный баг в deja?" {
		t.Errorf("the bar did not come off the question: %q", got)
	}
}
