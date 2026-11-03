package sshconn

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

const (
	connectTimeout    = 10 * time.Second
	keepAliveInterval = 30 * time.Second

	stateDisconnected = "disconnected"
	stateConnecting   = "connecting"
	stateConnected    = "connected"
	stateError        = "error"
)

// ConnectionStatus describes the current state of an SSH connection.
type ConnectionStatus struct {
	ConfigID string   `json:"config_id"`
	State    string   `json:"state"` // "disconnected", "connecting", "connected", "error"
	Message  string   `json:"message"`
	Logs     []string `json:"logs"` // accumulated log lines for the current connection attempt
}

// connEntry holds all resources for a single managed connection.
type connEntry struct {
	mu     sync.Mutex
	client *ssh.Client
	tunnel *Tunnel
	status ConnectionStatus
	stopKA chan struct{} // stops keepalive goroutine
}

// Manager manages the lifecycle of multiple SSH connections.
type Manager struct {
	mu            sync.RWMutex
	configs       map[string]Config // saved configs (keyed by ID)
	conns         map[string]*connEntry
	onStatus      func(ConnectionStatus)
	onTunnelReady func(configID, localAddr string) // called when tunnel is up
	onDisconnect  func(configID string)            // called when disconnected
}

// NewManager creates a connection manager. The onStatus callback is invoked
// whenever a connection's status changes (may be called from any goroutine).
func NewManager(onStatus func(ConnectionStatus)) *Manager {
	if onStatus == nil {
		onStatus = func(ConnectionStatus) {}
	}
	return &Manager{
		configs:       make(map[string]Config),
		conns:         make(map[string]*connEntry),
		onStatus:      onStatus,
		onTunnelReady: func(string, string) {},
		onDisconnect:  func(string) {},
	}
}

// SetTunnelCallbacks sets callbacks for tunnel lifecycle events.
func (m *Manager) SetTunnelCallbacks(onReady func(configID, localAddr string), onDisconnect func(configID string)) {
	m.onTunnelReady = onReady
	m.onDisconnect = onDisconnect
}

// SaveConfig saves or updates an SSH connection configuration.
// Returns the config ID (generated if the ID field is empty).
func (m *Manager) SaveConfig(cfg Config) (string, error) {
	if cfg.Host == "" {
		return "", fmt.Errorf("host is required")
	}
	if cfg.Name == "" {
		return "", fmt.Errorf("name is required")
	}
	if cfg.Port <= 0 {
		cfg.Port = 22
	}

	// Check name uniqueness — different configs must have different names.
	m.mu.RLock()
	for id, existing := range m.configs {
		if existing.Name == cfg.Name && id != cfg.ID {
			m.mu.RUnlock()
			return "", fmt.Errorf("an SSH connection named %q already exists", cfg.Name)
		}
	}
	m.mu.RUnlock()

	if cfg.ID == "" {
		cfg.ID = fmt.Sprintf("ssh-%s-%d-%d", cfg.Host, cfg.Port, time.Now().UnixMilli())
	}
	m.mu.Lock()
	m.configs[cfg.ID] = cfg
	m.mu.Unlock()
	return cfg.ID, nil
}

// ListConfigs returns all saved SSH connection configurations.
func (m *Manager) ListConfigs() []Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	configs := make([]Config, 0, len(m.configs))
	for _, c := range m.configs {
		configs = append(configs, c)
	}
	return configs
}

// GetConfig returns the config for the given ID.
func (m *Manager) GetConfig(id string) Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.configs[id]
}

// DeleteConfig deletes a saved SSH config and disconnects any active connection.
func (m *Manager) DeleteConfig(id string) error {
	m.Disconnect(id)
	m.mu.Lock()
	delete(m.configs, id)
	m.mu.Unlock()
	return nil
}

// Connect establishes an SSH connection used by remote deployments.
func (m *Manager) Connect(cfg Config) error {
	// Persist runtime-determined fields (e.g. ExecMode) back to the config map
	// so that callbacks like onTunnelReady see the updated values.
	m.mu.Lock()
	if stored, ok := m.configs[cfg.ID]; ok {
		if cfg.ExecMode != "" && stored.ExecMode == "" {
			stored.ExecMode = cfg.ExecMode
			m.configs[cfg.ID] = stored
		}
	}
	m.mu.Unlock()

	entry := m.getOrCreateEntry(cfg.ID)

	entry.mu.Lock()
	defer entry.mu.Unlock()

	// Don't reconnect if already connected.
	if entry.client != nil {
		return nil
	}

	m.setStatus(entry, cfg.ID, stateConnecting, "establishing SSH connection")

	log.Printf("[ssh] Connect: dialing %s@%s:%d", cfg.User, cfg.Host, cfg.Port)
	client, err := dial(cfg)
	if err != nil {
		log.Printf("[ssh] Connect: dial failed: %v", err)
		m.setStatus(entry, cfg.ID, stateError, err.Error())
		return fmt.Errorf("ssh dial: %w", err)
	}
	log.Printf("[ssh] Connect: SSH connected")
	entry.client = client

	// Start keepalive.
	entry.stopKA = make(chan struct{})
	go keepAlive(client, entry.stopKA)

	log.Printf("[ssh] Connect: ready for remote commands")
	m.setStatus(entry, cfg.ID, stateConnected, "")
	m.onTunnelReady(cfg.ID, "")
	return nil
}

