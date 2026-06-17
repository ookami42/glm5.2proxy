package api

import (
	"sync"

	"glm5.2proxy/internal/config"
)

type usageReserve struct {
	mu          sync.RWMutex
	byModel     map[string]int64
	startupFloor int64
}

func newUsageReserve(cfg config.Config) *usageReserve {
	floor := cfg.AccountMinAvailable
	if conservative := cfg.AccountMinAvailable * 5 / 2; conservative > floor {
		floor = conservative
	}
	return &usageReserve{byModel: map[string]int64{}, startupFloor: floor}
}

func (r *usageReserve) Observe(modelID string, delta int64) {
	if delta <= 0 || modelID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byModel[modelID] = delta
}

func (r *usageReserve) Minimum(modelID string, base int64) int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if observed, ok := r.byModel[modelID]; ok && observed > 0 {
		if observed > base {
			return observed
		}
		return base
	}
	if r.startupFloor > base {
		return r.startupFloor
	}
	return base
}
