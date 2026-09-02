package main

import (
	"strings"
	"testing"
)

// `/deja` took no argument. Typing `/deja pgbouncer timeout` answered "Recipe
// /deja: Unexpected positional argument: pgbouncer" — goose does not map
// positionals onto a recipe, so the only working form is a named parameter,
// and a recipe that declares one has to use it or goose refuses the whole
// recipe with "Unnecessary parameter definitions".
//
// Measured against goose 1.48 with a stub endpoint: with the parameter declared
// and referenced, `/deja --query "pgbouncer timeout"` puts the query in front
// of the model, and bare `/deja` still carries the instructions.
func TestTheGooseRecipeTakesAQuery(t *testing.T) {
	r := gooseRecipe("/usr/local/bin/deja")

	if !strings.Contains(r, "key: query") {
		t.Errorf("no query parameter is declared:\n%s", r)
	}
	// Declared and used, or goose rejects the recipe outright.
	if !strings.Contains(r, "{{ query }}") {
		t.Errorf("the parameter is declared and never used:\n%s", r)
	}
	// Optional, or the bare `/deja` a reader types first stops working.
	if !strings.Contains(r, "requirement: optional") {
		t.Errorf("the parameter is not optional:\n%s", r)
	}
	// And the instructions are still there: the query is what to look for, the
	// instructions are how to look.
	if !strings.Contains(r, "instructions: |") {
		t.Errorf("the recipe lost its instructions:\n%s", r)
	}
}

// The parameter block sits after the instructions, not inside them: indented
// under `instructions: |` it would be part of the prose the model reads rather
// than something goose parses.
func TestTheGooseRecipeParameterIsNotSwallowedByTheInstructions(t *testing.T) {
	r := gooseRecipe("/usr/local/bin/deja")
	params := strings.Index(r, "parameters:")
	if params < 0 {
		t.Fatalf("no parameters block:\n%s", r)
	}
	line := r[params:]
	if strings.HasPrefix(line, "  parameters:") {
		t.Errorf("the parameters block is indented into the instructions:\n%s", r)
	}
	for _, l := range strings.Split(r, "\n") {
		if strings.TrimSpace(l) == "parameters:" && l != "parameters:" {
			t.Errorf("parameters is written at %q, want the first column", l)
		}
	}
}
