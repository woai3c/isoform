package auth

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"yansu-agent/internal/client"
	"yansu-agent/internal/config"
)

const (
	// cliAuthPath is the path for CLI authentication
	cliAuthPath = "/cli-auth"
)

// Login performs the authentication flow
func Login() error {
	cfg, err := config.Load()
	if err != nil {
		cfg = &config.Config{
			BaseURL:    config.DefaultBaseURL,
			WebBaseURL: config.DefaultBaseURL,
			APIBaseURL: config.DefaultBaseURL,
		}
	}

	fmt.Println("Yansu Agent Login")
	fmt.Println()

	// Check if already logged in
	if cfg.Token != "" {
		apiClient := client.New(cfg.APIBaseURL, cfg.Token)
		if err := apiClient.Verify(context.Background()); err == nil {
			fmt.Println("You are already logged in.")
			fmt.Print("Do you want to re-authenticate? [y/N] ")

			reader := bufio.NewReader(os.Stdin)
			answer, _ := reader.ReadString('\n')
			answer = strings.TrimSpace(strings.ToLower(answer))
			if answer != "y" && answer != "yes" {
				return nil
			}
		}
	}

	fmt.Println("Choose authentication method:")
	fmt.Println("  1. Browser login (recommended)")
	fmt.Println("  2. Paste token manually")
	fmt.Print("Enter choice [1]: ")

	reader := bufio.NewReader(os.Stdin)
	choice, _ := reader.ReadString('\n')
	choice = strings.TrimSpace(choice)

	if choice == "" || choice == "1" {
		return browserLogin(cfg)
	}
	return tokenLogin(cfg)
}

func browserLogin(cfg *config.Config) error {
	// Start local server to receive callback
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("failed to start local server: %w", err)
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port
	callbackURL := fmt.Sprintf("http://localhost:%d/callback", port)

	// Channel to receive token
	tokenChan := make(chan string, 1)
	errChan := make(chan error, 1)

	// Start HTTP server
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/callback" {
				http.NotFound(w, r)
				return
			}

			token := r.URL.Query().Get("token")
			if token == "" {
				errChan <- fmt.Errorf("no token received")
				w.Header().Set("Content-Type", "text/html")
				w.Write([]byte(`<html><body><h1>Authentication Failed</h1><p>No token received. Please try again.</p></body></html>`))
				return
			}

			tokenChan <- token
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(`<html><body><h1>Authentication Successful!</h1><p>You can close this window and return to the terminal.</p><script>window.close()</script></body></html>`))
		}),
	}

	go func() {
		server.Serve(listener)
	}()
	defer server.Shutdown(context.Background())

	// Open browser on web URL and complete token verification on API URL.
	authURL := fmt.Sprintf("%s%s?callback=%s", cfg.WebBaseURL, cliAuthPath, callbackURL)
	fmt.Printf("\nOpening browser for authentication...\n")
	fmt.Printf("If browser doesn't open, visit: %s\n\n", authURL)

	if err := openBrowser(authURL); err != nil {
		fmt.Printf("Failed to open browser: %v\n", err)
		fmt.Println("Please open the URL above manually.")
	}

	fmt.Println("Waiting for authentication...")

	// Wait for callback with timeout
	select {
	case token := <-tokenChan:
		return saveAndVerifyToken(cfg, token)
	case err := <-errChan:
		return err
	case <-time.After(5 * time.Minute):
		return fmt.Errorf("authentication timed out")
	}
}

func tokenLogin(cfg *config.Config) error {
	fmt.Println()
	fmt.Println("To get your token:")
	fmt.Println("  1. Open your browser and login to Yansu")
	fmt.Println("  2. Open Developer Tools (F12) -> Application -> Cookies")
	fmt.Println("  3. Copy the 'something-session' cookie value")
	fmt.Println()
	fmt.Print("Paste your token: ")

	reader := bufio.NewReader(os.Stdin)
	token, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("failed to read token: %w", err)
	}

	token = strings.TrimSpace(token)
	if token == "" {
		return fmt.Errorf("token cannot be empty")
	}

	return saveAndVerifyToken(cfg, token)
}

func saveAndVerifyToken(cfg *config.Config, token string) error {
	// Verify token
	apiClient := client.New(cfg.APIBaseURL, token)
	if err := apiClient.Verify(context.Background()); err != nil {
		return fmt.Errorf("token verification failed: %w", err)
	}

	// Save token
	if err := config.SaveToken(token); err != nil {
		return fmt.Errorf("failed to save token: %w", err)
	}

	fmt.Println()
	fmt.Println("✓ Authentication successful!")
	fmt.Println()
	fmt.Println("You can now use Yansu Agent:")
	fmt.Println("  yansu clone <org/product/project>  - Clone a project")
	fmt.Println("  yansu daemon                       - Start the sync daemon")
	fmt.Println("  yansu service install              - Install as system service")

	return nil
}

func openBrowser(url string) error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		// Try xdg-open first, then common browsers
		if _, err := exec.LookPath("xdg-open"); err == nil {
			cmd = exec.Command("xdg-open", url)
		} else if _, err := exec.LookPath("google-chrome"); err == nil {
			cmd = exec.Command("google-chrome", url)
		} else if _, err := exec.LookPath("firefox"); err == nil {
			cmd = exec.Command("firefox", url)
		} else {
			return fmt.Errorf("no browser found")
		}
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
		hideConsoleWindow(cmd)
	default:
		return fmt.Errorf("unsupported platform")
	}

	return cmd.Start()
}

// Logout clears the saved token
func Logout() error {
	token, err := config.LoadToken()
	if err != nil {
		return nil // Nothing to clear
	}

	if token == "" {
		fmt.Println("You are not logged in.")
		return nil
	}

	if err := config.ClearToken(); err != nil {
		return fmt.Errorf("failed to clear token: %w", err)
	}

	fmt.Println("✓ Logged out successfully")
	return nil
}

// Status shows the current authentication status
func Status() error {
	cfg, err := config.Load()
	if err != nil || cfg.Token == "" {
		fmt.Println("Status: Not logged in")
		fmt.Println()
		fmt.Println("Run 'yansu login' to authenticate.")
		return nil
	}

	apiClient := client.New(cfg.APIBaseURL, cfg.Token)
	if err := apiClient.Verify(context.Background()); err != nil {
		fmt.Println("Status: Token invalid or expired")
		fmt.Println()
		fmt.Println("Run 'yansu login' to re-authenticate.")
		return nil
	}

	fmt.Println("Status: Authenticated")
	fmt.Printf("Web URL: %s\n", cfg.WebBaseURL)
	fmt.Printf("API URL: %s\n", cfg.APIBaseURL)
	return nil
}
