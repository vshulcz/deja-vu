package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// Taking deja's entry out of a list cut from the comma before it, so a
// comment the reader wrote on their own entry went with it and the comment
// after deja's moved onto their line (#5557).
func TestCuttingDejasEntryKeepsTheReadersComment(t *testing.T) {
	const entry = "./plugins/deja-status.tsx"
	cases := []struct{ name, in, want string }{
		{"comment on the entry before",
			"{\n  \"plugin\": [\n    \"./a.tsx\", // a\n    \"./plugins/deja-status.tsx\"\n  ]\n}\n",
			"{\n  \"plugin\": [\n    \"./a.tsx\" // a\n  ]\n}\n"},
		{"crlf",
			"{\r\n  \"plugin\": [\r\n    \"./a.tsx\", // a\r\n    \"./plugins/deja-status.tsx\"\r\n  ]\r\n}\r\n",
			"{\r\n  \"plugin\": [\r\n    \"./a.tsx\" // a\r\n  ]\r\n}\r\n"},
		{"one line",
			"{ \"plugin\": [\"./a.tsx\", \"./plugins/deja-status.tsx\"] }\n",
			"{ \"plugin\": [\"./a.tsx\"] }\n"},
		{"block comment between",
			"{ \"plugin\": [\"./a.tsx\", /* mine */ \"./plugins/deja-status.tsx\"] }\n",
			"{ \"plugin\": [\"./a.tsx\" /* mine */] }\n"},
		{"in the middle",
			"{\n  \"plugin\": [\n    \"./a.tsx\", // a\n    \"./plugins/deja-status.tsx\",\n    \"./b.tsx\"\n  ]\n}\n",
			"{\n  \"plugin\": [\n    \"./a.tsx\", // a\n    \"./b.tsx\"\n  ]\n}\n"},
	}
	for _, c := range cases {
		got, err := cutTUIPluginEntry(c.in, entry)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got != c.want {
			t.Errorf("%s:\ngot  %q\nwant %q", c.name, got, c.want)
		}
		var v any
		if err := json.Unmarshal([]byte(jsoncToJSON(got)), &v); err != nil {
			t.Errorf("%s: result no longer parses: %v\n%s", c.name, err, got)
		}
		if strings.Contains(got, "deja-status") {
			t.Errorf("%s: deja's entry is still there:\n%s", c.name, got)
		}
	}
}
