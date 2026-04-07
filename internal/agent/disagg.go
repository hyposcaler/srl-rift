package agent

import (
	"github.com/hyposcaler/srl-rift/internal/encoding"
	"github.com/hyposcaler/srl-rift/internal/spf"
)

// runDisaggregation computes positive disaggregation prefixes and sends
// them to the flood engine for TIE origination or withdrawal.
func (a *Agent) runDisaggregation() {
	entries := a.floodEngine.LSDB().Snapshot()
	adjs := a.floodEngine.Adjacencies()
	southRIB := a.spfEngine.SouthRIB()
	if southRIB == nil {
		return
	}

	disaggPrefixes := spf.ComputeDisaggregation(
		a.cfg.SystemID, a.cfg.Level, adjs, entries, southRIB, a.logger)

	// Convert to encoding.PrefixEntry for the flood engine.
	var prefixEntries []encoding.PrefixEntry
	for _, dp := range disaggPrefixes {
		prefixEntries = append(prefixEntries, encoding.PrefixEntry{
			Prefix:     dp.Prefix,
			Attributes: encoding.PrefixAttributes{Metric: dp.Distance},
		})
	}

	a.lastDisaggPrefixes = prefixEntries

	// Non-blocking send: if the flood engine hasn't consumed the last update,
	// drain it and send the new one.
	select {
	case <-a.floodEngine.DisaggUpdateCh:
	default:
	}
	a.floodEngine.DisaggUpdateCh <- prefixEntries
}