// Disconnect closes the connection identified by configID and releases all
// associated resources.
func (m *Manager) Disconnect(configID string) error {
	m.mu.Lock()
	entry, ok := m.conns[configID]
	if !ok {
		m.mu.Unlock()
		return nil
	}
	delete(m.conns, configID)
	m.mu.Unlock()

	entry.mu.Lock()
	defer entry.mu.Unlock()

	m.closeEntry(entry)
	m.setStatus(entry, configID, stateDisconnected, "")
	m.onDisconnect(configID)
	return nil
}

// TestConnection verifies that an SSH connection can be established with
// the given config. The connection is immediately closed.
func (m *Manager) TestConnection(cfg Config) error {
	client, err := dial(cfg)
	if err != nil {
		return err
	}
	return client.Close()
}

// GetStatus returns the current status for a connection.
func (m *Manager) GetStatus(configID string) ConnectionStatus {
	m.mu.RLock()
	entry, ok := m.conns[configID]
	m.mu.RUnlock()
	if !ok {
		return ConnectionStatus{ConfigID: configID, State: stateDisconnected}
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	return entry.status
}

// GetTunnelAddr returns the local address (127.0.0.1:port) of the tunnel
// for the given connection.
func (m *Manager) GetTunnelAddr(configID string) (string, error) {
	m.mu.RLock()
	entry, ok := m.conns[configID]
	m.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("not connected: %s", configID)
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.tunnel == nil {
		return "", fmt.Errorf("no tunnel for: %s", configID)
	}
	return fmt.Sprintf("127.0.0.1:%d", entry.tunnel.LocalPort()), nil
}

// OpenShell opens a new interactive shell on the remote host.
func (m *Manager) OpenShell(configID string, workDir string, cols, rows int) (*Shell, error) {
	m.mu.RLock()
	entry, ok := m.conns[configID]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("not connected: %s", configID)
	}
	entry.mu.Lock()
	client := entry.client
	entry.mu.Unlock()
	if client == nil {
		return nil, fmt.Errorf("not connected: %s", configID)
	}
	return NewShell(client, workDir, cols, rows)
}

// IsConnected returns true if the given config ID has an active connection.
func (m *Manager) IsConnected(configID string) bool {
	return m.GetStatus(configID).State == stateConnected
}

// RunCommand executes a command on the remote server via the connected SSH client.
// Returns combined stdout+stderr output.
func (m *Manager) RunCommand(configID, cmd string) (string, error) {
	m.mu.RLock()
	entry, ok := m.conns[configID]
	m.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("not connected: %s", configID)
	}
	entry.mu.Lock()
	client := entry.client
	entry.mu.Unlock()
	if client == nil {
		return "", fmt.Errorf("not connected: %s", configID)
	}
	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()
	out, err := session.CombinedOutput(cmd)
	return string(out), err
}

// ForwardRemotePort creates an SSH local port forward: a random local port
// tunnels to remoteHost:remotePort on the server. Returns the local port.
func (m *Manager) ForwardRemotePort(configID string, remotePort int) (int, func(), error) {
	m.mu.RLock()
	entry, ok := m.conns[configID]
	m.mu.RUnlock()
	if !ok {
		return 0, nil, fmt.Errorf("not connected: %s", configID)
	}
	entry.mu.Lock()
	client := entry.client
	entry.mu.Unlock()
	if client == nil {
		return 0, nil, fmt.Errorf("not connected: %s", configID)
	}

	// Listen on a random local port.
	localListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, nil, fmt.Errorf("local listen: %w", err)
	}
	localPort := localListener.Addr().(*net.TCPAddr).Port

	// Forward connections in background.
	done := make(chan struct{})
	go func() {
		defer localListener.Close()
		for {
			select {
			case <-done:
				return
			default:
			}
			localConn, err := localListener.Accept()
			if err != nil {
				select {
				case <-done:
				default:
					log.Printf("[ssh-forward] accept error: %v", err)
				}
				return
			}
			remoteConn, err := client.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", remotePort))
			if err != nil {
				localConn.Close()
				log.Printf("[ssh-forward] remote dial error: %v", err)
				continue
			}
			go func() {
				defer localConn.Close()
				defer remoteConn.Close()
				errc := make(chan error, 2)
				go func() { _, err := io.Copy(localConn, remoteConn); errc <- err }()
				go func() { _, err := io.Copy(remoteConn, localConn); errc <- err }()
				<-errc
			}()
		}
	}()

	cleanup := func() {
		close(done)
		localListener.Close()
	}
	return localPort, cleanup, nil
}

