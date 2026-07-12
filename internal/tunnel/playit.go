package tunnel

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

var (
	TunnelURL   string
	TunnelMu    sync.RWMutex
	tunnelCmd   *exec.Cmd
	tunnelStop  chan struct{}
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

func StartPlayitTunnel(socksPort, secret string) error {
	tunnelStop = make(chan struct{})
	arch := runtime.GOARCH
	binPath := filepath.Join(os.TempDir(), "zetproxy-playit")

	switch arch {
	case "arm":
		binPath += "-arm"
	case "arm64":
		binPath += "-aarch64"
	default:
		binPath += "-x86_64"
	}

	if _, err := os.Stat(binPath); os.IsNotExist(err) {
		log.Printf("[Tunnel] Downloading playit.gg agent for %s/%s...", runtime.GOOS, arch)
		if err := downloadPlayit(binPath, arch); err != nil {
			return fmt.Errorf("download playit: %w", err)
		}
	}

	configFile := filepath.Join(os.TempDir(), "zetproxy-playit-config.json")
	portNum := 1088
	fmt.Sscanf(socksPort, "%d", &portNum)
	config := map[string]interface{}{
		"secret": secret,
		"tunnels": []map[string]interface{}{
			{
				"protocol": "tcp",
				"port":     portNum,
				"target":   fmt.Sprintf("localhost:%s", socksPort),
			},
		},
	}
	configData, _ := json.Marshal(config)
	if err := os.WriteFile(configFile, configData, 0644); err != nil {
		return fmt.Errorf("write playit config: %w", err)
	}

	log.Printf("[Tunnel] Starting playit.gg tunnel (SOCKS5 -> internet)")
	log.Printf("[Tunnel] If asked, sign up at https://playit.gg and create an agent")
	log.Printf("[Tunnel] Set PLAYIT_SECRET=<your_secret> env var")

	tunnelCmd = exec.Command(binPath, "--config", configFile)
	tunnelCmd.Stdin = nil

	stdout, err := tunnelCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("playit stdout pipe: %w", err)
	}
	stderr, err := tunnelCmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("playit stderr pipe: %w", err)
	}

	if err := tunnelCmd.Start(); err != nil {
		return fmt.Errorf("start playit: %w", err)
	}

	go parsePlayitOutput(stdout, "stdout")
	go parsePlayitOutput(stderr, "stderr")

	go func() {
		if err := tunnelCmd.Wait(); err != nil {
			if err.Error() != "signal: killed" {
				log.Printf("[Tunnel] playit agent exited: %v", err)
			}
		}
	}()

	go func() {
		select {
		case <-tunnelStop:
			if tunnelCmd != nil && tunnelCmd.Process != nil {
				tunnelCmd.Process.Signal(os.Interrupt)
				time.Sleep(3 * time.Second)
				tunnelCmd.Process.Kill()
			}
		}
	}()

	return nil
}

func StopTunnel() {
	if tunnelCmd != nil && tunnelCmd.Process != nil {
		log.Printf("[Tunnel] Stopping playit agent...")
		close(tunnelStop)
	}
}

func downloadPlayit(dest, arch string) error {
	url := fmt.Sprintf("https://github.com/playit-cloud/playit-agent/releases/latest/download/playit-linux-%s", arch)
	if arch == "arm" {
		url = "https://github.com/playit-cloud/playit-agent/releases/latest/download/playit-linux-arm"
	} else if arch == "arm64" {
		url = "https://github.com/playit-cloud/playit-agent/releases/latest/download/playit-linux-aarch64"
	}

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	tmpDest := dest + ".download"
	f, err := os.OpenFile(tmpDest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}

	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmpDest)
		return err
	}
	f.Close()

	if err := os.Rename(tmpDest, dest); err != nil {
		os.Remove(tmpDest)
		return err
	}

	log.Printf("[Tunnel] Downloaded playit agent to %s", dest)
	return nil
}

var tunnelURLRegex = regexp.MustCompile(`(?:tcp://|https?://)?([a-zA-Z0-9_-]+\.playit\.gg:\d+)`)

func parsePlayitOutput(reader io.Reader, label string) {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "tcp://") || strings.Contains(line, "playit.gg") {
			if matches := tunnelURLRegex.FindStringSubmatch(line); len(matches) > 1 {
				url := matches[1]
				setTunnelURL(url)
				log.Printf("[Tunnel] PUBLIC SOCKS5: %s", url)
				log.Printf("[Tunnel] Enter this host:port in Super Proxy!")
			}
		}
		if label == "stderr" {
			if !strings.Contains(line, "keepalive") && !strings.Contains(line, "heartbeat") {
				log.Printf("[playit] %s", line)
			}
		}
	}
}

func GetPlayitDownloadURL() string {
	arch := runtime.GOARCH
	base := "https://github.com/playit-cloud/playit-agent/releases/latest/download/playit-linux-"
	switch arch {
	case "arm":
		return base + "arm"
	case "arm64":
		return base + "aarch64"
	default:
		return base + "x86_64"
	}
}
