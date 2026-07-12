package tunnel

import (
	"fmt"
	"io"
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"
)

var (
	TunnelURL  string
	TunnelMu   sync.RWMutex
	tunnelStop chan struct{}
	tunnelCmd  *exec.Cmd
)

func GetTunnelURL() string {
	TunnelMu.RLock()
	defer TunnelMu.RUnlock()
	return TunnelURL
}

func setTunnelURL(url string) {
	TunnelMu.Lock()
	defer TunnelMu.Unlock()
	TunnelURL = url
}

func StopTunnel() {
	if tunnelStop != nil {
		close(tunnelStop)
	}
	if tunnelCmd != nil && tunnelCmd.Process != nil {
		tunnelCmd.Process.Kill()
	}
}

func StartSSHTunnel(localPort, sshHost string) error {
	tunnelStop = make(chan struct{})

	remotePortStr := ""
	if strings.Contains(sshHost, ":") {
		parts := strings.SplitN(sshHost, ":", 2)
		sshHost = parts[0]
		remotePortStr = parts[1]
	}

	tryPorts := []string{localPort}
	if remotePortStr != "" {
		tryPorts = []string{remotePortStr}
	}

	for _, rp := range tryPorts {
		err := trySSHRemote(sshHost, localPort, rp)
		if err == nil {
			setTunnelURL(fmt.Sprintf("%s:%s", sshHost, rp))
			log.Printf("[Tunnel] PUBLIC SOCKS5: %s:%s", sshHost, rp)
			return nil
		}
		log.Printf("[Tunnel] Port %s failed: %v", rp, err)
	}

	return fmt.Errorf("all ports failed on %s", sshHost)
}

func trySSHRemote(host, localPort, remotePort string) error {
	args := []string{
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
		"-o", "ExitOnForwardFailure=yes",
		"-o", "ConnectTimeout=10",
	}

	wildcard := fmt.Sprintf("*:%s:127.0.0.1:%s", remotePort, localPort)
	noWildcard := fmt.Sprintf("%s:127.0.0.1:%s", remotePort, localPort)

	args = append(args, "-R", wildcard, "-R", noWildcard, "-N", host)

	cmd := exec.Command("ssh", args...)

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start: %w", err)
	}

	errCh := make(chan error, 1)
	var stderrBuf strings.Builder

	go func() {
		io.Copy(&stderrBuf, stderr)
		errCh <- cmd.Wait()
	}()

	select {
	case err := <-errCh:
		output := stderrBuf.String()
		if err != nil {
			if strings.Contains(output, "forwarding failed") {
				return fmt.Errorf("port %s rejected", remotePort)
			}
			lines := strings.SplitN(output, "\n", 2)
			return fmt.Errorf("%s", strings.TrimSpace(lines[0]))
		}
		return nil
	case <-time.After(3 * time.Second):
		if cmd.Process != nil {
			tunnelCmd = cmd
			go func() {
				<-tunnelStop
				cmd.Process.Kill()
			}()
			go func() {
				if err := cmd.Wait(); err != nil {
					log.Printf("[Tunnel] SSH exited: %v", err)
				}
			}()
			return nil
		}
		return fmt.Errorf("process vanished")
	}
}

func ParseTunnelConfig(val string) (mode string, remote string) {
	val = strings.TrimSpace(val)
	if val == "" {
		return "", ""
	}
	if val == "serveo" || val == "serveo.net" {
		return "serveo", ""
	}
	if val == "ssh" || val == "1" || val == "true" || val == "yes" {
		return "ssh", "serveo.net"
	}
	if val == "localrun" {
		return "ssh", "nokey@localhost.run"
	}
	if strings.HasPrefix(val, "relay:") {
		rest := strings.TrimPrefix(val, "relay:")
		if rest == "" {
			rest = "127.0.0.1:7800"
		}
		return "relay", rest
	}
	if strings.HasPrefix(val, "serveo:") {
		rest := strings.TrimPrefix(val, "serveo:")
		return "serveo", rest
	}
	if strings.HasPrefix(val, "ssh:") {
		rest := val[4:]
		if !strings.Contains(rest, "@") && !strings.Contains(rest, ".") {
			rest = "serveo.net"
		}
		return "ssh", rest
	}
	if strings.Contains(val, "@") || strings.Contains(val, ".") {
		return "ssh", val
	}
	return "ssh", "serveo.net"
}
