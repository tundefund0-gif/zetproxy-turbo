package tunnel

import (
	"bufio"
	"crypto/rand"
	"fmt"
	"io"
	"log"
	"math/big"
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

func randUser() string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 12)
	for i := range b {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		b[i] = chars[n.Int64()]
	}
	return string(b)
}

func StartSSHTunnel(localPort, sshHost string) error {
	tunnelStop = make(chan struct{})

	if sshHost == "" || sshHost == "serveo.net" {
		user := randUser()
		url, err := startServeoTunnel(localPort, user)
		if err != nil {
			return err
		}
		setTunnelURL(url)
		log.Printf("[Tunnel] PUBLIC HTTP: %s", url)
		log.Printf("[Tunnel] Set Super Proxy to HTTP proxy host=%s port=80", url)
		return nil
	}

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

func startServeoTunnel(localPort, username string) (string, error) {
	cmd := exec.Command("ssh",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
		"-o", "ConnectTimeout=10",
		"-R", fmt.Sprintf("80:127.0.0.1:%s", localPort),
		"-N",
		fmt.Sprintf("%s@serveo.net", username),
	)

	// Combine stdout+stderr into one pipe
	combined, err := cmd.StderrPipe()
	if err != nil {
		return "", fmt.Errorf("pipe: %w", err)
	}
	// Also capture stdout
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start: %w", err)
	}

	type line struct {
		text string
		err  error
	}
	lines := make(chan line, 64)

	reader := func(r io.Reader) {
		br := bufio.NewReaderSize(r, 4096)
		for {
			l, err := br.ReadString('\n')
			if err != nil {
				if err != io.EOF {
					lines <- line{err: err}
				}
				return
			}
			lines <- line{text: l}
		}
	}

	go reader(combined)
	go reader(stdout)

	for {
		select {
		case l := <-lines:
			if l.err != nil {
				continue
			}
			out := strings.TrimRight(l.text, "\r\n")
			log.Printf("[Tunnel] serveo: %s", out)

			if strings.Contains(out, "forwarding failed") {
				cmd.Process.Kill()
				return "", fmt.Errorf("port rejected")
			}
			if strings.Contains(out, "https://") || strings.Contains(out, "http://") {
				idx := strings.Index(out, "https://")
				if idx < 0 {
					idx = strings.Index(out, "http://")
				}
				rest := out[idx:]
				end := strings.IndexAny(rest, " \t\r\n")
				if end > 0 {
					rest = rest[:end]
				}
				host := strings.TrimPrefix(rest, "https://")
				host = strings.TrimPrefix(host, "http://")
				tunnelCmd = cmd
				go func() {
					<-tunnelStop
					cmd.Process.Kill()
				}()
				go func() {
					if err := cmd.Wait(); err != nil {
						log.Printf("[Tunnel] serveo exited: %v", err)
					}
				}()
				return host, nil
			}
		case <-time.After(20 * time.Second):
			cmd.Process.Kill()
			return "", fmt.Errorf("timeout waiting for serveo URL")
		}
	}
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
	if val == "serveo" {
		return "serveo", "serveo.net"
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
