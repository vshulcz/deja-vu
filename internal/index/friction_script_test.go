package index

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// The bound on what counts as a wall is about how much of a line a person can
// recognise, and it was counted in bytes — so a Russian error line was held to
// 60 characters and a Chinese one to 40, while English got 120. An
// 89-character Russian line was dropped where an 80-character English one was
// kept, and the wall a Russian-speaking user keeps hitting never reached
// `deja friction`, the environment block or `deja fix` (#1319).
func TestFrictionBoundsCountCharactersNotBytes(t *testing.T) {
	// Carries an English marker, as most real error lines do, so only the
	// length decides.
	for _, l := range []string{
		"ОШИБКА при развёртывании сервиса доставки заказов: connection refused при подключении к очереди повторов на резервном узле",
		"ОШИБКА при развёртывании сервиса доставки заказов и уведомлений: connection refused при подключении к очереди повторов узла",
	} {
		if n := utf8.RuneCountInString(l); n > frictionLineMax {
			t.Fatalf("the fixture is %d characters, past the bound itself", n)
		}
		if len(l) <= frictionLineMax {
			t.Fatalf("the fixture is %d bytes, inside the byte bound, so it proves nothing", len(l))
		}
		if _, ok := FrictionLine(l); !ok {
			t.Errorf("a %d-character line was dropped for being %d bytes: %.40s",
				utf8.RuneCountInString(l), len(l), l)
		}
	}
}

// The bounds still bound: a line too short to name anything, and one past the
// limit in characters, are both out.
func TestFrictionBoundsStillHold(t *testing.T) {
	if _, ok := FrictionLine("panic: x"); ok {
		t.Error("a line under the minimum was accepted")
	}
	long := "connection refused " + strings.Repeat("очень длинное описание ", 12)
	if n := utf8.RuneCountInString(long); n <= frictionLineMax {
		t.Fatalf("the fixture is only %d characters", n)
	}
	if _, ok := FrictionLine(long); ok {
		t.Error("a line past the maximum in characters was accepted")
	}
}

// And English is unchanged, since bytes and characters agree there.
func TestFrictionKeepsEnglishAsItWas(t *testing.T) {
	if _, ok := FrictionLine("ModuleNotFoundError: No module named yaml while building the image"); !ok {
		t.Error("an ordinary English error line stopped counting as a wall")
	}
	if _, ok := FrictionLine("connection refused " + strings.Repeat("x", 200)); ok {
		t.Error("a very long English line was accepted")
	}
}

// The gate decides what gets a friction signature at ingest, so a store built
// before this keeps fewer of them — the bump is what makes the one rebuild
// that re-derives them happen.
func TestTheFrictionBoundRidesOnTheFormatVersion(t *testing.T) {
	if version < 27 {
		t.Errorf("index version is %d — the friction gate changed without the bump that re-derives it", version)
	}
}
