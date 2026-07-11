package main

import (
	"encoding/json"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/pycabbage/conduit/internal/jsonc"
	"github.com/pycabbage/conduit/internal/relay"
)

func main() {
	configFile := os.Getenv("CONFIG_FILE")
	if configFile == "" {
		configFile = "/etc/conduit/config.json"
	}

	mgr := relay.NewManager()
	applyConfigs := func() {
		data, err := os.ReadFile(configFile)
		if err != nil {
			log.Printf("config read: %v", err)
			return
		}
		var cfgs []relay.BotConfig
		if err := json.Unmarshal(jsonc.ToJSON(data), &cfgs); err != nil {
			log.Printf("config parse: %v", err)
			return
		}
		mgr.Apply(cfgs)
	}

	applyConfigs()

	sighup := make(chan os.Signal, 1)
	signal.Notify(sighup, syscall.SIGHUP)
	sigterm := make(chan os.Signal, 1)
	signal.Notify(sigterm, syscall.SIGTERM, syscall.SIGINT)

	for {
		select {
		case <-sighup:
			log.Print("SIGHUP: reloading config")
			applyConfigs()
		case <-sigterm:
			mgr.StopAll()
			return
		}
	}
}
