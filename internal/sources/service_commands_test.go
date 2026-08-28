package sources

import "testing"

// "Start the thing that is not running" is the commonest answer to a refused
// connection, and it was the one remedy the command allowlist did not carry —
// while `brew install`, two words away, was carried (#2373).
func TestServiceCommandsAreIndexed(t *testing.T) {
	for _, cmd := range []string{
		"brew services start postgresql",
		"brew services restart redis",
		"systemctl restart postgresql",
		"sudo systemctl start nginx",
		"systemctl --user restart syncthing",
		"service postgresql restart",
		"launchctl load ~/Library/LaunchAgents/redis.plist",
		"launchctl kickstart -k gui/501/homebrew.mxcl.postgresql",
	} {
		if !worthIndexing(cmd) {
			t.Errorf("a remedy deja fix exists to name is not indexed: %q", cmd)
		}
	}
}

// The list stays an allowlist: navigation and one-shot surgery on a number that
// changes every run are not remedies another session can repeat.
func TestTheAllowlistStaysNarrow(t *testing.T) {
	for _, cmd := range []string{
		"kill -9 4711",
		"lsof -ti :5432",
		"cd services",
		"ls /etc/service",
		"cat service.log",
	} {
		if worthIndexing(cmd) {
			t.Errorf("the allowlist let in something that is not a remedy: %q", cmd)
		}
	}
}
