package index

import "testing"

// One file name in five on a real store pooled several real files: 439 of 2169,
// worst `00-scroll-00.png` in 289 places, `service.go` in 7 packages,
// `kustomization.yaml` in 9 environments. The line before an edit reported that
// mixed history as the file's own.
func TestTwoFilesOfOneNameAreTwoFiles(t *testing.T) {
	apart := [][2]string{
		{"/work/app/cmd/dfbot/main.go", "/work/app/cmd/dfinvoice/main.go"},
		{"internal/handlers/service.go", "internal/billing/service.go"},
		{"deploy/prod/kustomization.yaml", "deploy/staging/kustomization.yaml"},
	}
	for _, c := range apart {
		if SameFile(c[0], c[1]) {
			t.Errorf("two different files read as one:\n  %s\n  %s", c[0], c[1])
		}
	}
}

// And every spelling of one file is still that file: the store holds what each
// harness recorded — absolute here, relative there, Windows separators from a
// synced machine.
func TestOneFileIsOneFileHoweverItWasSpelled(t *testing.T) {
	same := [][2]string{
		{"/work/app/internal/handlers/service.go", "internal/handlers/service.go"},
		{`C:\work\app\internal\handlers\service.go`, "internal/handlers/service.go"},
		{"./internal/handlers/service.go", "/work/app/internal/handlers/service.go"},
		// A harness that recorded the name and nothing else has only the name
		// to match on.
		{"service.go", "/work/app/internal/handlers/service.go"},
		{"/work/app/internal/handlers/service.go", "service.go"},
	}
	for _, c := range same {
		if !SameFile(c[0], c[1]) {
			t.Errorf("one file read as two:\n  %s\n  %s", c[0], c[1])
		}
	}
	if SameFile("internal/handlers/service.go", "internal/handlers/client.go") {
		t.Error("different names in one directory read as one file")
	}
}
