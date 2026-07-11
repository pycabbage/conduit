//go:build core

package main

import "C"

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/pycabbage/conduit/internal/jsonc"
	"github.com/pycabbage/conduit/internal/relay"
)

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

func applyStart(configJSON string) error {
	cfgs, err := parseConfigs(configJSON)
	if err != nil {
		return err
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
	return nil
}

func applyReload(configJSON string) error {
	mgrMu.Lock()
	m := mgr
	mgrMu.Unlock()
	if m == nil {
		return fmt.Errorf("conduit: reload called before start")
	}

	cfgs, err := parseConfigs(configJSON)
	if err != nil {
		return err
	}
	m.Apply(cfgs)
	return nil
}

//export ConduitStart
func ConduitStart(configJSON *C.char) *C.char {
	if err := applyStart(C.GoString(configJSON)); err != nil {
		return C.CString(err.Error())
	}
	return nil
}

//export ConduitStop
func ConduitStop() {
	mgrMu.Lock()
	m := mgr
	mgrMu.Unlock()
	if m != nil {
		m.StopAll()
	}
}

//export ConduitReload
func ConduitReload(configJSON *C.char) *C.char {
	if err := applyReload(C.GoString(configJSON)); err != nil {
		return C.CString(err.Error())
	}
	return nil
}

func main() {}
