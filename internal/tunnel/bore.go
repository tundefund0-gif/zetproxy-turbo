package tunnel

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/user/zetproxy/internal/pool"
)

var (
	TunnelURL  string
	TunnelMu   sync.RWMutex
	tunnelStop chan struct{}
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
}

// StartBoreTunnel starts a TCP tunnel via bore.pub compatible relay.
// Protocol: https://github.com/ekzhang/bore
func StartBoreTunnel(localPort, relayAddr string) error {
	tunnelStop = make(chan struct{})

	if relayAddr == "" {
		relayAddr = "bore.pub:7835"
	} else if !strings.Contains(relayAddr, ":") {
		relayAddr = relayAddr + ":7835"
	}

	port := 0
	fmt.Sscanf(localPort, "%d", &port)

	log.Printf("[Tunnel] Connecting to relay %s ...", relayAddr)

	control, err := net.DialTimeout("tcp", relayAddr, 10*time.Second)
	if err != nil {
		return fmt.Errorf("connect relay: %w", err)
	}

	magic := []byte("bore")
	hello := make([]byte, 6)
	copy(hello[:4], magic)
	binary.BigEndian.PutUint16(hello[4:6], uint16(port))

	if _, err := control.Write(hello); err != nil {
		control.Close()
		return fmt.Errorf("send hello: %w", err)
	}

	resp := make([]byte, 2)
	if _, err := io.ReadFull(control, resp); err != nil {
		control.Close()
		return fmt.Errorf("read response: %w", err)
	}

	assignedPort := binary.BigEndian.Uint16(resp)
	if assignedPort == 0 {
		control.Close()
		return fmt.Errorf("relay rejected connection (rate limited?)")
	}

	publicURL := fmt.Sprintf("%s:%d", relayHost(relayAddr), assignedPort)
	setTunnelURL(publicURL)

	log.Printf("[Tunnel] PUBLIC SOCKS5: %s", publicURL)
	log.Printf("[Tunnel] Enter this host:port in Super Proxy!")
	log.Printf("[Tunnel] Relay: %s | Internal port: %s", relayAddr, localPort)

	go func() {
		defer control.Close()
		buf := make([]byte, 1)
		for {
			select {
			case <-tunnelStop:
				return
			default:
			}

			control.SetReadDeadline(time.Now().Add(2 * time.Second))
			_, err := io.ReadFull(control, buf)
			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					continue
				}
				if err == io.EOF {
					log.Printf("[Tunnel] Relay disconnected")
				}
				return
			}

			if buf[0] == 0x01 {
				go handleBoreConnection(localPort, relayAddr)
			}
		}
	}()

	return nil
}

func handleBoreConnection(localPort, relayAddr string) {
	target, err := net.DialTimeout("tcp", "127.0.0.1:"+localPort, 10*time.Second)
	if err != nil {
		log.Printf("[Tunnel] Cannot reach local SOCKS5: %v", err)
		return
	}
	defer target.Close()

	relay, err := net.DialTimeout("tcp", relayAddr, 10*time.Second)
	if err != nil {
		log.Printf("[Tunnel] Cannot connect relay: %v", err)
		return
	}
	defer relay.Close()

	hello := make([]byte, 6)
	copy(hello[:4], []byte("bore"))
	binary.BigEndian.PutUint16(hello[4:6], uint16(0xFFFF))
	if _, err := relay.Write(hello); err != nil {
		return
	}

	resp := make([]byte, 2)
	if _, err := io.ReadFull(relay, resp); err != nil {
		return
	}

	bp := pool.New()
	bp.Relay(target, relay)
}

func relayHost(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

// ParseTunnelConfig parses ZETPROXY_TUNNEL.
//   "bore"               -> bore.pub:7835
//   "bore://host:port"   -> custom relay
func ParseTunnelConfig(val string) (mode string, addr string) {
	val = strings.TrimSpace(val)
	if val == "" {
		return "", ""
	}
	if val == "bore" {
		return "bore", "bore.pub:7835"
	}
	if strings.HasPrefix(val, "bore://") {
		rest := val[7:]
		if !strings.Contains(rest, ":") {
			rest = rest + ":7835"
		}
		return "bore", rest
	}
	if u, err := url.Parse(val); err == nil && u.Host != "" {
		return "bore", u.Host
	}
	return "bore", "bore.pub:7835"
}
