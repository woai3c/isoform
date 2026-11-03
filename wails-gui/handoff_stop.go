package main

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"wails-gui/internal/handoff"
	"yansu-agent/agent"
	"yansu-agent/agent/native"
)

// handoffStopProof is what the computer can prove about its AI when a run
// ends with an error.
type handoffStopProof struct {
	// NoAIAtStart is the start-time check's answer: no AI was configured or
	// signed in when the attempt started.
	NoAIAtStart bool
	LoopGuard   bool
	// AuthFailed is set when the run failed with an HTTP 401 or 403 from the
	// provider.
	AuthFailed bool
	// External is set when the AI is Claude Code or Codex; SignedIn and
	// SignInErr are that CLI's own auth status.
	External  bool
	SignedIn  bool
	SignInErr error
	Assistant string
}

// handoffStopCause names why a run ended with an error. No AI comes only from
// the start-time check, signed out only from a structured auth failure (a 401
// or 403 from the provider, or the CLI's own not-logged-in status), never from
// an error's words; anything else is the assistant's error.
func handoffStopCause(proof handoffStopProof) string {
	switch {
	case proof.LoopGuard:
		return handoff.CauseLoopGuard
	case proof.NoAIAtStart:
		return handoff.CauseNoAssistant
	case proof.AuthFailed:
		return handoff.CauseSignedOut
	case proof.External && proof.SignInErr == nil && !proof.SignedIn:
		return handoff.CauseSignedOut
	}
	return handoff.CauseAssistantError
}

// handoffRunAuthFailed reports a provider's HTTP 401 or 403 in a run's error.
func handoffRunAuthFailed(err error) bool {
	var httpErr *native.HTTPError
	if !errors.As(err, &httpErr) || httpErr == nil {
		return false
	}
	return httpErr.Status == http.StatusUnauthorized || httpErr.Status == http.StatusForbidden
}

// handoffStartAIReady is the start-time check behind setup's "Signed in" and
// Settings' "Thinks with"; tests replace it.
var handoffStartAIReady = func(a *App) bool { return a.AIReady() }

// handoffStopProbe reads the CLI's own sign-in status; tests replace it so a
// plain go test never runs the user's installed claude or codex
// (testmain_cli_test.go).
var handoffStopProbe = probeHandoffStop

func probeHandoffStop(a *App) handoffStopProof {
	cfg := loadAIProviderConfig()
	proof := handoffStopProof{Assistant: mobileTaskRunner(cfg.Provider)}
	if runner, ok := cfg.externalAgentRunnerConfig(); ok {
		proof.External = true
		proof.SignedIn, proof.SignInErr = agent.ExternalAgentSignedIn(runner)
	}
	return proof
}

// recordSystemStop marks an entry stopped by Isoform with a cause and adds the
// thread message that explains it.
func recordSystemStop(e *handoff.Entry, cause, assistant string, at int64) {
	e.StoppedBy = handoff.StoppedBySystem
	e.StopCause = cause
	e.StopAssistant = assistant
	e.StoppedAt = at
	code := handoff.EventCodeStopped
	if cause == handoff.CauseNoAssistant {
		code = handoff.EventCodeStoppedNoAssistant
	}
	params := map[string]string{"cause": cause}
	if assistant != "" {
		params["assistant"] = assistant
	}
	ensureThread(e).AppendEvent(handoff.Event{ID: uuid.NewString(), Kind: handoff.EventIsoform, At: at, Code: code, Params: params})
}

// recordPersonStop marks an entry stopped by the person, with how far it got.
func recordPersonStop(e *handoff.Entry, progress *TaskProgressView, at int64) {
	e.StoppedBy = handoff.StoppedByPerson
	e.StopCause, e.StopAssistant = "", ""
	e.StoppedAt = at
	outcome := &handoff.Outcome{Kind: handoff.OutcomeStopped, By: handoff.OutcomeByYou, At: at}
	if progress != nil && progress.Total > 0 {
		outcome.Params = map[string]string{"n": strconv.Itoa(progress.Done), "m": strconv.Itoa(progress.Total)}
	}
	ensureThread(e).Outcome = outcome
}

// handoffStopEventText words a stop or Start again thread message for the
// phone, in the Mac's language. The desktop words it from Code and Params.
func handoffStopEventText(event handoff.Event, lang string) string {
	zh := lang == "zh-CN"
	at := time.UnixMilli(event.At).Local()
	clock := at.Format("3:04 PM")
	if zh {
		clock = at.Format("15:04")
	}
	switch event.Code {
	case handoff.EventCodeStartedAgain:
		if zh {
			return "重新开始 · " + clock
		}
		return "Started again · " + clock
	case handoff.EventCodeStoppedNoAssistant:
		if zh {
			return "我" + clock + "开始了，但这台电脑上还没有登录 AI，所以什么都没做。设置好以后再重新开始。"
		}
		return "I started at " + clock + ", but no AI is signed in on this computer, so nothing was done. Set one up, then start again."
	}
	ai := handoffAssistantName(event.Params["assistant"], zh)
	if event.Params["cause"] == handoff.CauseLoopGuard {
		if zh {
			return "我在" + clock + "停下了：尝试了太多次也没完成。已经完成的部分在下面。重新开始会从头再做一遍，所以如果它在别的应用里改过什么，请先看一下。"
		}
		return "I stopped at " + clock + ": it took too many attempts without finishing. What I'd finished is below. Starting again runs it from the start, so if it changed anything in another app, check that first."
	}
	if event.Params["cause"] == handoff.CauseSignedOut {
		// The copy has no signed-out cause; the thread says the row's line.
		if zh {
			return "没能完成：需要重新登录 " + ai + "。"
		}
		return "Couldn't finish: sign in to " + ai + " again."
	}
	var cause string
	switch event.Params["cause"] {
	case handoff.CauseRestarted:
		cause = map[bool]string{false: "Isoform restarted", true: "Isoform 重启了"}[zh]
	case handoff.CauseTimeout:
		cause = map[bool]string{false: "nothing happened for two hours", true: "两小时没有任何进展"}[zh]
	default:
		cause = map[bool]string{false: ai + " stopped with an error", true: ai + " 出错停止了"}[zh]
	}
	if zh {
		return "我在" + clock + "停下了，因为" + cause + "。已经完成的部分在下面。"
	}
	return "I stopped at " + clock + " because " + cause + ". What I'd finished is below."
}

func handoffAssistantName(runner string, zh bool) string {
	switch runner {
	case "claude_code":
		return "Claude Code"
	case "codex":
		return "Codex"
	}
	if zh {
		return "你的 AI"
	}
	return "your AI"
}

func mobileStopEventParams(params map[string]string) map[string]string {
	out := map[string]string{}
	for _, key := range []string{"cause", "assistant"} {
		if value := params[key]; value != "" {
			out[key] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
