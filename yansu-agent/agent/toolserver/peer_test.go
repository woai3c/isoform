//go:build linux || darwin

package toolserver

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestSocketRejectsWrongTokenAndUnrelatedPeer(t *testing.T) {
	for _, wrongPeer := range []bool{false, true} {
		t.Run(map[bool]string{false: "wrong token", true: "unrelated process group"}[wrongPeer], func(t *testing.T) {
			s, _ := newTestServer(t, nil)
			token := s.token + "wrong"
			if wrongPeer {
				executable, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				cmd := exec.Command(executable, "-test.run=^TestPeerWaitProcess$")
				cmd.Env = append(os.Environ(), "YANSU_TEST_PEER_WAIT=1")
				cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
				s.assistantPID.Store(int64(cmd.Process.Pid))
				token = s.token
			}
			conn, err := net.Dial("unix", s.ticket.SocketPath)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(time.Second))
			if err := json.NewEncoder(conn).Encode(map[string]string{"token": token}); err != nil {
				t.Fatal(err)
			}
			if line, err := bufio.NewReader(conn).ReadString('\n'); err == nil {
				t.Fatalf("unauthorized connection accepted: %s", line)
			}
			count, unfinished, err := s.Snapshot()
			if err != nil || count != 0 || unfinished {
				t.Fatalf("unauthorized call touched ledger: %d %v %v", count, unfinished, err)
			}
		})
	}
}

func TestPeerWaitProcess(t *testing.T) {
	if os.Getenv("YANSU_TEST_PEER_WAIT") == "" {
		return
	}
	select {}
}
