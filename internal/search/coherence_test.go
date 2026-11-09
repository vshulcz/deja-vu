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
		model.Message{Role: "user", Text: "очередь ретраев встала на воркере 10.10.0.10"},
		model.Message{Role: "assistant", Text: "Проверил воркеры — консьюмеры очереди поднялись."},
	)
	for i := range 25 {
		msgs = append(msgs,
			model.Message{Role: "user", Text: fmt.Sprintf("проверка шага %d", i)},
			model.Message{Role: "assistant", Text: fmt.Sprintf("результат шага %d", i)},
		)
	}
	msgs = append(msgs,
		model.Message{Role: "user", Text: "как то репликация медленно работает между основной и резервной базой, постоянно отстает"},
		model.Message{Role: "assistant", Text: "Главное: причина не ушла. Задержка на канале провайдера между основной и резервной базой — это их линк, не настройки пула."},
	)
	s := model.Session{
		Harness:  "opencode",
		ID:       "multi-topic-db",
		Project:  "goprojects/netgateway",
		Messages: msgs,
	}
	block := AutoRecallDigest([]model.Session{s}, 1200)
	if strings.Contains(block, "очередь ретраев встала") {
		t.Fatalf("session-start digest spliced unrelated opening topic with tail conclusion:\n%s", block)
	}
	if !strings.Contains(block, "как то репликация медленно работает") {
		t.Fatalf("expected session-start digest to pair tail conclusion with its own user problem:\n%s", block)
	}
}

func TestAnswerAfterSkipsPlanOpenerAcrossToolCalls(t *testing.T) {
	s := model.Session{
		Messages: []model.Message{
			{Role: "user", Text: "почему ты путаешь внутренний биллинг и публичный edgeproxy?"},
			{Role: "assistant", Text: "Понял, пересобираю картину."},
			{Role: "tool", Text: "ps aux | grep envoy"},
			{Role: "tool", Text: "cat /var/run/edgeproxy/envoy.json"},
			{Role: "assistant", Text: "Архитектура: envoy 95410 держит публичный edgeproxy через шлюз витрины, а envoy-internal.json держит контур 10.10.0.10/24 до биллинга."},
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
		model.Message{Role: "user", Text: "почему лагает шлюз сегодня вечером"},
		model.Message{Role: "assistant", Text: "Смотрю логи."},
	)
	for i := range 35 {
		msgs = append(msgs, model.Message{Role: "tool", Text: fmt.Sprintf("log line %d", i)})
	}
	msgs = append(msgs,
		model.Message{Role: "user", Text: "причем тут биллинг? не путай, у меня два разных шлюза — публичный edgeproxy для витрины и внутренний контур для биллинга"},
		model.Message{Role: "assistant", Text: "Понял, пересобираю картину."},
		model.Message{Role: "tool", Text: "cat /etc/edgeproxy/config.yaml"},
		model.Message{Role: "assistant", Text: "Архитектура: публичный edgeproxy маршрутизируется через шлюз 203.0.113.10 для витрины, а внутренний контур идет в 10.10.0.10."},
	)
	s := model.Session{
		Harness:  "opencode",
		ID:       "disambig-proxy",
		Project:  "Users/alex",
		Messages: msgs,
	}
	block := AutoRecallDigestFor([]model.Session{s}, 1200, []string{"edgeproxy", "витрины", "шлюза"})
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
		"- настроил внутренний контур до биллинга 10.10.0.10\n" +
		"- проверил публичный edgeproxy grpc через витрину\n"
	s := model.Session{
		Harness: "claude",
		ID:      "compacted-sess",
		Messages: []model.Message{
			{Role: "user", Text: compaction},
			{Role: "assistant", Text: "Продолжаем работу."},
		},
	}
	if SpeechCarriesTermsTogether(s, []string{"биллинга", "grpc"}, 2) {
		t.Fatal("expected SpeechCarriesTermsTogether not to match terms across separate bullets of a compaction summary")
	}
}

func TestLeadWithConclusionPrefersUserCorrection(t *testing.T) {
	s1 := model.Session{
		ID: "ordinary-concluded",
		Messages: []model.Message{
			{Role: "user", Text: "проверь шлюз и витрину"},
			{Role: "assistant", Text: "Причина: маршрут через шлюз до витрины работает штатно."},
		},
	}
	s2 := model.Session{
		ID: "user-disambiguation",
		Messages: []model.Message{
			{Role: "user", Text: "не путай, у меня два разных шлюза: edgeproxy идет через шлюз до витрины, а внутренний для доступа к биллингу"},
			{Role: "assistant", Text: "Понял, зафиксировал."},
		},
	}
	got := LeadWithConclusion([]model.Session{s1, s2}, []string{"edgeproxy", "шлюз", "витрина", "биллинг"})
	if got[0].ID != "user-disambiguation" {
		t.Fatalf("expected LeadWithConclusion to lead with user disambiguation session, got %s first", got[0].ID)
	}
}
