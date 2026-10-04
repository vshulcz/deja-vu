package index

import "testing"

// A bot token names its service exactly, so `deja secrets` lists it rather
// than folding it into the counted rules at the bottom of the screen.
func TestATelegramBotTokenIsListedByName(t *testing.T) {
	if !SecretKindNamed("telegram-bot-token") {
		t.Fatal("telegram-bot-token is counted, not listed")
	}
	got := markerKinds("BOT_TOKEN=[redacted:telegram-bot-token]")
	if len(got) != 1 || got[0] != "telegram-bot-token" {
		t.Fatalf("markerKinds = %v", got)
	}
}
