package digest

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// carryEv is a transcript in the shorthand of the reference cases: "u" a user
// turn, "a" assistant text, "t" a command.
func carryEv(pairs ...string) []model.Message {
	role := map[string]string{"u": "user", "a": "assistant", "t": sources.RoleCommand, "s": "user"}
	var out []model.Message
	for i := 0; i < len(pairs); i += 2 {
		out = append(out, model.Message{Role: role[pairs[i]], Text: pairs[i+1]})
	}
	return out
}

type carryWant struct{ kind, text string }

func carryGot(items []model.ContextCarry) []carryWant {
	var out []carryWant
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
				{Kind: "open", Key: "#7307", Text: "Zed | реестр расширений | PR #7307 ждёт мейнтейнера"},
				{Kind: "recheck", Text: "Вернусь с результатом через час."},
				{Kind: "verdict", Text: "Гипотеза «частые релизы = рост» опровергнута нашими же числами."},
			},
			[]carryWant{{"open", "Zed | реестр расширений | PR #7307 ждёт мейнтейнера"}, {"verdict", "Гипотеза «частые релизы = рост» опровергнута нашими же числами."}}},
		{"the user says merged with the number",
			carryEv("u", "вмержил 7307", "a", "Ок."),
			[]model.ContextCarry{{Kind: "open", Key: "#7307", Text: "PR #7307 ждёт твоего мержа"}}, nil},
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
			[]model.ContextCarry{open("#7307", "PR #7307 ждёт мейнтейнера", 2)}, nil},
		{"waiting on CI is not carried",
			carryEv("u", "go"),
			[]model.ContextCarry{open("#7307", "PR #7307 ждёт зелёного CI", 0)}, nil},
		{"a user merged without a number closes what waited on them",
			carryEv("u", "смержил"),
			[]model.ContextCarry{open("#7307", "PR #7307 ждёт твоего мержа", 0)}, nil},
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
			carryEv("u", "go", "a", "PR #7307 всё ещё ждёт мейнтейнера."),
			[]model.ContextCarry{open("#7307", "PR #7307 ждёт мейнтейнера", 0)},
			[]carryWant{{"open", "PR #7307 всё ещё ждёт мейнтейнера."}}},
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
	var text []string
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
			{Kind: "open", Key: "#7307", Text: strings.Repeat("длинное описание ", 20) + "PR #7307 ждёт мейнтейнера"},
			{Kind: "recheck", Text: "Посмотрю завтра на чистых сутках."},
			{Kind: "verdict", Text: "H487 отвергнута."},
		},
	}
	out := RenderCompactionContext(c, 0)
	carry, objective := strings.Index(out, carryHeader), strings.Index(out, "\nObjective\n")
	if carry < 0 || objective < 0 || carry > objective {
		t.Fatalf("carry section must precede the objective:\n%s", out)
	}
	for _, want := range []string{"- [awaiting you] Splitting", "- [open] #7307 длинное", "- [recheck] Посмотрю", "- [verdict] H487"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	// A line past the budget is left out and said to be.
	tight := RenderCompactionContext(c, 400)
	if !strings.Contains(tight, "- [awaiting you]") || strings.Contains(tight, "- [open]") || !strings.Contains(tight, "Omitted") {
		t.Fatalf("tight budget:\n%s", tight)
	}
	if RenderCarry(nil) != "" || !strings.HasPrefix(RenderCarry(c.Carry), carryHeader+"\n- [awaiting you]") {
		t.Fatalf("RenderCarry: %q", RenderCarry(c.Carry))
	}
}

// Printed first, a full list would push the objective and the conclusions out
// of a recovery-sized packet; it keeps to its share and says what it left out.
func TestCarryKeepsToItsShareOfThePacket(t *testing.T) {
	c := model.CompactionContext{
		Objective:   model.ContextFact{Text: "Fix retry cancellation."},
		Conclusions: []model.ContextFact{{Text: "Root cause: the retry loop ignored ctx.Done."}},
	}
	for i := 0; i < 12; i++ {
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
		var out []string
		for _, c := range ExtractCarry(carryEv("u", "go", "a", text), nil) {
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
