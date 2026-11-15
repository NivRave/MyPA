package orchestrator

import (
	"context"
	"log/slog"
	"time"
)

// stickyDomainsTTL bounds how long a turn's domains carry over to a follow-up message.
const stickyDomainsTTL = 15 * time.Minute

// loadStickyDomains returns the domains the user's previous turn used (best effort: errors
// only degrade tool selection, never fail the turn).
func (e *Engine) loadStickyDomains(ctx context.Context, userID string) []DomainGroup {
	if e.store == nil || userID == "" {
		return nil
	}
	raw, err := e.store.GetTurnDomains(ctx, userID)
	if err != nil {
		slog.Warn("sticky domains: load failed", "user_id", userID, "error", err)
		return nil
	}
	out := make([]DomainGroup, 0, len(raw))
	for _, d := range raw {
		out = append(out, DomainGroup(d))
	}
	return out
}

// saveStickyDomains records what THIS turn used: domains matched by its text plus domains of
// tools it actually called. Inherited sticky domains are deliberately not re-saved, so a domain
// carries over one follow-up and then decays unless it is used again.
func (e *Engine) saveStickyDomains(ctx context.Context, userID string, matched []DomainGroup, calledTools []string) {
	if e.store == nil || userID == "" || e.toolRegistry == nil {
		return
	}
	used := unionDomains(matched, e.toolRegistry.DomainsForTools(calledTools))
	names := make([]string, 0, len(used))
	for _, d := range used {
		names = append(names, string(d))
	}
	if err := e.store.SetTurnDomains(ctx, userID, names, stickyDomainsTTL); err != nil {
		slog.Warn("sticky domains: save failed", "user_id", userID, "error", err)
	}
}

func unionDomains(sets ...[]DomainGroup) []DomainGroup {
	var out []DomainGroup
	seen := map[DomainGroup]bool{}
	for _, set := range sets {
		for _, d := range set {
			if d != "" && !seen[d] {
				seen[d] = true
				out = append(out, d)
			}
		}
	}
	return out
}