// Shutdown disconnects all active connections.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	ids := make([]string, 0, len(m.conns))
	for id := range m.conns {
		ids = append(ids, id)
	}
	m.mu.Unlock()

	for _, id := range ids {
		m.Disconnect(id)
	}
}

// getOrCreateEntry returns the existing entry or creates a new one.
func (m *Manager) getOrCreateEntry(configID string) *connEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	if entry, ok := m.conns[configID]; ok {
		return entry
	}
	entry := &connEntry{
		status: ConnectionStatus{ConfigID: configID, State: stateDisconnected},
	}
	m.conns[configID] = entry
	return entry
}

// setStatus updates the entry status, appends a log line, and fires the callback.
func (m *Manager) setStatus(entry *connEntry, configID, state, message string) {
	// Reset logs on new connection attempt or disconnect.
	if state == stateConnecting && entry.status.State == stateDisconnected {
		entry.status.Logs = nil
	}
	if message != "" {
		entry.status.Logs = append(entry.status.Logs, message)
	}
	entry.status.ConfigID = configID
	entry.status.State = state
	entry.status.Message = message
	m.onStatus(entry.status)
}

// closeEntry releases all resources in an entry. Caller must hold entry.mu.
func (m *Manager) closeEntry(entry *connEntry) {
	if entry.stopKA != nil {
		close(entry.stopKA)
		entry.stopKA = nil
	}
	if entry.tunnel != nil {
		entry.tunnel.Close()
		entry.tunnel = nil
	}
	// Kill remote agent servers and any claude-agent-sdk processes before
	// closing the SSH connection. Child processes may not exit when the
	// parent is killed (stdin close is not always enough), so we kill
	// everything related.
	if entry.client != nil {
		if sess, err := entry.client.NewSession(); err == nil {
			killCmd := "pkill -f 'bun run server.ts'; " +
				"pkill -f 'Bun.serve'; " +
				"pkill -f 'claude-agent-sdk'; " +
				"echo cleanup_done"
			out, err := sess.CombinedOutput(killCmd)
			log.Printf("[ssh] remote cleanup: out=%q err=%v", strings.TrimSpace(string(out)), err)
			sess.Close()
		} else {
			log.Printf("[ssh] remote cleanup: failed to create session: %v", err)
		}
	}
	if entry.client != nil {
		entry.client.Close()
		entry.client = nil
	}
}

