package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/runcrate/cli/internal/api"
	"github.com/runcrate/cli/internal/auth"
	"github.com/runcrate/cli/internal/config"
)

// loadClient creates an authenticated API client from saved config.
// Supports OAuth tokens (primary) and legacy API keys.
func loadClient() (*api.Client, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}

	if cfg.IsOAuth() {
		if auth.NeedsRefresh(cfg) {
			if err := auth.RefreshTokens(cfg); err != nil {
				return nil, fmt.Errorf("session expired — run: runcrate login\n  (%w)", err)
			}
		}
		return api.NewOAuthClient(cfg.APIURL, cfg.AccessToken, cfg.ProjectID, cfg.Environment), nil
	}

	if cfg.APIKey != "" {
		return api.NewClient(cfg.APIKey, cfg.APIURL), nil
	}

	return nil, fmt.Errorf("not authenticated — run: runcrate login")
}

// newTable creates a tabwriter for formatted table output.
func newTable() *tabwriter.Writer {
	return newTableTo(os.Stdout)
}

// newTableTo is newTable writing to w.
func newTableTo(w io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
}

// printJSON outputs data as indented JSON.
func printJSON(v any) error {
	return printJSONTo(os.Stdout, v)
}

// printJSONTo writes data as indented JSON to w.
func printJSONTo(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// truncate shortens a string to maxLen, adding "…" if needed.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-1] + "…"
}

// execOrRun replaces the current process on Unix (syscall.Exec) or spawns
// a child process on Windows where Exec is not supported.
func execOrRun(bin string, argv []string) error {
	if runtime.GOOS == "windows" {
		cmd := exec.Command(bin, argv[1:]...)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	return syscall.Exec(bin, argv, os.Environ())
}

// statusColor returns the status with a simple text indicator.
func statusColor(status string) string {
	switch strings.ToLower(status) {
	case "running", "deployed", "succeeded", "ready", "passed":
		return "● " + status
	case "creating", "deploying", "awaiting_provider", "queued", "starting", "provisioning", "installing", "terminating":
		return "◌ " + status
	case "terminated", "failed", "cancelled", "timed_out":
		return "✕ " + status
	default:
		return "  " + status
	}
}
