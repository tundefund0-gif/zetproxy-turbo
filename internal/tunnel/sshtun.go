package tunnel

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os/exec"
	"strings"
	"sync"
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

	remotePort := localPort
	if strings.Contains(sshHost, ":") {
		parts := strings.SplitN(sshHost, ":", 2)
		sshHost = parts[0]
		remotePort = parts[1]
	}

	knownHosts := "/dev/null"
	if sshHost == "serveo.net" {
		knownHosts = "/dev/null"
	} else if strings.Contains(sshHost, "localhost.run") {
		knownHosts = "/dev/null"
	}

	args := []string{
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=" + knownHosts,
		"-o", "ServerAliveInterval=30",
		"-o", "ServerAliveCountMax=3",
		"-o", "ExitOnForwardFailure=yes",
		"-R", fmt.Sprintf("%s:127.0.0.1:%s", remotePort, localPort),
		"-N",
	}

	userHost := sshHost
	if !strings.Contains(sshHost, "@") && sshHost == "serveo.net" {
		args = append(args, "serveo.net")
	} else {
		args = append(args, userHost)
	}

	log.Printf("[Tunnel] Starting SSH tunnel: %s -> %s:%s", sshHost, remotePort, localPort)
	log.Printf("[Tunnel] ssh %s", strings.Join(args, " "))

	tunnelCmd = exec.Command("ssh", args...)

	stdout, err := tunnelCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := tunnelCmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := tunnelCmd.Start(); err != nil {
		return fmt.Errorf("start ssh: %w", err)
	}

	go parseSSHOutput(stdout, "out", remotePort, sshHost)
	go parseSSHOutput(stderr, "err", remotePort, sshHost)

	go func() {
		if err := tunnelCmd.Wait(); err != nil {
			log.Printf("[Tunnel] SSH exited: %v", err)
		}
	}()

	go func() {
		select {
		case <-tunnelStop:
			if tunnelCmd != nil && tunnelCmd.Process != nil {
				tunnelCmd.Process.Kill()
			}
		}
	}()

	setTunnelURL(fmt.Sprintf("%s:%s", sshHost, remotePort))

	return nil
}

func parseSSHOutput(reader io.Reader, label, port, host string) {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "Forwarding") || strings.Contains(line, "forwarding") {
			log.Printf("[Tunnel] %s", line)
		}
		if strings.Contains(line, "http://") || strings.Contains(line, "https://") {
			log.Printf("[Tunnel] PUBLIC URL: %s", line)
			url := extractURL(line)
			if url != "" {
				setTunnelURL(url)
			}
		}
		if strings.Contains(line, "port") && strings.Contains(line, "forward") {
			log.Printf("[Tunnel] %s", line)
		}
		if strings.Contains(line, "connecting") || strings.Contains(line, "connected") {
			log.Printf("[Tunnel] %s", line)
		}
		if label == "err" && !strings.Contains(line, "debug") && !strings.Contains(line, "Warning") {
			if strings.Contains(line, "Authentication") || strings.Contains(line, "password") || strings.Contains(line, "denied") || strings.Contains(line, "refused") || strings.Contains(line, "error") {
				log.Printf("[Tunnel] %s", line)
			}
		}
	}
}

func extractURL(line string) string {
	line = strings.TrimSpace(line)
	idx := strings.Index(line, "http://")
	if idx >= 0 {
		end := strings.Index(line[idx:], " ")
		if end > 0 {
			return line[idx : idx+end]
		}
		return line[idx:]
	}
	idx = strings.Index(line, "https://")
	if idx >= 0 {
		end := strings.Index(line[idx:], " ")
		if end > 0 {
			return line[idx : idx+end]
		}
		return line[idx:]
	}
	return ""
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
	if strings.HasPrefix(val, "ssh:") {
		rest := val[4:]
		return "ssh", rest
	}
	if strings.HasPrefix(val, "serveo:") {
		rest := val[7:]
		if rest == "" {
			rest = "serveo.net"
		}
		return "ssh", rest
	}
	if strings.Contains(val, "@") || strings.Contains(val, ".com") || strings.Contains(val, ".net") || strings.Contains(val, ".org") {
		return "ssh", val
	}
	return "ssh", "serveo.net"
}

func init() {
	_, err := exec.LookPath("ssh")
	if err != nil {
		log.Printf("[Tunnel] ssh not found in PATH. Install: pkg install openssh")
	}
}