// dial creates an SSH client connection using the provided config.
func dial(cfg Config) (*ssh.Client, error) {
	log.Printf("[ssh] dial: building auth methods for auth_type=%q", cfg.AuthType)
	authMethods, err := buildAuthMethods(cfg)
	if err != nil {
		log.Printf("[ssh] dial: buildAuthMethods error: %v", err)
		return nil, err
	}
	log.Printf("[ssh] dial: got %d auth methods", len(authMethods))

	hostKeyCallback, err := buildHostKeyCallback()
	if err != nil {
		log.Printf("[ssh] dial: buildHostKeyCallback error, falling back to insecure: %v", err)
		hostKeyCallback = ssh.InsecureIgnoreHostKey()
	}

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	sshCfg := &ssh.ClientConfig{
		User:            cfg.User,
		Auth:            authMethods,
		HostKeyCallback: hostKeyCallback,
		Timeout:         connectTimeout,
	}

	log.Printf("[ssh] dial: connecting to %s as %s", addr, cfg.User)
	client, err := ssh.Dial("tcp", addr, sshCfg)
	if err != nil {
		errMsg := err.Error()
		// Provide user-friendly messages for common SSH errors.
		if strings.Contains(errMsg, "unable to authenticate") && strings.Contains(errMsg, "attempted methods [none]") {
			switch cfg.AuthType {
			case "password":
				return nil, fmt.Errorf("the server does not accept password authentication for user '%s'. "+
					"Check the server's /etc/ssh/sshd_config: ensure PasswordAuthentication is set to 'yes' "+
					"and PermitRootLogin is not set to 'prohibit-password' (if connecting as root)", cfg.User)
			default:
				return nil, fmt.Errorf("the server rejected all authentication methods. "+
					"Verify the server's SSH configuration allows the selected method for user '%s'", cfg.User)
			}
		}
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	return client, nil
}

// buildAuthMethods constructs SSH auth methods based on the config.
func buildAuthMethods(cfg Config) ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod

	switch cfg.AuthType {
	case "key":
		if cfg.KeyPath != "" {
			// User specified a key file — use it directly.
			key, err := os.ReadFile(cfg.KeyPath)
			if err != nil {
				return nil, fmt.Errorf("read key %s: %w", cfg.KeyPath, err)
			}
			signer, err := ssh.ParsePrivateKey(key)
			if err != nil {
				return nil, fmt.Errorf("parse key: %w", err)
			}
			methods = append(methods, ssh.PublicKeys(signer))
		} else {
			// No key specified — try all common key files, like ssh does.
			signers := findDefaultKeySigners()
			if len(signers) == 0 {
				return nil, fmt.Errorf("no SSH key found. Tried ~/.ssh/id_ed25519, id_rsa, id_ecdsa. " +
					"Specify a key path or use a different auth method")
			}
			methods = append(methods, ssh.PublicKeys(signers...))
		}

	case "password":
		if cfg.Password == "" {
			return nil, fmt.Errorf("password is required for password authentication")
		}
		methods = append(methods, ssh.Password(cfg.Password))
		// Many servers use keyboard-interactive instead of (or in addition to)
		// the plain "password" method. Provide both so authentication succeeds
		// regardless of which method the server advertises.
		methods = append(methods, ssh.KeyboardInteractive(
			func(user, instruction string, questions []string, echos []bool) ([]string, error) {
				answers := make([]string, len(questions))
				for i := range questions {
					answers[i] = cfg.Password
				}
				return answers, nil
			},
		))

	default:
		return nil, fmt.Errorf("unsupported auth type: %s", cfg.AuthType)
	}

	return methods, nil
}

// buildHostKeyCallback loads ~/.ssh/known_hosts for host verification.
// Unknown hosts are automatically accepted and appended to known_hosts.
func buildHostKeyCallback() (ssh.HostKeyCallback, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return ssh.InsecureIgnoreHostKey(), nil //nolint:gosec // fallback when home dir is unavailable
	}
	khPath := filepath.Join(home, ".ssh", "known_hosts")

	// Ensure known_hosts file exists.
	if _, statErr := os.Stat(khPath); os.IsNotExist(statErr) {
		if mkErr := os.MkdirAll(filepath.Dir(khPath), 0700); mkErr != nil {
			return ssh.InsecureIgnoreHostKey(), nil //nolint:gosec // fallback
		}
		if _, createErr := os.Create(khPath); createErr != nil {
			return ssh.InsecureIgnoreHostKey(), nil //nolint:gosec // fallback
		}
	}

	cb, err := knownhosts.New(khPath)
	if err != nil {
		return ssh.InsecureIgnoreHostKey(), nil //nolint:gosec // fallback
	}

	// Wrap the callback to auto-accept and persist unknown hosts.
	wrapped := func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		khErr := cb(hostname, remote, key)
		if khErr == nil {
			return nil
		}
		var keyErr *knownhosts.KeyError
		if errors.As(khErr, &keyErr) {
			if len(keyErr.Want) > 0 {
				// Host key changed — possible MITM attack.
				return fmt.Errorf("host key changed for %s — this may indicate a security issue", hostname)
			}
			// Unknown host — accept and persist the key.
			line := knownhosts.Line([]string{knownhosts.Normalize(remote.String())}, key)
			f, openErr := os.OpenFile(khPath, os.O_APPEND|os.O_WRONLY, 0600)
			if openErr == nil {
				_, _ = fmt.Fprintln(f, line)
				f.Close()
			}
			return nil
		}
		return khErr
	}
	return wrapped, nil
}

// defaultKeyPath returns the default SSH private key path.
// findDefaultKeySigners tries common SSH key files and returns all valid signers.
func findDefaultKeySigners() []ssh.Signer {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	candidates := []string{
		filepath.Join(home, ".ssh", "id_ed25519"),
		filepath.Join(home, ".ssh", "id_rsa"),
		filepath.Join(home, ".ssh", "id_ecdsa"),
		filepath.Join(home, ".ssh", "id_dsa"),
	}
	var signers []ssh.Signer
	for _, path := range candidates {
		key, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			log.Printf("[ssh] skipping %s: %v", path, err)
			continue
		}
		signers = append(signers, signer)
	}
	return signers
}

// keepAlive sends periodic keepalive requests to the SSH server.
func keepAlive(client *ssh.Client, stop <-chan struct{}) {
	ticker := time.NewTicker(keepAliveInterval)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
			if err != nil {
				return
			}
		}
	}
}
