package tunnel

import (
	"fmt"
	"io"
	"log"
	"os/exec"
	"strconv"
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

	if sshHost == "" {
		sshHost = "serveo.net"
	}

	customPort := ""
	if strings.Contains(sshHost, ":") {
		parts := strings.SplitN(sshHost, ":", 2)
		sshHost = parts[0]
		customPort = parts[1]
	}

	port, _ := strconv.Atoi(localPort)
	if port == 0 {
		port = 1088
	}

	portsToTry := []int{port}
	if customPort != "" {
		if cp, err := strconv.Atoi(customPort); err == nil {
			portsToTry = []int{cp}
		}
	} else if sshHost == "serveo.net" {
		portsToTry = []int{port, 8080, 8888, 9999, 10000, 10800}
	}

	userHost := sshHost
	if !strings.Contains(sshHost, "@") && sshHost == "serveo.net" {
		userHost = "serveo.net"
	}

	for _, tryPort := range portsToTry {
		log.Printf("[Tunnel] Trying SSH port forward %d -> localhost:%s ...", tryPort, localPort)

		err := trySSHRemote(userHost, localPort, tryPort)
		if err == nil {
		host := sshHost
		if strings.Contains(host, ":") {
			host = strings.SplitN(host, ":", 2)[0]
		}
		setTunnelURL(fmt.Sprintf("%s:%d", host, tryPort))
		log.Printf("[Tunnel] PUBLIC SOCKS5: %s:%d", host, tryPort)
			log.Printf("[Tunnel] Set Super Proxy to SOCKS5 host=%s port=%d", sshHost, tryPort)
			return nil
		}
		log.Printf("[Tunnel] Port %d failed: %v", tryPort, err)
	}

	return fmt.Errorf("all ports failed on %s", sshHost)
}

func trySSHRemote(host, localPort string, remotePort int) error {
	args := []string{
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
		"-o", "ExitOnForwardFailure=yes",
		"-o", "ConnectTimeout=10",
	}

	wildcard := fmt.Sprintf("*:%d:127.0.0.1:%s", remotePort, localPort)
	noWildcard := fmt.Sprintf("%d:127.0.0.1:%s", remotePort, localPort)

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
				return fmt.Errorf("port %d rejected", remotePort)
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
	if val == "ssh" || val == "serveo" || val == "1" || val == "true" || val == "yes" {
		return "ssh", "serveo.net"
	}
	if val == "localrun" {
		return "ssh", "nokey@localhost.run"
	}
	if strings.HasPrefix(val, "serveo:") {
		rest := val[7:]
		if rest == "" {
			rest = "serveo.net"
		}
		return "ssh", rest
	}
	if strings.HasPrefix(val, "ssh:") {
		rest := val[4:]
		if !strings.Contains(rest, "@") && !strings.Contains(rest, ".") {
			rest = rest + "@" + rest
		}
		return "ssh", rest
	}
	if strings.Contains(val, "@") || strings.Contains(val, ".") {
		return "ssh", val
	}
	return "ssh", "serveo.net"
}
