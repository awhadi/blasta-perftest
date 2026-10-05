package ws

import (
	"testing"

	"github.com/awhadi/blasta-perftest/internal/version"
)

func TestHandshakeNamesBlastaUnlessTheJobSetsItsOwnAgent(t *testing.T) {
	if got := handshakeHeader(nil)["User-Agent"]; len(got) != 1 || got[0] != version.UserAgent() {
		t.Errorf("default agent = %v, want %q", got, version.UserAgent())
	}
	if got := handshakeHeader(map[string]string{"X-A": "1"})["User-Agent"]; len(got) != 1 || got[0] != version.UserAgent() {
		t.Errorf("other headers must not remove the default agent: %v", got)
	}
	custom := handshakeHeader(map[string]string{"user-agent": "mine"})
	if len(custom) != 1 || custom["user-agent"][0] != "mine" {
		t.Errorf("a job's own agent must be kept as it is: %v", custom)
	}
}
