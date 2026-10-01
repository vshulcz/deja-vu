package main

import (
	"encoding/json"
	"testing"
)

// A config read and written back unchanged comes back byte for byte, whatever
// order the reader kept their keys in — including two array items with the
// same value in different orders, and keys whose names look like paths.
func TestTheReadersNestedKeyOrderSurvivesARewrite(t *testing.T) {
	for name, old := range map[string]string{
		"same value, two orders": `{
  "a": [
    {
      "x": 1,
      "y": 2
    }
  ],
  "b": [
    {
      "y": 2,
      "x": 1
    }
  ]
}`,
		"repeats in one array": `{
  "hooks": [
    {
      "type": "command",
      "command": "a"
    },
    {
      "command": "a",
      "type": "command"
    },
    {
      "type": "command",
      "command": "a"
    }
  ]
}`,
		"dotted keys": `{
  "a.b": {
    "y": 1,
    "x": 2
  },
  "a": {
    "b": {
      "x": 1,
      "y": 2
    }
  },
  "a/\"b\"": {
    "z": 1,
    "w": 2
  }
}`,
	} {
		var root map[string]any
		if err := json.Unmarshal([]byte(old), &root); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got, err := marshalConfigLike([]byte(old), root)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(got) != old {
			t.Errorf("%s: came back changed:\n%s\n--- want\n%s", name, got, old)
		}
	}
}
