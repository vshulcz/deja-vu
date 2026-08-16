package main

import (
	"strings"
	"testing"
)

// The stats page, the viewer and the card each carried their own pasted copy of
// the mark. These pin the generated markup that replaced them, so a later change
// to the sprite that reaches the assets but not these pages fails here.
//
// The rectangles are fewer than the hand-drawn copies held: those were one per
// row, and a stretch now merges downwards through the rows that repeat it. Same
// drawing, fewer edges — and an edge is where a renderer can leave a hairline of
// background once the mark is scaled.
const (
	handDrawnStill = `<path fill="#8787af" d="M4 0h1v1h-1ZM17 0h1v1h-1ZM3 1h3v1h-3ZM16 1h3v1h-3ZM3 2h4v1h-4ZM15 2h4v1h-4ZM3 3h5v1h-5ZM14 3h5v1h-5ZM3 4h16v1h-16ZM2 5h18v2h-18ZM2 7h3v3h-3ZM7 7h8v3h-8ZM17 7h3v3h-3ZM2 10h18v1h-18ZM2 11h8v1h-8ZM12 11h8v1h-8ZM2 12h18v1h-18ZM3 13h16v1h-16ZM4 14h14v1h-14ZM19 14h2v5h-2ZM5 15h12v4h-12ZM4 19h16v2h-16ZM5 21h4v1h-4ZM13 21h4v1h-4Z"/><path fill="#1c1c1c" d="M5 7h2v3h-2ZM15 7h2v3h-2Z"/><path fill="#ff8700" d="M10 11h2v1h-2Z"/>`

	// The still layer of the animated version: no eyes cut out of it, so a blink
	// does not punch two holes in the head, and no tail, since the tail hangs
	// clear of the body and each wag position draws its own.
	handDrawnAliveBody = `<path fill="#8787af" d="M4 0h1v1h-1ZM17 0h1v1h-1ZM3 1h3v1h-3ZM16 1h3v1h-3ZM3 2h4v1h-4ZM15 2h4v1h-4ZM3 3h5v1h-5ZM14 3h5v1h-5ZM3 4h16v1h-16ZM2 5h18v6h-18ZM2 11h8v1h-8ZM12 11h8v1h-8ZM2 12h18v1h-18ZM3 13h16v1h-16ZM4 14h14v1h-14ZM5 15h12v4h-12ZM4 19h14v2h-14ZM5 21h4v1h-4ZM13 21h4v1h-4Z"/><path fill="#ff8700" d="M10 11h2v1h-2Z"/>`
)

func TestStillMarkMatchesTheCopyItReplaced(t *testing.T) {
	if got := markStill(0, 0, 1); got != handDrawnStill {
		t.Errorf("the still mark changed shape:\n got %s\nwant %s", got, handDrawnStill)
	}
}

func TestAliveMarkKeepsTheBodyAndCarriesEveryFrame(t *testing.T) {
	got := markAlive(0, 0, 1)
	if !strings.HasPrefix(got, handDrawnAliveBody) {
		t.Errorf("the animated body changed shape:\n got %s\nwant prefix %s", got, handDrawnAliveBody)
	}
	for _, class := range []string{`class="t0"`, `class="t1"`, `class="t2"`,
		`class="eyes-open"`, `class="eyes-shut"`} {
		if !strings.Contains(got, class) {
			t.Errorf("animated mark is missing %s, so that frame never shows", class)
		}
	}
}

// Both pages must actually carry the mark: a placeholder that never got
// substituted leaves a literal {{MARK_...}} in the page, and the template parses
// happily either way.
func TestPagesCarryTheMarkRatherThanThePlaceholder(t *testing.T) {
	for name, src := range map[string]string{
		"viewer":     viewSource,
		"stats page": statsHTMLSource,
	} {
		if !strings.Contains(src, "{{MARK_") {
			t.Errorf("%s source lost its mark placeholder", name)
		}
	}
	var page strings.Builder
	if err := viewTemplate.Execute(&page, viewPage{}); err != nil {
		t.Fatalf("viewer: %v", err)
	}
	out := page.String()
	if strings.Contains(out, "{{MARK_") {
		t.Error("viewer rendered with the placeholder still in it")
	}
	if !strings.Contains(out, `fill="#8787af"`) {
		t.Error("viewer rendered without the mark")
	}
}
