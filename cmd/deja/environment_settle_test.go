package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/policy"
)

const settleWallLine = "zsh:1: command not found: shellcheck"

// settleStore is a store with one wall: three sessions in three projects,
// each hitting it ago days back. It returns the index dir and a function that
// adds a session to proj0 at the given age, hitting the wall or not, and
// re-indexes.
func settleStore(t *testing.T, ago time.Duration) (string, func(name string, age time.Duration, hit bool)) {
	t.Helper()
	tmp := hermeticEnv(t)
	t.Setenv("HERMES_HOME", filepath.Join(tmp, "hermes"))
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	dir := index.DefaultDir()
	write := func(name string, project int, age time.Duration, hit bool) {
		t.Helper()
		root := filepath.Join(claude, fmt.Sprintf("proj-s%d", project))
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-age).UTC()
		output := "ok 12 tests passed"
		if hit {
			output = settleWallLine
		}
		cwd := fmt.Sprintf("/s/proj%d", project)
		line := func(ts time.Time, content string) string {
			return `{"type":"user","sessionId":"` + name + `","cwd":"` + cwd + `","timestamp":"` +
				ts.Format(time.RFC3339) + `","message":{"role":"user","content":` + content + `}}` + "\n"
		}
		body := line(at, `"run the checks"`) +
			line(at.Add(5*time.Minute), `[{"type":"tool_result","content":"`+output+`"}]`)
		if err := os.WriteFile(filepath.Join(root, name+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for i := range environmentMinProjects {
		write(fmt.Sprintf("s%02d", i), i, ago+time.Duration(i)*time.Hour, true)
	}
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir, func(name string, age time.Duration, hit bool) {
		t.Helper()
		write(name, 0, age, hit)
		if err := index.Ensure(dir, "", true, nil); err != nil {
			t.Fatal(err)
		}
	}
}

func blockNamesWall(dir string) bool {
	return strings.Contains(environmentBlock(dir, policy.ActivationAuto), "command not found: shellcheck")
}

// frictionWallID reads the id `deja friction --json` gives the one wall.
func frictionWallID(t *testing.T, dir string) (string, string) {
	t.Helper()
	var out bytes.Buffer
	if err := runFriction(dir, []string{"--json"}, &out); err != nil {
		t.Fatal(err)
	}
	var env struct {
		Rows []struct {
			Error   string `json:"error"`
			ID      string `json:"id"`
			Settled string `json:"settled"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	for _, r := range env.Rows {
		if strings.Contains(r.Error, "shellcheck") {
			return r.ID, r.Settled
		}
	}
	t.Fatalf("no shellcheck row:\n%s", out.String())
	return "", ""
}

// A wall whose cause was fixed kept printing in the session-start block
// forever (#5628). One that has not been hit for longer than the window, while
// its projects went on getting sessions, has stopped recurring.
func TestAWallThatStoppedRecurringLeavesTheBlock(t *testing.T) {
	dir, add := settleStore(t, 60*24*time.Hour)
	if !blockNamesWall(dir) {
		t.Fatal("the wall is not in the block to begin with")
	}
	add("later", 2*24*time.Hour, false)
	if blockNamesWall(dir) {
		t.Errorf("a wall last hit two months ago, with work since in its project, is still in the block:\n%s",
			environmentBlock(dir, policy.ActivationAuto))
	}
	// `deja friction` is history and keeps it.
	var out bytes.Buffer
	if err := runFriction(dir, nil, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "shellcheck") {
		t.Errorf("deja friction dropped an old wall:\n%s", out.String())
	}
}

// Nobody worked here for two months: that is not evidence the wall went away.
func TestAnOldWallOnAnIdleMachineStays(t *testing.T) {
	dir, _ := settleStore(t, 60*24*time.Hour)
	if !blockNamesWall(dir) {
		t.Errorf("an idle machine lost its wall:\n%s", environmentBlock(dir, policy.ActivationAuto))
	}
}

// A newer session that is still inside the window is not enough either.
func TestARecentWallStaysWithNewerSessions(t *testing.T) {
	dir, add := settleStore(t, 10*24*time.Hour)
	add("later", 2*24*time.Hour, false)
	if !blockNamesWall(dir) {
		t.Errorf("a wall hit ten days ago left the block:\n%s", environmentBlock(dir, policy.ActivationAuto))
	}
}

func TestSettleHidesAWallAndUnsettleBringsItBack(t *testing.T) {
	dir, _ := settleStore(t, 5*24*time.Hour)
	id, settled := frictionWallID(t, dir)
	if settled != "" || !validWallID(id) {
		t.Fatalf("id %q settled %q before any settle", id, settled)
	}
	var out bytes.Buffer
	if err := runFriction(dir, []string{"--settle", strings.ToUpper(id)}, &out); err != nil {
		t.Fatal(err)
	}
	if blockNamesWall(dir) {
		t.Errorf("a settled wall is still in the block:\n%s", environmentBlock(dir, policy.ActivationAuto))
	}
	if _, settled := frictionWallID(t, dir); settled == "" {
		t.Error("--json does not mark the wall settled")
	}
	out.Reset()
	if err := runFriction(dir, nil, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "settled") || !strings.Contains(out.String(), "id "+id) {
		t.Errorf("deja friction does not show the settle:\n%s", out.String())
	}

	out.Reset()
	if err := runFriction(dir, []string{"--unsettle", id}, &out); err != nil {
		t.Fatal(err)
	}
	if !blockNamesWall(dir) {
		t.Errorf("an unsettled wall did not come back:\n%s", environmentBlock(dir, policy.ActivationAuto))
	}
	if err := runFriction(dir, []string{"--unsettle", id}, &out); err == nil {
		t.Error("unsettling a wall that is not settled said nothing")
	}
}

// A settle means fixed as of then. A session started after it that hits the
// same wall says it was not.
func TestAHitAfterTheSettleBringsTheWallBack(t *testing.T) {
	dir, add := settleStore(t, 20*24*time.Hour)
	id, _ := frictionWallID(t, dir)
	if err := policy.SettleWall(id, time.Now().Add(-10*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if blockNamesWall(dir) {
		t.Fatal("the settle did not hide the wall")
	}
	add("again", 24*time.Hour, true)
	if !blockNamesWall(dir) {
		t.Errorf("a wall hit after its settle stayed hidden:\n%s", environmentBlock(dir, policy.ActivationAuto))
	}
	if _, settled := frictionWallID(t, dir); settled != "" {
		t.Errorf("--json still calls a wall hit since its settle settled at %s", settled)
	}
	var out bytes.Buffer
	if err := runFriction(dir, nil, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "hit again after it was settled") {
		t.Errorf("deja friction does not say the settle was overtaken:\n%s", out.String())
	}
}

// The settle is kept beside the policy file, not in the index, so a rebuild
// from scratch does not undo it.
func TestASettleSurvivesAnIndexRebuild(t *testing.T) {
	dir, _ := settleStore(t, 5*24*time.Hour)
	id, _ := frictionWallID(t, dir)
	var out bytes.Buffer
	if err := runFriction(dir, []string{"--settle", id}, &out); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if again, _ := frictionWallID(t, dir); again != id {
		t.Fatalf("the rebuild gave the wall id %s, was %s", again, id)
	}
	if blockNamesWall(dir) {
		t.Errorf("a rebuild brought a settled wall back:\n%s", environmentBlock(dir, policy.ActivationAuto))
	}
}

func TestSettleRefusesWhatItCannotName(t *testing.T) {
	dir, _ := settleStore(t, 5*24*time.Hour)
	var out bytes.Buffer
	for _, args := range [][]string{
		{"--settle"},
		{"--settle", "nothex!!"},
		{"--settle", "0000000a"},
		{"--settle", "0000000a", "--unsettle", "0000000b"},
	} {
		if err := runFriction(dir, args, &out); err == nil {
			t.Errorf("%v was accepted", args)
		}
	}
	if !blockNamesWall(dir) {
		t.Error("a refused settle hid the wall")
	}
}
