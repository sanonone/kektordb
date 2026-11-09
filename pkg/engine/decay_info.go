package engine

import (
	"fmt"
	"math"
	"time"

	"github.com/sanonone/kektordb/pkg/core/hnsw"
)

// DecayInfo describes the decay applied to one memory, with the values that
// produced it.
//
// It exists so a caller can explain a ranking rather than assert one. The search
// paths already compute this; without a way to read it back, any tool that shows
// decay would have to recompute it and would drift from the real ranking.
type DecayInfo struct {
	// Factor is the multiplier applied to the similarity score, in [0, 1].
	Factor float64 `json:"factor"`
	// Model is the decay algorithm used: exponential, linear, step, ebbinghaus.
	Model string `json:"model"`
	// HalfLifeHours is the layer's configured half-life.
	HalfLifeHours float64 `json:"half_life_hours"`
	// EffectiveHalfLifeHours accounts for reinforcement. For models that ignore
	// access count it equals HalfLifeHours.
	EffectiveHalfLifeHours float64 `json:"effective_half_life_hours"`
	// AccessCount is how many times the memory was reinforced.
	AccessCount int `json:"access_count"`
	// AgeDays is how long ago the reference point is (creation or last access).
	AgeDays float64 `json:"age_days"`
	// Layer is the memory layer that supplied the half-life.
	Layer string `json:"layer"`
	// Explanation is a sentence describing why the factor is what it is.
	Explanation string `json:"explanation"`
}

// decayParams resolves the decay configuration that applies to one memory.
//
// The search paths resolve this inline from the memory's metadata; keeping the
// resolution here means a diagnostic view and an actual ranking cannot disagree.
type decayParams struct {
	layer          string
	model          string
	halfLifeSecond float64
	accessCount    int
	referenceTime  float64
}

// resolveDecay reads the metadata that determines how a memory decays.
//
// Returns ok=false when the memory has no reference time: without one there is
// nothing to measure age against, and the search paths skip the multiplier
// entirely.
func (e *Engine) resolveDecay(meta map[string]any, memCfg hnsw.MemoryConfig) (decayParams, bool) {
	var p decayParams

	p.referenceTime = 0
	if val, ok := meta["_created_at"]; ok {
		p.referenceTime = toFloat64(val)
	}
	// Last access overrides creation when it is newer: a memory that was used
	// recently should not decay as if it had been forgotten since creation.
	if val, ok := meta["_last_accessed"]; ok {
		if lastAccess := toFloat64(val); lastAccess > p.referenceTime {
			p.referenceTime = lastAccess
		}
	}
	if p.referenceTime <= 0 {
		return p, false
	}

	p.layer = "episodic"
	if val, ok := meta["memory_layer"]; ok {
		if s, ok := val.(string); ok && s != "" {
			p.layer = s
		}
	}

	// Half-life and model come from the layer, falling back to the global pair.
	p.halfLifeSecond = float64(time.Duration(memCfg.DecayHalfLife).Seconds())
	p.model = string(memCfg.DecayModel)
	if p.model == "" {
		p.model = "exponential"
	}
	if layerCfg, ok := memCfg.Layers[p.layer]; ok {
		p.halfLifeSecond = float64(time.Duration(layerCfg.DecayHalfLife).Seconds())
		if layerCfg.DecayModel != "" {
			p.model = string(layerCfg.DecayModel)
		}
	}
	// A per-memory override wins over the layer's choice.
	if override, ok := meta["_decay_model"].(string); ok && override != "" {
		p.model = override
	}

	if ac, ok := meta["_access_count"].(float64); ok {
		p.accessCount = int(ac)
	}
	return p, true
}

