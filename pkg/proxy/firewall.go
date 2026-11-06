package proxy

import (
	"fmt"
	"log/slog"
	"regexp"
)

// initFirewall compiles regex patterns for performance during proxy startup.
func (p *AIProxy) initFirewall() error {
	p.firewallPatterns = make([]*regexp.Regexp, 0, len(p.cfg.FirewallDenyList))

	for _, pattern := range p.cfg.FirewallDenyList {
		// Prepend (?i) to make the match case-insensitive
		// This handles "System" vs "system" vs "SYSTEM" automatically.
		compileStr := "(?i)" + pattern

		re, err := regexp.Compile(compileStr)
		if err != nil {
			return fmt.Errorf("invalid firewall regex '%s': %w", pattern, err)
		}
		p.firewallPatterns = append(p.firewallPatterns, re)
	}

	if len(p.firewallPatterns) > 0 {
		slog.Info("[Firewall] Loaded regex patterns", "count", len(p.firewallPatterns))
	}
	return nil
}

// checkStaticFirewall performs fast regex matching on the raw text.
// Returns (blocked, reason).
//
// The reason is what the client sees. When block_message is configured it is
// returned verbatim, so an operator can localise it or replace wording that no
// longer matches their policy. The detail about which pattern matched stays in
// the logs, where it is useful for diagnosis and not for the caller.
func (p *AIProxy) checkStaticFirewall(text string) (bool, string) {
	if !p.cfg.FirewallEnabled || len(p.firewallPatterns) == 0 {
		return false, ""
	}

	for _, re := range p.firewallPatterns {
		// MatchString checks if the regex exists ANYWHERE in the text
		if re.MatchString(text) {
			if p.cfg.BlockMessage != "" {
				return true, p.cfg.BlockMessage
			}
			return true, fmt.Sprintf("Content blocked by policy (pattern match: %s)", re.String())
		}
	}
	return false, ""
}

// checkSemanticFirewall performs vector similarity check against forbidden concepts.
func (p *AIProxy) checkSemanticFirewall(vec []float32) (bool, string) {
	if !p.cfg.FirewallEnabled || p.cfg.FirewallIndex == "" {
		return false, ""
	}

	results, err := p.engine.VSearchWithScores(p.cfg.FirewallIndex, vec, 1, "", 0)
	if err != nil || len(results) == 0 {
		return false, ""
	}

	bestMatch := results[0]
	// Assuming Distance metric: Lower is closer (0 = identical)
	// If threshold is 0.25, anything below 0.25 (very similar) is blocked.
	// NOTE: Check your metric! If using Cosine Similarity (1=identical), logic is reversed.
	// Based on previous code, Engine returns Distance.

	if float32(bestMatch.Score) < p.cfg.FirewallThreshold {
		// The matched id and distance go to the log, not to the caller: they say
		// which forbidden topic triggered the block, which is diagnosis, not
		// something to hand back. The caller gets block_message when configured.
		slog.Warn("[Firewall] semantic match", "id", bestMatch.ID, "dist", bestMatch.Score)
		if p.cfg.BlockMessage != "" {
			return true, p.cfg.BlockMessage
		}
		return true, fmt.Sprintf("Content semantically similar to forbidden topic (id: %s, dist: %.4f)", bestMatch.ID, bestMatch.Score)
	}
	return false, ""
}
