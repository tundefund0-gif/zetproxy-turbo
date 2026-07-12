package tunnel

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"github.com/user/zetproxy/internal/pool"
)

var relayPool = pool.New()

func relayHost(addr string) string {
	if idx := strings.LastIndex(addr, ":"); idx > 0 {
		return addr[:idx]
	}
	return addr
}

func StartRelayTunnel(localPort, relayAddr string) error {
	tunnelStop = make(chan struct{})

	if !strings.Contains(relayAddr, ":") {
		relayAddr = relayAddr + ":7800"
	}

	log.Printf("[Tunnel] Connecting to relay %s ...", relayAddr)

	ctrl, err := net.DialTimeout("tcp", relayAddr, 15*time.Second)
	if err != nil {
		return fmt.Errorf("connect relay: %w", err)
	}
	if t, ok := ctrl.(*net.TCPConn); ok {
		t.SetKeepAlive(true)
		t.SetKeepAlivePeriod(15 * time.Second)
		t.SetNoDelay(true)
	}

	if _, err := fmt.Fprintf(ctrl, "register\n"); err != nil {
		ctrl.Close()
		return fmt.Errorf("register: %w", err)
	}

	r := bufio.NewReader(ctrl)
	resp, err := r.ReadString('\n')
	if err != nil {
		ctrl.Close()
		return fmt.Errorf("read id: %w", err)
	}
	resp = strings.TrimSpace(resp)
	if !strings.HasPrefix(resp, "id:") {
		ctrl.Close()
		return fmt.Errorf("bad response: %s", resp)
	}
	id := resp[3:]

	host := relayHost(relayAddr)
	setTunnelURL(fmt.Sprintf("%s (relay %s)", host, id))
	log.Printf("[Tunnel] Registered as %s on relay %s", id, relayAddr)
	log.Printf("[Tunnel] Enter in Super Proxy: host=%s port=%s (SOCKS5)", host, localPort)

	go func() {
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				log.Printf("[Tunnel] Control conn lost: %v", err)
				return
			}
			line = strings.TrimSpace(line)

			if line == "conn" {
				go handleRelayData(localPort, relayAddr, id, ctrl)
			}
		}
	}()

	<-tunnelStop
	ctrl.Close()
	return nil
}

func handleRelayData(localPort, relayAddr, id string, ctrl net.Conn) {
	log.Printf("[Tunnel] Client connected, opening data channel...")

	data, err := net.DialTimeout("tcp", relayAddr, 10*time.Second)
	if err != nil {
		log.Printf("[Tunnel] Data conn failed: %v", err)
		return
	}
	defer data.Close()

	if _, err := fmt.Fprintf(data, "data:%s\n", id); err != nil {
		return
	}

	r := bufio.NewReader(data)
	resp, _ := r.ReadString('\n')
	if strings.TrimSpace(resp) != "ready" {
		return
	}

	local, err := net.DialTimeout("tcp", "127.0.0.1:"+localPort, 5*time.Second)
	if err != nil {
		log.Printf("[Tunnel] Local SOCKS5 unreachable: %v", err)
		return
	}
	defer local.Close()

	if t, ok := data.(*net.TCPConn); ok {
		t.SetNoDelay(true)
	}
	if t, ok := local.(*net.TCPConn); ok {
		t.SetNoDelay(true)
	}

	relayPool.Relay(local, data)
}
