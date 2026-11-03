package sshconn

import (
	"fmt"
	"io"

	"golang.org/x/crypto/ssh"
)

// Shell wraps an SSH session with a remote PTY for interactive terminal use.
type Shell struct {
	session *ssh.Session
	stdin   io.WriteCloser
	stdout  io.Reader
	done    chan struct{}
}

// NewShell creates a new remote shell with a PTY allocated.
// If workDir is non-empty, the shell starts with a cd to that directory.
func NewShell(client *ssh.Client, workDir string, cols, rows int) (*Shell, error) {
	session, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("new session: %w", err)
	}

	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}

	if err := session.RequestPty("xterm-256color", rows, cols, modes); err != nil {
		session.Close()
		return nil, fmt.Errorf("request pty: %w", err)
	}

	stdin, err := session.StdinPipe()
	if err != nil {
		session.Close()
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}

	stdout, err := session.StdoutPipe()
	if err != nil {
		session.Close()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	// Merge stderr into stdout for the terminal.
	session.Stderr = session.Stdout

	if err := session.Shell(); err != nil {
		session.Close()
		return nil, fmt.Errorf("start shell: %w", err)
	}

	s := &Shell{
		session: session,
		stdin:   stdin,
		stdout:  stdout,
		done:    make(chan struct{}),
	}

	// Monitor session completion in the background.
	go func() {
		session.Wait()
		close(s.done)
	}()

	// Send initial cd command if workDir is provided.
	if workDir != "" {
		cmd := fmt.Sprintf("cd %q\n", workDir)
		if _, err := stdin.Write([]byte(cmd)); err != nil {
			s.Close()
			return nil, fmt.Errorf("cd to workdir: %w", err)
		}
	}

	return s, nil
}

// Read reads from the remote shell's stdout.
func (s *Shell) Read(p []byte) (int, error) {
	return s.stdout.Read(p)
}

// Write sends data to the remote shell's stdin.
func (s *Shell) Write(p []byte) (int, error) {
	return s.stdin.Write(p)
}

// Resize changes the PTY window size.
func (s *Shell) Resize(cols, rows int) error {
	return s.session.WindowChange(rows, cols)
}

// Close terminates the shell session.
func (s *Shell) Close() error {
	s.stdin.Close()
	return s.session.Close()
}

// Done returns a channel that is closed when the shell session ends.
func (s *Shell) Done() <-chan struct{} {
	return s.done
}
