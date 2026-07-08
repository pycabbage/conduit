//go:build js

package main

import (
	"encoding/json"
	"sync"
	"syscall/js"

	"github.com/pycabbage/conduit/internal/jsonc"
	"github.com/pycabbage/conduit/internal/relay"
)

// WebAssembly entrypoint for conduit, built with GOOS=js GOARCH=wasm. See
// docs/adr/0007-wasm-lifecycle-without-signals.md and
// docs/adr/0006-relay-wasm-cmd-layout.md.

var (
	mgrMu sync.Mutex
	mgr   *relay.Manager
)

func parseConfigs(configJSONC string) ([]relay.BotConfig, error) {
	var cfgs []relay.BotConfig
	if err := json.Unmarshal(jsonc.ToJSON([]byte(configJSONC)), &cfgs); err != nil {
		return nil, err
	}
	return cfgs, nil
}

var startFunc = js.FuncOf(func(_ js.Value, args []js.Value) any {
	configStr := args[0].String()
	resolve, reject := args[1], args[2]

	go func() {
		cfgs, err := parseConfigs(configStr)
		if err != nil {
			reject.Invoke(err.Error())
			return
		}

		mgrMu.Lock()
		prev := mgr
		mgr = relay.NewManager()
		next := mgr
		mgrMu.Unlock()

		if prev != nil {
			prev.StopAll()
		}
		next.Apply(cfgs)
		resolve.Invoke(js.Undefined())
	}()
	return nil
})

var reloadFunc = js.FuncOf(func(_ js.Value, args []js.Value) any {
	configStr := args[0].String()
	resolve, reject := args[1], args[2]

	go func() {
		mgrMu.Lock()
		m := mgr
		mgrMu.Unlock()
		if m == nil {
			reject.Invoke("conduit: reload called before start")
			return
		}

		cfgs, err := parseConfigs(configStr)
		if err != nil {
			reject.Invoke(err.Error())
			return
		}
		m.Apply(cfgs)
		resolve.Invoke(js.Undefined())
	}()
	return nil
})

var stopFunc = js.FuncOf(func(_ js.Value, args []js.Value) any {
	resolve := args[0]

	go func() {
		mgrMu.Lock()
		m := mgr
		mgrMu.Unlock()
		if m != nil {
			m.StopAll()
		}
		resolve.Invoke(js.Undefined())
	}()
	return nil
})

func main() {
	js.Global().Set("start", startFunc)
	js.Global().Set("reload", reloadFunc)
	js.Global().Set("stop", stopFunc)
	js.Global().Set("__conduitReady", js.ValueOf(true))

	select {}
}
