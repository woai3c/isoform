package main

import (
	"encoding/json"
	"sync"
	"time"

	"wails-gui/internal/proactivevoice"
)

// Presence is the one short line the phone shows under Chat's title while
// Isoform works on something (Phone Threads §5): "Writing the reply to
// Priya…". The Mac publishes it when work starts and every few seconds while
// it lasts, and clears it when it ends; the phone hides a line older than 10 s,
// so it never reads as a loading state.

const mobilePresenceRepeat = 4 * time.Second

type mobilePresenceText struct {
	EN string `json:"en"`
	ZH string `json:"zh-CN"`
}

type mobilePresenceEvent struct {
	Event string              `json:"event"`
	Text  *mobilePresenceText `json:"text"`
	At    string              `json:"at"`
}

type mobilePresenceState struct {
	mu      sync.Mutex
	current *mobilePresenceText
	work    int
	stop    chan struct{}
}

var mobilePresence mobilePresenceState

func mobilePresenceWriting(person string) mobilePresenceText {
	if person == "" {
		return mobilePresenceText{EN: "Writing a reply…", ZH: "正在写回复…"}
	}
	return mobilePresenceText{EN: "Writing the reply to " + person + "…", ZH: "正在写给 " + person + " 的回复…"}
}

func mobilePresenceReadingHistory(assistant string) mobilePresenceText {
	if assistant == "codex" {
		return mobilePresenceText{EN: "Reading your Codex history…", ZH: "正在读你的 Codex 记录…"}
	}
	return mobilePresenceText{EN: "Reading your Claude Code history…", ZH: "正在读你的 Claude Code 记录…"}
}

// beginPresence publishes the line and keeps it fresh until the returned end
// is called. Nested work keeps the newest line.
func (a *App) beginPresence(text mobilePresenceText) func() {
	if a == nil || a.mobileBridge == nil {
		return func() {}
	}
	bridge := a.mobileBridge
	p := &mobilePresence
	p.mu.Lock()
	p.current = &text
	p.work++
	if p.stop == nil {
		p.stop = make(chan struct{})
		go p.repeat(bridge, p.stop)
	}
	p.mu.Unlock()
	bridge.publishPresence(&text)
	var once sync.Once
	return func() {
		once.Do(func() {
			p.mu.Lock()
			p.work--
			done := p.work <= 0
			if done {
				p.work = 0
				p.current = nil
				if p.stop != nil {
					close(p.stop)
					p.stop = nil
				}
			}
			p.mu.Unlock()
			if done {
				bridge.publishPresence(nil)
			}
		})
	}
}

func (p *mobilePresenceState) repeat(bridge *mobileBridge, stop chan struct{}) {
	ticker := time.NewTicker(mobilePresenceRepeat)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			p.mu.Lock()
			current := p.current
			p.mu.Unlock()
			if current != nil {
				bridge.publishPresence(current)
			}
		}
	}
}

func (bridge *mobileBridge) publishPresence(text *mobilePresenceText) {
	payload, err := json.Marshal(mobilePresenceEvent{Event: "presence", Text: text, At: time.Now().UTC().Format(time.RFC3339)})
	if err != nil {
		return
	}
	bridge.publishEvent(mobileScopeChatRead, payload)
}

// proactiveVoicePresence names the reply being written, when the job is one.
func proactiveVoicePresence(job proactivevoice.Job) (mobilePresenceText, bool) {
	if !job.DraftAllowed {
		return mobilePresenceText{}, false
	}
	name := ""
	for _, item := range job.Items {
		if item.FromName != nil && *item.FromName != "" {
			name = *item.FromName
			break
		}
	}
	if fields := splitFirst(name); fields != "" {
		name = fields
	}
	return mobilePresenceWriting(name), true
}

func splitFirst(name string) string {
	for i, r := range name {
		if r == ' ' {
			return name[:i]
		}
	}
	return name
}
