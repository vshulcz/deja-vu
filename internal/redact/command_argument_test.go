package redact

import (
	"strings"
	"testing"
)

// A password handed to a program as an argument has no key word beside it, so
// every pattern here missed it: of twelve planted secret shapes, ten were
// redacted at ingest and `sshpass -p '…'` and `curl -u admin:…` reached
// `deja show`, `deja recall` and `deja sync export` in the clear.
func TestACredentialPassedAsAnArgumentIsRedacted(t *testing.T) {
	for _, tc := range []struct {
		name   string
		in     string
		secret string
		keep   string
	}{
		{"sshpass quoted", `sshpass -p 'ZZexampleSSHpassword' ssh deploy@host.example.com`, "ZZexampleSSHpassword", "sshpass"},
		{"sshpass bare", `sshpass -pZZexampleSSHpassword ssh deploy@host.example.com`, "ZZexampleSSHpassword", "sshpass"},
		{"sshpass after flags", `sshpass -e -p ZZexampleSSHpassword scp f host:/tmp`, "ZZexampleSSHpassword", "scp"},
		{"curl basic", `curl -u admin:ZZexampleBASICpass https://api.example.com`, "ZZexampleBASICpass", "admin"},
		{"long user flag", `curl --user admin:ZZexampleBASICpass https://api.example.com`, "ZZexampleBASICpass", "admin"},
		{"mysql attached", `mysql -h db.example.com -uroot -pZZexampleMYSQLpass app`, "ZZexampleMYSQLpass", "mysql"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, counts := Text(tc.in)
			if strings.Contains(out, tc.secret) {
				t.Errorf("the secret survived: %q", out)
			}
			if !strings.Contains(out, tc.keep) {
				t.Errorf("the line lost what makes it findable (%q): %q", tc.keep, out)
			}
			if len(counts) == 0 {
				t.Errorf("a redaction was made and counted nothing: %q", out)
			}
		})
	}
}

// And the shapes that look the same and are not secrets.
func TestArgumentRedactionLeavesTheseAlone(t *testing.T) {
	for _, tc := range []struct{ name, in string }{
		{"uid and gid", `docker run -u 1000:1000 alpine sh`},
		{"a shell reference", `sshpass -p "$DEPLOY_PASSWORD" ssh deploy@host.example.com`},
		{"ssh port", `ssh -p 2222 deploy@host.example.com`},
		{"git push upstream", `git push -u origin feature/retry-budget`},
		{"a user with no password", `curl -u admin https://api.example.com`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, _ := Text(tc.in)
			if out != tc.in {
				t.Errorf("redacted something that is not a secret:\n in:  %q\n out: %q", tc.in, out)
			}
		})
	}
}
