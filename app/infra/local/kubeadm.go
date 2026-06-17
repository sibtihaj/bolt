package local

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	runner "github.com/sibtihaj/bolt/internal/exec"
)

const flannelManifest = "https://raw.githubusercontent.com/flannel-io/flannel/master/Documentation/kube-flannel.yml"

// SSHConfig holds connection details for a remote Linux machine.
type SSHConfig struct {
	Host    string
	User    string
	KeyPath string // path to private key; empty = use default SSH auth
}

// EnsureKubeadmCluster initialises a kubeadm cluster on a remote Linux host
// via SSH and writes the patched kubeconfig to ~/.bolt/kubeconfigs/<namePrefix>.yaml.
// Returns the kubeconfig path. Idempotent — skips kubeadm init if the cluster
// is already running.
func EnsureKubeadmCluster(namePrefix string, ssh SSHConfig) (string, error) {
	// 1. Verify SSH connectivity
	if _, err := sshOutput(ssh, "echo ok"); err != nil {
		return "", fmt.Errorf("SSH connectivity to %s@%s failed — check host, user, and key path: %w",
			ssh.User, ssh.Host, err)
	}

	// 2. Verify kubeadm is installed on the target
	if _, err := sshOutput(ssh, "which kubeadm"); err != nil {
		return "", fmt.Errorf(
			"kubeadm not found on %s — install it before running bolt: "+
				"https://kubernetes.io/docs/setup/production-environment/tools/kubeadm/install-kubeadm/",
			ssh.Host)
	}

	// 3. Check if cluster is already initialised
	alreadyUp := false
	if _, err := sshOutput(ssh, "sudo kubectl --kubeconfig /etc/kubernetes/admin.conf get nodes"); err == nil {
		alreadyUp = true
	}

	if !alreadyUp {
		// 4. Initialise the cluster
		if err := sshRun(ssh, "sudo kubeadm init --pod-network-cidr=10.244.0.0/16"); err != nil {
			return "", fmt.Errorf("kubeadm init: %w", err)
		}

		// 5. Apply Flannel CNI for pod networking
		if err := sshRun(ssh, "sudo kubectl --kubeconfig /etc/kubernetes/admin.conf apply -f "+flannelManifest); err != nil {
			return "", fmt.Errorf("apply Flannel CNI: %w", err)
		}

		// 6. Remove control-plane taint for single-node operation
		_ = sshRun(ssh, "sudo kubectl --kubeconfig /etc/kubernetes/admin.conf taint nodes --all node-role.kubernetes.io/control-plane- 2>/dev/null || true")
	}

	// 7. Fetch kubeconfig from target
	raw, err := sshOutput(ssh, "sudo cat /etc/kubernetes/admin.conf")
	if err != nil {
		return "", fmt.Errorf("read kubeconfig from %s: %w", ssh.Host, err)
	}

	// 8. Patch server URL: replace loopback with the actual SSH host so the Mac can reach it
	patched := strings.ReplaceAll(string(raw), "https://127.0.0.1:6443", "https://"+ssh.Host+":6443")

	// 9. Write patched kubeconfig
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	kubeconfigDir := filepath.Join(home, ".bolt", "kubeconfigs")
	if err := os.MkdirAll(kubeconfigDir, 0700); err != nil {
		return "", fmt.Errorf("create kubeconfigs dir: %w", err)
	}
	kubeconfigPath := filepath.Join(kubeconfigDir, namePrefix+".yaml")
	if err := os.WriteFile(kubeconfigPath, []byte(patched), 0600); err != nil {
		return "", fmt.Errorf("write kubeconfig: %w", err)
	}

	return kubeconfigPath, nil
}

// DeleteKubeadmCluster resets the kubeadm cluster on the remote host via SSH.
func DeleteKubeadmCluster(ssh SSHConfig) error {
	return sshRun(ssh, "sudo kubeadm reset -f && sudo rm -rf /etc/cni/net.d /etc/kubernetes /var/lib/etcd || true")
}

// ── SSH helpers ───────────────────────────────────────────────────────────────

func sshArgs(cfg SSHConfig, command string) []string {
	args := []string{
		"-o", "StrictHostKeyChecking=no",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=15",
	}
	if cfg.KeyPath != "" {
		args = append(args, "-i", cfg.KeyPath)
	}
	args = append(args, cfg.User+"@"+cfg.Host, command)
	return args
}

func sshRun(cfg SSHConfig, command string) error {
	return runner.Run("ssh", sshArgs(cfg, command), runner.RunOptions{})
}

func sshOutput(cfg SSHConfig, command string) ([]byte, error) {
	return runner.Output("ssh", sshArgs(cfg, command), runner.RunOptions{})
}
