package storetest

import (
	"context"
	"testing"

	"crimsonledger/internal/store"
)

// Option tunes the suite for an implementation's capabilities.
type Option func(*config)

type config struct{ noPrune bool }

// NoPrune skips rule 11 for stores whose retention is done elsewhere (DynamoDB TTL).
func NoPrune() Option { return func(c *config) { c.noPrune = true } }

// Run executes the 13 contract rules against stores made by newStore. Each rule gets a fresh store.
func Run(t *testing.T, newStore func(t *testing.T, clock *FakeClock) store.Store, opts ...Option) {
	var cfg config
	for _, o := range opts {
		o(&cfg)
	}
	rules := []struct {
		name string
		run  func(*env)
	}{
		{"rule_01_duplicate_batch_is_ignored", rule01DuplicateBatch},
		{"rule_02_older_ping_never_moves_contact_back", rule02OutOfOrder},
		{"rule_03_fixes_keyed_and_trail_filters", rule03Fixes},
		{"rule_04_kills_keyed_and_bounty_ranking", rule04Kills},
		{"rule_05_chart_first_write_wins", rule05Charts},
		{"rule_06_search_word_prefix", rule06Search},
		{"rule_07_sweep_window_filters_order", rule07Sweep},
		{"rule_08_rulesets_isolated", rule08Rulesets},
		{"rule_09_version_counts_accepted_pings", rule09Version},
		{"rule_10_keys_round_trip", rule10Keys},
		{"rule_11_prune_old_data", rule11Prune},
		{"rule_12_cancelled_context_writes_nothing", rule12Cancel},
		{"rule_13_concurrent_equals_serial", rule13Concurrency},
	}
	for _, r := range rules {
		t.Run(r.name, func(t *testing.T) {
			if cfg.noPrune && r.name == "rule_11_prune_old_data" {
				t.Skip("store opted out of Prune")
			}
			clock := NewFakeClock()
			s := newStore(t, clock)
			t.Cleanup(func() { _ = s.Close() })
			fresh := func() store.Store {
				other := newStore(t, clock)
				t.Cleanup(func() { _ = other.Close() })
				return other
			}
			r.run(&env{t: t, s: s, clock: clock, ctx: context.Background(), fresh: fresh})
		})
	}
}
