package relay

import (
	"context"
	"log"
	"sync"
)

// Manager tracks the set of currently-running bot relay goroutines, keyed by
// BotConfig.ID, and starts/stops them to match a desired configuration.
//
// Manager is safe for concurrent use.
type Manager struct {
	mu      sync.Mutex
	running map[string]context.CancelFunc
}

// NewManager returns an initialized Manager with no running bots.
func NewManager() *Manager {
	return &Manager{running: map[string]context.CancelFunc{}}
}

// Apply starts goroutines for newly-active bots and cancels goroutines for
// bots that are no longer active, based on cfgs (Status=="active" == desired).
// Bots whose configuration is unchanged and still active are left running
// untouched.
func (m *Manager) Apply(cfgs []BotConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()

	desired := map[string]BotConfig{}
	for _, c := range cfgs {
		if c.Status == "active" {
			desired[c.ID] = c
		}
	}
	for id, cancel := range m.running {
		if _, ok := desired[id]; !ok {
			cancel()
			delete(m.running, id)
			log.Printf("stopped bot %s", id)
		}
	}
	for id, cfg := range desired {
		if _, ok := m.running[id]; !ok {
			ctx, cancel := context.WithCancel(context.Background())
			m.running[id] = cancel
			log.Printf("starting bot %s", id)
			go botRun(ctx, cfg)
		}
	}
}

// StopAll cancels all running bot goroutines. It is safe to call multiple
// times and safe to call before any bots have ever been started.
func (m *Manager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, cancel := range m.running {
		cancel()
		log.Printf("stopped bot %s", id)
	}
	m.running = map[string]context.CancelFunc{}
}
