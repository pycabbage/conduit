// Package relay is the platform-independent core of conduit: the Discord
// Gateway <-> Worker relay loop and the Manager that runs it.
package relay

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"
)

type BotConfig struct {
	ID           string `json:"id"`
	Token        string `json:"token"`
	Status       string `json:"status"`
	Intents      int    `json:"intents"`
	WorkerWSURL  string `json:"worker_ws_url"`
	WorkerSecret string `json:"worker_secret"`
}

type sessInfo struct {
	id, resumeURL string
	seq           *int
}

func botRun(ctx context.Context, cfg BotConfig) {
	var sess sessInfo
	for {
		if err := runOnce(ctx, cfg, &sess); err != nil {
			log.Printf("bot %s: %v", cfg.ID, err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

func discordWrite(ctx context.Context, conn *websocket.Conn, mu *sync.Mutex, msg []byte) {
	mu.Lock()
	defer mu.Unlock()
	_ = conn.Write(ctx, websocket.MessageText, msg)
}

func runOnce(ctx context.Context, cfg BotConfig, sess *sessInfo) error {
	gwURL := "wss://gateway.discord.gg/?v=10&encoding=json"
	if sess.resumeURL != "" {
		gwURL = sess.resumeURL + "?v=10&encoding=json"
	}

	dc, _, err := websocket.Dial(ctx, gwURL, nil)
	if err != nil {
		return err
	}
	defer func() { _ = dc.Close(websocket.StatusNormalClosure, "") }()
	dc.SetReadLimit(1 << 20)

	wc, _, err := websocket.Dial(ctx, cfg.WorkerWSURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Authorization": []string{"Bearer " + cfg.WorkerSecret},
		},
	})
	if err != nil {
		return err
	}
	defer func() { _ = wc.Close(websocket.StatusNormalClosure, "") }()
	wc.SetReadLimit(1 << 20)

	initMsg, _ := json.Marshal(map[string]any{"type": "init", "token": cfg.Token})
	if err := wc.Write(ctx, websocket.MessageText, initMsg); err != nil {
		return err
	}

	var mu sync.Mutex
	errCh := make(chan error, 2)
	hbStop := make(chan struct{})
	defer close(hbStop)

	go func() { // Worker -> Discord
		for {
			_, msg, err := wc.Read(ctx)
			if err != nil {
				errCh <- err
				return
			}
			discordWrite(ctx, dc, &mu, msg)
		}
	}()

	seqJSON := func() string {
		if sess.seq == nil {
			return "null"
		}
		return strconv.Itoa(*sess.seq)
	}
	sendHB := func() { discordWrite(ctx, dc, &mu, []byte(`{"op":1,"d":`+seqJSON()+`}`)) }

	for {
		select {
		case err := <-errCh:
			return err
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		_, raw, err := dc.Read(ctx)
		if err != nil {
			return err
		}

		var frame struct {
			Op int             `json:"op"`
			D  json.RawMessage `json:"d"`
			S  *int            `json:"s"`
			T  *string         `json:"t"`
		}
		if json.Unmarshal(raw, &frame) != nil {
			continue
		}
		if frame.S != nil {
			sess.seq = frame.S
		}

		switch frame.Op {
		case 10: // Hello
			var hello struct {
				HeartbeatInterval int `json:"heartbeat_interval"`
			}
			if err := json.Unmarshal(frame.D, &hello); err != nil {
				return err
			}
			go func(interval time.Duration) {
				t := time.NewTicker(interval)
				defer t.Stop()
				for {
					select {
					case <-hbStop:
						return
					case <-ctx.Done():
						return
					case <-t.C:
						sendHB()
					}
				}
			}(time.Duration(hello.HeartbeatInterval) * time.Millisecond)

			if sess.id != "" {
				b, _ := json.Marshal(map[string]any{"op": 6, "d": map[string]any{
					"token": cfg.Token, "session_id": sess.id, "seq": sess.seq,
				}})
				log.Printf("bot %s: resuming session %s", cfg.ID, sess.id)
				discordWrite(ctx, dc, &mu, b)
			} else {
				b, _ := json.Marshal(map[string]any{"op": 2, "d": map[string]any{
					"token": cfg.Token, "intents": cfg.Intents,
					"properties": map[string]string{"os": "linux", "browser": "conduit", "device": "conduit"},
				}})
				log.Printf("bot %s: identifying", cfg.ID)
				discordWrite(ctx, dc, &mu, b)
			}

		case 0: // Dispatch
			if frame.T != nil && *frame.T == "READY" {
				var ready struct {
					SessionID string `json:"session_id"`
					ResumeURL string `json:"resume_gateway_url"`
				}
				if json.Unmarshal(frame.D, &ready) == nil {
					sess.id, sess.resumeURL = ready.SessionID, ready.ResumeURL
				}
			}
			if err := wc.Write(ctx, websocket.MessageText, raw); err != nil {
				return err
			}

		case 1: // Heartbeat request
			sendHB()

		case 7: // Reconnect
			return nil

		case 9: // Invalid Session
			*sess = sessInfo{}
			return nil
		}
	}
}