// DecayExplanation returns the decay currently applied to one memory.
//
// The factor is computed with the same function the search paths use, so the
// number a reader sees is the number that shaped the ranking.
func (e *Engine) DecayExplanation(indexName, id string) (DecayInfo, error) {
	data, err := e.DB.GetVector(indexName, id)
	if err != nil {
		return DecayInfo{}, err
	}

	// Start from the defaults: an index that was never given a memory config
	// carries a zero value, and a zero half-life would report "no decay" for
	// memories that the search path does decay using the defaults.
	memCfg := hnsw.DefaultMemoryConfig()
	if idx, ok := e.DB.GetVectorIndex(indexName); ok {
		if h, ok := idx.(*hnsw.Index); ok {
			if cfg := h.GetMemoryConfig(); cfg.Enabled || len(cfg.Layers) > 0 {
				memCfg = cfg
			}
		}
	}

	p, ok := e.resolveDecay(data.Metadata, memCfg)
	if !ok {
		return DecayInfo{
			Factor:      1.0,
			Model:       "none",
			Explanation: "no timestamp: decay does not apply",
		}, nil
	}

	factor := calculateTimeDecayModel(p.referenceTime, p.halfLifeSecond, p.model, p.accessCount)
	return buildDecayInfo(p, factor), nil
}

// buildDecayInfo assembles the explanation, computing the reinforced half-life
// from the same access count the factor used.
func buildDecayInfo(p decayParams, factor float64) DecayInfo {
	ageSeconds := float64(time.Now().Unix()) - p.referenceTime
	if ageSeconds < 0 {
		ageSeconds = 0
	}

	info := DecayInfo{
		Factor:        factor,
		Model:         p.model,
		HalfLifeHours: p.halfLifeSecond / 3600,
		AccessCount:   p.accessCount,
		AgeDays:       ageSeconds / 86400,
		Layer:         p.layer,
		Explanation:   "",
	}

	// Ebbinghaus extends stability with reinforcement; the other models ignore
	// the access count, so their effective half-life is the configured one.
	info.EffectiveHalfLifeHours = info.HalfLifeHours
	if p.model == "ebbinghaus" && p.accessCount > 0 && p.halfLifeSecond > 0 {
		// Same formula as calculateEbbinghausDecay: S = halfLife * (1 + ln(1+n)).
		stability := p.halfLifeSecond * (1 + math.Log1p(float64(p.accessCount)))
		info.EffectiveHalfLifeHours = stability / 3600
	}

	info.Explanation = explainDecay(info)
	return info
}

// explainDecay writes the sentence a reader needs to understand the number.
func explainDecay(info DecayInfo) string {
	if info.HalfLifeHours <= 0 {
		return "this layer does not decay"
	}

	switch info.Model {
	case "ebbinghaus":
		if info.AccessCount > 0 && info.EffectiveHalfLifeHours > info.HalfLifeHours {
			return fmt.Sprintf(
				"reinforced %d times: half-life extended from %.0fh to %.0fh",
				info.AccessCount, info.HalfLifeHours, info.EffectiveHalfLifeHours)
		}
		return fmt.Sprintf("ebbinghaus curve, half-life %.0fh, never reinforced",
			info.HalfLifeHours)
	case "step":
		if info.Factor >= 1.0 {
			return fmt.Sprintf("within the %.0fh window: no decay yet", info.HalfLifeHours)
		}
		return fmt.Sprintf("past the %.0fh window: fully decayed", info.HalfLifeHours)
	case "linear":
		return fmt.Sprintf("linear decay over %.0fh, %.1f days old",
			info.HalfLifeHours, info.AgeDays)
	default:
		return fmt.Sprintf("exponential decay, half-life %.0fh, %.1f days old",
			info.HalfLifeHours, info.AgeDays)
	}
}

// LastSnapshotTime reports when the last snapshot was written.
//
// Zero means no snapshot has happened in this process. lastSaveTime alone cannot
// answer that: it is seeded at Open so the auto-save throttle has a baseline.
func (e *Engine) LastSnapshotTime() time.Time {
	if e.snapshotCount.Load() == 0 {
		return time.Time{}
	}
	return time.Unix(0, e.lastSaveTime.Load())
}
