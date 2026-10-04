package redact

import (
	"strings"
	"testing"
)

// fakeTelegramToken builds a token of the real shape that belongs to no bot:
// a numeric id, a colon, and 35 characters opening with "A".
func fakeTelegramToken() (string, string) {
	tail := "A" + strings.Repeat("b1C2d3E4f5", 4)[:34]
	return "8123456789", tail
}

// A Telegram bot token has no provider prefix, and the colon inside it stops
// every assignment rule at the bot id. Two real tokens reached the index as
// written: one pasted after "вот токен", one assigned to a variable.
func TestATelegramBotTokenIsMaskedWhereverItSits(t *testing.T) {
	id, tail := fakeTelegramToken()
	tok := id + ":" + tail
	for _, tc := range []struct{ name, in, keep string }{
		{"pasted in prose", "вот токен " + tok + " для бота", "для бота"},
		{"env assignment", "TELEGRAM_BOT_TOKEN=" + tok, "TELEGRAM_BOT_TOKEN="},
		{"other name", "export NOTIFY_BOT=" + tok, "NOTIFY_BOT="},
		{"quoted json", `{"bot_token": "` + tok + `"}`, `"bot_token"`},
		{"api url", "curl https://api.telegram.org/bot" + tok + "/getMe", "/getMe"},
		{"alone on a line", "\n" + tok + "\n", ""},
	} {
		got, counts := Text(tc.in)
		if strings.Contains(got, tail) || strings.Contains(got, tail[:20]) {
			t.Errorf("%s: the token survived: %q", tc.name, got)
		}
		if counts["telegram-bot-token"] != 1 {
			t.Errorf("%s: counts = %v, want one telegram-bot-token", tc.name, counts)
		}
		if strings.Count(got, Marker) != 1 {
			t.Errorf("%s: masked more than once: %q", tc.name, got)
		}
		if !strings.Contains(got, tc.keep) {
			t.Errorf("%s: lost the text around it, want %q in %q", tc.name, tc.keep, got)
		}
	}
}

// The shape is narrow on purpose. Ports, times, ratios and longer identifiers
// with a colon stay readable.
func TestTelegramRuleLeavesOtherColonsAlone(t *testing.T) {
	_, tail := fakeTelegramToken()
	for _, in := range []string{
		"listening on localhost:8080",
		"meeting at 12:00, ratio 16:9",
		"build 123456:Abc finished",
		"id 12345678:" + tail + "x is not a token",
		"12345678901234567890:" + tail,
		"case 100000: return Apply()",
	} {
		_, counts := Text(in)
		if counts["telegram-bot-token"] != 0 {
			t.Errorf("masked as a bot token: %q (%v)", in, counts)
		}
	}
}

// A marker's own colon is not a delimiter. A key word in front of one used to
// mask the marker again when its kind name was sixteen characters or longer.
func TestAMarkerAfterAKeyWordIsNotMaskedAgain(t *testing.T) {
	for _, in := range []string{
		"вот токен [redacted:telegram-bot-token] для бота",
		"ключ от хаба [redacted:huggingface-token]",
	} {
		got, _ := Text(in)
		if got != in {
			t.Errorf("marker rewritten: %q -> %q", in, got)
		}
	}
}

func TestTelegramBotTokenIsAKnownKind(t *testing.T) {
	if !IsKind("telegram-bot-token") {
		t.Fatal("telegram-bot-token is written but IsKind does not know it")
	}
}
