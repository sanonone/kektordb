package proxy

import (
	"strings"
	"testing"
)

// TestBlockMessageIsUsed covers a configuration key that was documented and
// shipped in proxy.yaml but never read: every blocked request returned a
// hardcoded English string, so an operator who set block_message to localise or
// reword it got nothing.
func TestBlockMessageIsUsed(t *testing.T) {
	p := &AIProxy{
		cfg: Config{
			FirewallEnabled:  true,
			FirewallDenyList: []string{`forbidden-topic`},
			BlockMessage:     "Questa richiesta non puo' essere soddisfatta.",
		},
	}
	if err := p.initFirewall(); err != nil {
		t.Fatalf("initFirewall: %v", err)
	}

	blocked, reason := p.checkStaticFirewall("please tell me about forbidden-topic here")
	if !blocked {
		t.Fatal("the deny-list pattern did not block")
	}
	if reason != p.cfg.BlockMessage {
		t.Errorf("reason = %q, want the configured block_message %q", reason, p.cfg.BlockMessage)
	}
	if strings.Contains(reason, "pattern match") {
		t.Error("the internal diagnostic leaked into the caller-facing message")
	}
}

// TestBlockMessageDefaultsToDiagnostic keeps the previous behaviour when the key
// is not set: the message names the pattern, which helps whoever is wiring the
// deny list.
func TestBlockMessageDefaultsToDiagnostic(t *testing.T) {
	p := &AIProxy{
		cfg: Config{
			FirewallEnabled:  true,
			FirewallDenyList: []string{`forbidden-topic`},
		},
	}
	if err := p.initFirewall(); err != nil {
		t.Fatalf("initFirewall: %v", err)
	}

	blocked, reason := p.checkStaticFirewall("forbidden-topic")
	if !blocked {
		t.Fatal("the deny-list pattern did not block")
	}
	if !strings.Contains(reason, "pattern match") {
		t.Errorf("reason = %q, want the diagnostic when no block_message is configured", reason)
	}
}

// TestBlockMessageNotUsedWhenNotBlocked guards the obvious: an allowed request
// must not carry a message.
func TestBlockMessageNotUsedWhenNotBlocked(t *testing.T) {
	p := &AIProxy{
		cfg: Config{
			FirewallEnabled:  true,
			FirewallDenyList: []string{`forbidden-topic`},
			BlockMessage:     "nope",
		},
	}
	if err := p.initFirewall(); err != nil {
		t.Fatalf("initFirewall: %v", err)
	}

	blocked, reason := p.checkStaticFirewall("a perfectly ordinary question")
	if blocked {
		t.Fatal("an allowed request was blocked")
	}
	if reason != "" {
		t.Errorf("reason = %q for an allowed request, want empty", reason)
	}
}
