//go:build darwin

package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"wails-gui/storage"
)

const mainChatMailSendTimeout = 30 * time.Second

func (a *App) sendMainChatMail(ctx context.Context, send storage.OutboundSendRow) error {
	if !a.proactiveVoicePermission(ctx, mailPermissionKey) || !proactiveVoiceAppAllowed("com.apple.mail", "Mail") {
		return errMailNotAllowed
	}
	if !mainChatDraftSendable(&mainChatProactiveDraft{
		To: send.DraftTo, Subject: send.Subject, Text: send.Text, Channel: send.Channel,
		Recipient: send.Recipient, PrincipalID: send.PrincipalID, Revision: send.Revision,
	}) {
		return errors.New("invalid Mail draft")
	}
	pid := mailProcessID()
	if pid <= 0 || !mailRunning() {
		return errors.New("Mail must be running to send this draft")
	}
	if mailAutomationStatus(false) != mailAutomationAllowed {
		return errMailNotAllowed
	}
	// The process id and exact draft values travel as argv, never as script or
	// shell source. Mail is already running and send is the final Apple event.
	const script = `
function run(argv) {
    if (argv.length !== 4) throw new Error("draft arguments");
    const mail = Application(parseInt(argv[0], 10));
    const message = mail.OutgoingMessage({subject: argv[2], content: argv[3], visible: false});
    mail.outgoingMessages.push(message);
    message.toRecipients.push(mail.Recipient({address: argv[1]}));
    mail.send(message);
    return "accepted";
}`
	runCtx, cancel := context.WithTimeout(ctx, mainChatMailSendTimeout)
	defer cancel()
	out, err := exec.CommandContext(runCtx, mailOsascriptPath, "-l", "JavaScript", "-e", script, "--",
		fmt.Sprint(pid), send.DraftTo, send.Subject, send.Text).Output()
	if err == nil && strings.TrimSpace(string(out)) == "accepted" {
		return nil
	}
	stderr := ""
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		stderr = string(exitErr.Stderr)
	}
	return classifyMainChatMailSendError(ctx, runCtx, err, stderr)
}

func classifyMainChatMailSendError(ctx, runCtx context.Context, err error, stderr string) error {
	if ctx.Err() != nil || errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return proactiveSendUnknown(context.DeadlineExceeded)
	}
	if mailAutomationRefused(stderr) {
		return errMailNotAllowed
	}
	if mailQuit(stderr) {
		return errMailClosed
	}
	return proactiveSendUnknown(errors.New("Mail did not confirm whether it accepted the draft"))
}
