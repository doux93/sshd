package sshclient

import (
	"context"
	"strings"
	"testing"
)

func TestPersistentCommandsRejectUntrustedIdentities(t *testing.T) {
	c := &Client{}
	for _, id := range []string{"", "terminal", strings.Repeat("a", 31), strings.Repeat("a", 33), strings.Repeat("A", 32), "a;touch /tmp/unsafe", "$(id)", "../../default"} {
		if _, err := c.PreparePersistent(context.Background(), id, true, 80, 24); err == nil {
			t.Fatalf("accepted %q", id)
		}
		if err := c.TerminatePersistent(context.Background(), id); err == nil {
			t.Fatalf("accepted kill %q", id)
		}
	}
	prefix, err := tmuxPrefix(strings.Repeat("a", 32))
	if err != nil || !strings.Contains(prefix, "-L sshx-") || !strings.Contains(prefix, "-f /dev/null") {
		t.Fatal(prefix, err)
	}
}
