package search

import (
	"fmt"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func TestSessionStartProblemMatchesTailConclusionTopic(t *testing.T) {
	msgs := make([]model.Message, 0, 54)
	msgs = append(msgs,
		model.Message{Role: "user", Text: "wg будто бы упал в кластере 10.10.0.10"},
		model.Message{Role: "assistant", Text: "Проверил wg0 — пиры кластера поднялись."},
	)
	for i := range 25 {
		msgs = append(msgs,
			model.Message{Role: "user", Text: fmt.Sprintf("проверка шага %d", i)},
			model.Message{Role: "assistant", Text: fmt.Sprintf("результат шага %d", i)},
		)
	}
	msgs = append(msgs,
		model.Message{Role: "user", Text: "как то впн херово работает между московской и венской площадками, постоянно подлагивает"},
		model.Message{Role: "assistant", Text: "Главное: причина не ушла. Я не могу починить сеть у хостера между их московской и венской площадками — это их маршрутизация, не моя конфигурация."},
	)
	s := model.Session{
		Harness:  "opencode",
		ID:       "multi-topic-vpn",
		Project:  "goprojects/routepilot",
		Messages: msgs,
	}
	block := AutoRecallDigest([]model.Session{s}, 1200)
	if strings.Contains(block, "wg будто бы упал") {
		t.Fatalf("session-start digest spliced unrelated opening topic with tail conclusion:\n%s", block)
	}
	if !strings.Contains(block, "как то впн херово работает") {
		t.Fatalf("expected session-start digest to pair tail conclusion with its own user problem:\n%s", block)
	}
}

func TestAnswerAfterSkipsPlanOpenerAcrossToolCalls(t *testing.T) {
	s := model.Session{
		Messages: []model.Message{
			{Role: "user", Text: "почему ты путаешь кластерный wg и личный splitbox?"},
			{Role: "assistant", Text: "Понял, пересобираю картину."},
			{Role: "tool", Text: "ps aux | grep sing-box"},
			{Role: "tool", Text: "cat /var/run/splitbox/sing-box.json"},
			{Role: "assistant", Text: "Архитектура: sing-box 95410 держит личный VLESS через фронт до Вены, а sing-box-cluster.json держит WireGuard 10.10.0.10/24 до кластера."},
		},
	}
	got := AnswerAfter(s.Messages, 0)
	if strings.Contains(got, "пересобираю картину") {
		t.Fatalf("AnswerAfter stopped at plan opener instead of diagnosis: %q", got)
	}
	if !strings.Contains(got, "Архитектура:") {
		t.Fatalf("expected AnswerAfter to return architectural conclusion, got %q", got)
	}
}

func TestMatchedLinesAskedKeepsUserCorrectionAndDropsDistantUnrelatedUser(t *testing.T) {
	msgs := make([]model.Message, 0, 41)
	msgs = append(msgs,
		model.Message{Role: "user", Text: "почему лагает впн сегодня вечером"},
		model.Message{Role: "assistant", Text: "Смотрю логи."},
	)
	for i := range 35 {
		msgs = append(msgs, model.Message{Role: "tool", Text: fmt.Sprintf("log line %d", i)})
	}
	msgs = append(msgs,
		model.Message{Role: "user", Text: "причем тут кластер? не путай, у меня два разных впн — личный splitbox до вены и рабочий wg до кластера"},
		model.Message{Role: "assistant", Text: "Понял, пересобираю картину."},
		model.Message{Role: "tool", Text: "cat /etc/splitbox/config.yaml"},
		model.Message{Role: "assistant", Text: "Архитектура: личный splitbox маршрутизируется через фронт 178.236.240.175 до Вены, а рабочий wg идет в 10.10.0.10."},
	)
	s := model.Session{
		Harness:  "opencode",
		ID:       "disambig-vpn",
		Project:  "Users/shulcz",
		Messages: msgs,
	}
	block := AutoRecallDigestFor([]model.Session{s}, 1200, []string{"splitbox", "вена", "впн"})
	if !strings.Contains(block, "не путай") {
		t.Fatalf("expected AutoRecallDigest to surface user disambiguation turn, got:\n%s", block)
	}
	if !strings.Contains(block, "Архитектура:") {
		t.Fatalf("expected AutoRecallDigest to attach architectural reply after plan opener, got:\n%s", block)
	}
}

func TestCompactionSummaryDoesNotSpliceUnrelatedBullets(t *testing.T) {
	compaction := "This session is being continued from a previous conversation that ran out of context.\n\n" +
		"6. All user messages:\n" +
		"- настроил рабочий wireguard wg0 до кластера 10.10.0.10\n" +
		"- проверил личный splitbox vless через вену\n"
	s := model.Session{
		Harness: "claude",
		ID:      "compacted-sess",
		Messages: []model.Message{
			{Role: "user", Text: compaction},
			{Role: "assistant", Text: "Продолжаем работу."},
		},
	}
	if SpeechCarriesTermsTogether(s, []string{"wireguard", "vless"}, 2) {
		t.Fatal("expected SpeechCarriesTermsTogether not to match terms across separate bullets of a compaction summary")
	}
}

func TestLeadWithConclusionPrefersUserCorrection(t *testing.T) {
	s1 := model.Session{
		ID: "ordinary-concluded",
		Messages: []model.Message{
			{Role: "user", Text: "проверь фронт и вену"},
			{Role: "assistant", Text: "Причина: маршрут через фронт до вены работает штатно."},
		},
	}
	s2 := model.Session{
		ID: "user-disambiguation",
		Messages: []model.Message{
			{Role: "user", Text: "не путай, у меня два разных впн: splitbox идет через фронт до вены, а wg для доступа к кластеру"},
			{Role: "assistant", Text: "Понял, зафиксировал."},
		},
	}
	got := LeadWithConclusion([]model.Session{s1, s2}, []string{"splitbox", "фронт", "вена", "wg"})
	if got[0].ID != "user-disambiguation" {
		t.Fatalf("expected LeadWithConclusion to lead with user disambiguation session, got %s first", got[0].ID)
	}
}
