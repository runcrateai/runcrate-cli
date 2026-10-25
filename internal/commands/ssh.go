package commands

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/runcrate/cli/internal/api"
	"golang.org/x/crypto/ssh"
	"github.com/spf13/cobra"
)

var sshCmd = &cobra.Command{
	Use:   "ssh <instance-name-or-id> [-- command]",
	Short: "SSH into a running instance",
	Long: `Opens an interactive SSH session to a running instance.
Uses ephemeral SSH certificates — no key management needed.

Use -- to run a remote command without opening a shell:

  runcrate ssh my-gpu                              # interactive shell
  runcrate ssh my-gpu -- nvidia-smi                # check GPU status
  runcrate ssh my-gpu -- python train.py           # kick off training
  runcrate ssh my-gpu -- df -h                     # check disk space
  runcrate ssh my-gpu -- cat /var/log/training.log # tail logs
  runcrate ssh my-gpu -- kill %1                   # kill a background job`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runSSH(cmd, args, false)
	},
}

// runSSH opens ssh to an instance by name or ID, running the command after --
// when there is one.
func runSSH(cmd *cobra.Command, args []string, dryRun bool) error {
	client, err := loadClient()
	if err != nil {
		return err
	}

	// Resolve instance ID (need the UUID for the certificate API)
	instanceID, ip, err := resolveInstance(client, args[0])
	if err != nil {
		return err
	}

	var remote []string
	if dashIdx := cmd.ArgsLenAtDash(); dashIdx >= 0 && dashIdx < len(args) {
		remote = args[dashIdx:]
	}
	return execSSH(client, instanceID, ip, nil, remote)
}

// execSSH runs ssh to one instance (by UUID, at ip unless the certificate names
// another address) with an ephemeral key signed for it: options go before the
// destination, remote after it.
func execSSH(client *api.Client, instanceID, ip string, options, remote []string) error {
	// Generate ephemeral Ed25519 key pair
	pubKey, privKey, err := generateEphemeralKeyPair()
	if err != nil {
		return fmt.Errorf("generating ephemeral key: %w", err)
	}

	// Request signed certificate from the API
	cert, err := client.GetSSHCertificate(instanceID, pubKey)
	if err != nil {
		return fmt.Errorf("getting SSH certificate: %w", err)
	}

	if cert.IP != "" {
		ip = cert.IP
	}

	// Write ephemeral key + certificate to temp files
	tempDir, keyPath, certPath, err := writeTempSSHFiles(privKey, cert.Certificate)
	if err != nil {
		return fmt.Errorf("writing temp SSH files: %w", err)
	}
	defer os.RemoveAll(tempDir)

	user := cert.User
	if user == "" {
		user = "root"
	}
	port := fmt.Sprintf("%d", cert.Port)
	if cert.Port == 0 {
		port = "22"
	}

	// Build SSH args
	sshArgs := []string{"ssh"}
	sshArgs = append(sshArgs, "-o", "StrictHostKeyChecking=no")
	sshArgs = append(sshArgs, "-o", "UserKnownHostsFile=/dev/null")
	sshArgs = append(sshArgs, "-o", "CertificateFile="+certPath)
	sshArgs = append(sshArgs, "-i", keyPath)
	sshArgs = append(sshArgs, "-p", port)
	sshArgs = append(sshArgs, options...)
	sshArgs = append(sshArgs, fmt.Sprintf("%s@%s", user, ip))
	sshArgs = append(sshArgs, remote...)

	// Find ssh binary
	sshBin, err := exec.LookPath("ssh")
	if err != nil {
		return fmt.Errorf("ssh not found in PATH")
	}

	return execOrRun(sshBin, sshArgs)
}

// resolveInstance finds the instance UUID and IP by name or ID.
func resolveInstance(client *api.Client, instanceRef string) (id, ip string, err error) {
	// Try direct ID lookup first
	inst, err := client.GetInstance(instanceRef)
	if err == nil {
		if !isRunning(inst.Status) {
			return "", "", fmt.Errorf("instance %q is %s — must be running", inst.Name, inst.Status)
		}
		if inst.IP == "" {
			return "", "", fmt.Errorf("instance %q has no IP yet — still provisioning", inst.Name)
		}
		return inst.ID, inst.IP, nil
	}

	// Fall back to name search
	instances, listErr := client.ListInstances("")
	if listErr != nil {
		return "", "", fmt.Errorf("fetching instances: %w", listErr)
	}

	for _, inst := range instances {
		if strings.EqualFold(inst.Name, instanceRef) {
			if inst.IP == "" {
				return "", "", fmt.Errorf("instance %q has no IP yet — still provisioning", inst.Name)
			}
			if !isRunning(inst.Status) {
				return "", "", fmt.Errorf("instance %q is %s — must be running", inst.Name, inst.Status)
			}
			return inst.ID, inst.IP, nil
		}
	}

	return "", "", fmt.Errorf("instance %q not found", instanceRef)
}

func isRunning(status string) bool {
	s := strings.ToLower(status)
	return s == "running" || s == "deployed"
}

// generateEphemeralKeyPair creates a new Ed25519 key pair and returns
// the public key in OpenSSH format and the private key in PEM format.
func generateEphemeralKeyPair() (pubKeyStr string, privKeyPEM string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}

	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", "", err
	}
	pubKeyStr = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))

	// Marshal private key to OpenSSH PEM format
	privPEM, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		return "", "", err
	}
	privKeyPEM = string(pem.EncodeToMemory(privPEM))

	return pubKeyStr, privKeyPEM, nil
}

// writeTempSSHFiles writes the ephemeral private key and certificate to temp files.
func writeTempSSHFiles(privKeyPEM, certificate string) (tempDir, keyPath, certPath string, err error) {
	tempDir, err = os.MkdirTemp("", "runcrate-ssh-*")
	if err != nil {
		return "", "", "", err
	}
	os.Chmod(tempDir, 0700)

	keyPath = filepath.Join(tempDir, "ephemeral_key")
	if err := os.WriteFile(keyPath, []byte(privKeyPEM), 0600); err != nil {
		os.RemoveAll(tempDir)
		return "", "", "", err
	}

	certPath = filepath.Join(tempDir, "ephemeral_key-cert.pub")
	if err := os.WriteFile(certPath, []byte(certificate+"\n"), 0644); err != nil {
		os.RemoveAll(tempDir)
		return "", "", "", err
	}

	return tempDir, keyPath, certPath, nil
}

func init() {
	rootCmd.AddCommand(sshCmd)
}
