package accountpool

import (
	"context"
	"fmt"

	"glm5.2proxy/internal/accounts"
	"glm5.2proxy/internal/config"
	"glm5.2proxy/internal/models"
	"glm5.2proxy/internal/quota"
	"glm5.2proxy/internal/upstream"
)

type Selection struct {
	Config       upstream.Config
	Account      *accounts.Account
	Balance      *quota.Balance
	Rotated      bool
	AllExhausted bool
	Warning      string
}

type PoolItem struct {
	accounts.PublicAccount
	Balance   *quota.Balance `json:"balance"`
	Available *int64         `json:"available"`
	Exhausted bool           `json:"exhausted"`
	Error     string         `json:"error"`
}

type Pool struct {
	cfg    config.Config
	store  *accounts.Store
	loader *upstream.Loader
	quota  *quota.Service
}

func New(cfg config.Config, store *accounts.Store, loader *upstream.Loader, quotaService *quota.Service) *Pool {
	return &Pool{cfg: cfg, store: store, loader: loader, quota: quotaService}
}

func (p *Pool) Select(ctx context.Context, model models.Model) Selection {
	fallback := p.loader.Load(nil)
	if !p.cfg.AccountRotation || p.cfg.Authorization != "" {
		return Selection{Config: fallback}
	}
	ordered := p.ordered()
	if len(ordered) == 0 {
		return Selection{Config: fallback}
	}
	type inspected struct {
		account   accounts.Account
		config    upstream.Config
		balance   *quota.Balance
		exhausted bool
		err       error
	}
	results := make([]inspected, 0, len(ordered))
	for _, account := range ordered {
		upstreamConfig := p.loader.Load(&account)
		balance, err := p.quota.ModelBalance(ctx, upstreamConfig, model)
		exhausted := balance != nil && balance.Available != nil && *balance.Available < p.cfg.AccountMinAvailable
		result := inspected{account: account, config: upstreamConfig, balance: balance, exhausted: exhausted, err: err}
		results = append(results, result)
		if err == nil && !exhausted {
			active := p.store.Active()
			previous := ""
			if active != nil {
				previous = active.ID
			}
			rotated := previous != account.ID
			if rotated {
				_, _ = p.store.Activate(account.ID)
			}
			return Selection{Config: upstreamConfig, Account: &account, Balance: balance, Rotated: rotated}
		}
	}
	for _, result := range results {
		if result.err != nil {
			_, _ = p.store.Activate(result.account.ID)
			return Selection{Config: result.config, Account: &result.account, Warning: fmt.Sprintf("quota unavailable for account %s: %v", result.account.ID, result.err)}
		}
	}
	return Selection{Config: results[0].config, Account: &results[0].account, AllExhausted: true}
}

func (p *Pool) Snapshot(ctx context.Context, model models.Model) map[string]any {
	active := p.store.Active()
	activeID := ""
	if active != nil {
		activeID = active.ID
	}
	storedAccounts := p.store.Accounts()
	items := make([]PoolItem, 0, len(storedAccounts))
	for index, account := range storedAccounts {
		public := accounts.Sanitize(account)
		public.QueuePosition = index + 1
		public.Active = account.ID == activeID
		balance, err := p.quota.ModelBalance(ctx, p.loader.Load(&account), model)
		item := PoolItem{PublicAccount: public, Balance: balance}
		if balance != nil {
			item.Available = balance.Available
			item.Exhausted = balance.Available != nil && *balance.Available < p.cfg.AccountMinAvailable
		}
		if err != nil {
			item.Error = err.Error()
		}
		items = append(items, item)
	}
	return map[string]any{"object": "zcode.account_pool", "model": model.ID, "rotationEnabled": p.cfg.AccountRotation, "minimumAvailableUnits": p.cfg.AccountMinAvailable, "activeAccountId": activeID, "accounts": items}
}

func (p *Pool) ordered() []accounts.Account {
	storedAccounts := p.store.Accounts()
	active := p.store.Active()
	if active == nil {
		return storedAccounts
	}
	index := -1
	for current, account := range storedAccounts {
		if account.ID == active.ID {
			index = current
			break
		}
	}
	if index <= 0 {
		return storedAccounts
	}
	return append(append([]accounts.Account{}, storedAccounts[index:]...), storedAccounts[:index]...)
}
