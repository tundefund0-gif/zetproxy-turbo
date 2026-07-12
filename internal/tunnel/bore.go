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

var defaultRelays = []string{
	"bore.pub:7835",
	"bore.azz.lol:7835",
}

type BoreClient struct {
	localPort   string
	relayAddr   string
	control     net.Conn
	pool        *pool.BufferPool
}

func StartBoreTunnel(localPort, relayAddr string) error {
	tunnelStop = make(chan struct{})

	if relayAddr == "" || relayAddr == "bore.pub:7835" {
		for _, addr := range defaultRelays {
			log.Printf("[Tunnel] Trying relay %s ...", addr)
			err := tryBoreRelay(localPort, addr)
			if err == nil {
				return nil
			}
			log.Printf("[Tunnel] Relay %s failed: %v", addr, err)
		}
		return fmt.Errorf("all relays failed")
	}

	return tryBoreRelay(localPort, relayAddr)
}

func tryBoreRelay(localPort, relayAddr string) error {
	if !strings.Contains(relayAddr, ":") {
		relayAddr = relayAddr + ":7835"
	}

	port := 0
	fmt.Sscanf(localPort, "%d", &port)

	log.Printf("[Tunnel] Connecting to %s ...", relayAddr)

	control, err := net.DialTimeout("tcp", relayAddr, 15*time.Second)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	if tcp, ok := control.(*net.TCPConn); ok {
		tcp.SetKeepAlive(true)
		tcp.SetKeepAlivePeriod(15 * time.Second)
		tcp.SetNoDelay(true)
	}

	hello := make([]byte, 6)
	copy(hello[:4], []byte("bore"))
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
		return fmt.Errorf("rejected (rate limited)")
	}

	host := relayHost(relayAddr)
	publicURL := fmt.Sprintf("%s:%d", host, assignedPort)
	setTunnelURL(publicURL)

	log.Printf("[Tunnel] PUBLIC SOCKS5:  %s", publicURL)
	log.Printf("[Tunnel] Enter this in Super Proxy as SOCKS5 host:port")
	log.Printf("[Tunnel] Relay: %s | Local port: %s", relayAddr, localPort)

	client := &BoreClient{
		localPort: localPort,
		relayAddr: relayAddr,
		control:   control,
		pool:      pool.New(),
	}

	go client.controlLoop()

	return nil
}

func (c *BoreClient) controlLoop() {
	defer c.control.Close()
	buf := make([]byte, 1)

	for {
		select {
		case <-tunnelStop:
			return
		default:
		}

		c.control.SetReadDeadline(time.Now().Add(3 * time.Second))
		_, err := io.ReadFull(c.control, buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			if err == io.EOF {
				log.Printf("[Tunnel] Relay disconnected")
			} else {
				log.Printf("[Tunnel] Relay error: %v", err)
			}
			return
		}

		if buf[0] == 0x01 {
			go c.handleConnection()
		}
	}
}

func (c *BoreClient) handleConnection() {
	target, err := net.DialTimeout("tcp", "127.0.0.1:"+c.localPort, 10*time.Second)
	if err != nil {
		log.Printf("[Tunnel] Local SOCKS5 unreachable: %v", err)
		return
	}
	defer target.Close()

	relay, err := net.DialTimeout("tcp", c.relayAddr, 10*time.Second)
	if err != nil {
		log.Printf("[Tunnel] Relay reconnect failed: %v", err)
		return
	}
	defer relay.Close()

	hello := make([]byte, 6)
	copy(hello[:4], []byte("bore"))
	binary.BigEndian.PutUint16(hello[4:6], 0xFFFF)
	if _, err := relay.Write(hello); err != nil {
		return
	}

	resp := make([]byte, 2)
	if _, err := io.ReadFull(relay, resp); err != nil {
		return
	}

	c.pool.Relay(target, relay)
}

func relayHost(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

func ParseTunnelConfig(val string) (mode string, addr string) {
	val = strings.TrimSpace(val)
	if val == "" {
		return "", ""
	}
	if val == "bore" || val == "1" || val == "true" || val == "yes" {
		return "bore", ""
	}
	if strings.HasPrefix(val, "bore://") {
		rest := val[7:]
		if !strings.Contains(rest, ":") {
			rest = rest + ":7835"
		}
		return "bore", rest
	}
	if strings.Contains(val, ":") || strings.Contains(val, ".") {
		if !strings.Contains(val, "://") {
			val = "tcp://" + val
		}
		if u, err := url.Parse(val); err == nil && u.Host != "" {
			return "bore", u.Host
		}
	}
	return "bore", ""
}
