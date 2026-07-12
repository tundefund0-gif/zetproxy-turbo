package tunnel

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/user/zetproxy/internal/pool"
)

// Stats tracks tunnel performance
// NOTE: int64 fields first for ARM32 alignment (unaligned atomic panic fix)
type Stats struct {
	BytesIn      int64   `json:"bytes_in"`
	BytesOut     int64   `json:"bytes_out"`
	ConnsTotal   int64   `json:"conns_total"`
	StartTime    int64   `json:"start_time"`
	ThroughputIn float64 `json:"throughput_in_mbps"`
	ConnsActive  int32   `json:"conns_active"`
}

// Server is the ultra-fast tunnel with auto-detection
type Server struct {
	tcpAddr  string
	udpAddr  string
	stats    Stats
	pool     *pool.BufferPool
	mu       sync.Mutex
	lastIn   int64
	lastTime time.Time
}

func NewServer(tcpAddr, udpAddr string) *Server {
	return &Server{
		tcpAddr:  tcpAddr,
		udpAddr:  udpAddr,
		pool:     pool.New(),
		lastTime: time.Now(),
		stats: Stats{
			StartTime: time.Now().Unix(),
		},
	}
}

func (s *Server) GetStats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	elapsed := now.Sub(s.lastTime).Seconds()
	if elapsed >= 1.0 {
		in := atomic.LoadInt64(&s.stats.BytesIn)
		s.stats.ThroughputIn = (float64(in-s.lastIn) * 8) / (elapsed * 1000 * 1000)
		s.lastIn = in
		s.lastTime = now
	}
	return s.stats
}

func (s *Server) Start(ctx context.Context) error {
	errCh := make(chan error, 2)

	go func() {
		if err := s.startTCP(ctx); err != nil {
			errCh <- fmt.Errorf("TCP: %w", err)
		}
	}()

	go func() {
		if err := s.startUDP(ctx); err != nil {
			errCh <- fmt.Errorf("UDP: %w", err)
		}
	}()

	return <-errCh
}

func (s *Server) startTCP(ctx context.Context) error {
	lc := net.ListenConfig{KeepAlive: 30 * time.Second}
	listener, err := lc.Listen(ctx, "tcp", s.tcpAddr)
	if err != nil {
		return err
	}
	log.Printf("[TCP] Listening on %s", s.tcpAddr)

	go func() {
		<-ctx.Done()
		listener.Close()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				continue
			}
		}

		atomic.AddInt32(&s.stats.ConnsActive, 1)
		atomic.AddInt64(&s.stats.ConnsTotal, 1)

		if tcp, ok := conn.(*net.TCPConn); ok {
			tcp.SetNoDelay(true)
			tcp.SetKeepAlive(true)
			tcp.SetKeepAlivePeriod(15 * time.Second)
			tcp.SetReadBuffer(512 * 1024)
			tcp.SetWriteBuffer(512 * 1024)
		}

		go s.handleTCP(conn)
	}
}

func (s *Server) handleTCP(client net.Conn) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[TCP] Panic: %v", r)
		}
	}()
	defer client.Close()
	defer atomic.AddInt32(&s.stats.ConnsActive, -1)

	client.SetDeadline(time.Now().Add(60 * time.Second))

	// Read protocol header
	buf := make([]byte, 4096)
	n, err := client.Read(buf)
	if err != nil {
		return
	}

	peek := buf[:n]

	// Auto-detect protocol
	// SOCKS5
	if len(peek) > 0 && peek[0] == 5 {
		host, err := s.handleSOCKS5(client, peek)
		if err != nil {
			return
		}
		if host == "" {
			return
		}

		target, err := net.DialTimeout("tcp", host, 10*time.Second)
		if err != nil {
			return
		}
		defer target.Close()
		s.applyTCPOpts(target)

		client.SetDeadline(time.Time{})
		n1, n2 := s.pool.Relay(client, target)
		atomic.AddInt64(&s.stats.BytesIn, n1)
		atomic.AddInt64(&s.stats.BytesOut, n2)
		return
	}

	// HTTP CONNECT
	if len(peek) >= 7 && string(peek[:7]) == "CONNECT" {
		// Extract host
		host := extractCONNECTHost(peek)
		if host == "" {
			return
		}

		target, err := net.DialTimeout("tcp", host, 10*time.Second)
		if err != nil {
			return
		}
		defer target.Close()
		s.applyTCPOpts(target)

		client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
		client.SetDeadline(time.Time{})

		n1, n2 := s.pool.Relay(client, target)
		atomic.AddInt64(&s.stats.BytesIn, n1)
		atomic.AddInt64(&s.stats.BytesOut, n2)
		return
	}

	// Regular HTTP proxy - extract Host header
	host := extractHTTPHost(peek)
	if host != "" {
		target, err := net.DialTimeout("tcp", host, 10*time.Second)
		if err != nil {
			return
		}
		defer target.Close()
		s.applyTCPOpts(target)

		target.Write(peek)
		client.SetDeadline(time.Time{})

		n1, n2 := s.pool.Relay(client, target)
		atomic.AddInt64(&s.stats.BytesIn, n1)
		atomic.AddInt64(&s.stats.BytesOut, n2)
		return
	}
}

func (s *Server) handleSOCKS5(client net.Conn, initial []byte) (string, error) {
	// Parse SOCKS5 hello
	if len(initial) < 3 {
		return "", fmt.Errorf("short SOCKS5")
	}
	nmethods := int(initial[1])
	expected := 2 + nmethods
	if len(initial) < expected {
		return "", fmt.Errorf("incomplete hello")
	}

	// No auth
	client.Write([]byte{0x05, 0x00})

	// Read connect request
	reqBuf := make([]byte, 4)
	_, err := io.ReadFull(client, reqBuf)
	if err != nil {
		return "", err
	}

	ver := reqBuf[0]
	cmd := reqBuf[1]
	atyp := reqBuf[3]

	if ver != 5 || cmd != 1 {
		client.Write([]byte{0x05, 0x07, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
		return "", fmt.Errorf("unsupported cmd")
	}

	var host string
	var port uint16

	switch atyp {
	case 1: // IPv4
		b := make([]byte, 4)
		io.ReadFull(client, b)
		host = net.IP(b).String()
	case 3: // Domain
		b := make([]byte, 1)
		io.ReadFull(client, b)
		l := int(b[0])
		b2 := make([]byte, l)
		io.ReadFull(client, b2)
		host = string(b2)
	case 4: // IPv6
		b := make([]byte, 16)
		io.ReadFull(client, b)
		host = net.IP(b).String()
	default:
		return "", fmt.Errorf("unknown atyp")
	}

	pb := make([]byte, 2)
	io.ReadFull(client, pb)
	port = binary.BigEndian.Uint16(pb)

	targetAddr := fmt.Sprintf("%s:%d", host, port)

	// Send success
	localIP := net.ParseIP("0.0.0.0").To4()
	resp := []byte{0x05, 0x00, 0x00, 0x01, localIP[0], localIP[1], localIP[2], localIP[3], byte(port >> 8), byte(port & 0xff)}
	client.Write(resp)

	return targetAddr, nil
}

func (s *Server) applyTCPOpts(conn net.Conn) {
	if tcp, ok := conn.(*net.TCPConn); ok {
		tcp.SetNoDelay(true)
		tcp.SetKeepAlive(true)
		tcp.SetKeepAlivePeriod(15 * time.Second)
		tcp.SetReadBuffer(512 * 1024)
		tcp.SetWriteBuffer(512 * 1024)
	}
}

func (s *Server) startUDP(ctx context.Context) error {
	pc, err := net.ListenPacket("udp", s.udpAddr)
	if err != nil {
		return err
	}
	log.Printf("[UDP] Listening on %s", s.udpAddr)

	go func() {
		<-ctx.Done()
		pc.Close()
	}()

	buf := make([]byte, 65507)
	sessions := make(map[string]*net.UDPConn)
	var mu sync.Mutex

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		pc.SetDeadline(time.Now().Add(2 * time.Second))
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			select {
			case <-ctx.Done():
				return nil
			default:
				continue
			}
		}

		key := addr.String()
		mu.Lock()
		session, exists := sessions[key]
		mu.Unlock()

		if exists {
			session.Write(buf[:n])
			atomic.AddInt64(&s.stats.BytesOut, int64(n))
			continue
		}

		targetAddr, _ := net.ResolveUDPAddr("udp", "1.1.1.1:53")
		conn, err := net.DialUDP("udp", nil, targetAddr)
		if err != nil {
			continue
		}
		conn.Write(buf[:n])

		atomic.AddInt64(&s.stats.BytesOut, int64(n))
		atomic.AddInt32(&s.stats.ConnsActive, 1)
		atomic.AddInt64(&s.stats.ConnsTotal, 1)

		mu.Lock()
		sessions[key] = conn
		mu.Unlock()

		go func(k string, c *net.UDPConn) {
			defer func() { recover() }()
			b := make([]byte, 65507)
			for {
				c.SetDeadline(time.Now().Add(2 * time.Minute))
				rn, err := c.Read(b)
				if err != nil {
					mu.Lock()
					delete(sessions, k)
					mu.Unlock()
					c.Close()
					atomic.AddInt32(&s.stats.ConnsActive, -1)
					return
				}
				pc.WriteTo(b[:rn], addr)
				atomic.AddInt64(&s.stats.BytesIn, int64(rn))
			}
		}(key, conn)
	}
}

func extractCONNECTHost(data []byte) string {
	// Format: CONNECT host:port HTTP/1.1
	s := string(data)
	for i := 7; i < len(s); i++ {
		if s[i] == ' ' {
			return s[8:i]
		}
	}
	return ""
}

func extractHTTPHost(data []byte) string {
	s := string(data)
	lines := splitLines(s)
	for _, line := range lines {
		if len(line) > 6 && toLower(line[:6]) == "host: " {
			host := line[6:]
			if idx := indexOf(host, "\r"); idx != -1 {
				host = host[:idx]
			}
			return host
		}
	}
	return ""
}

func splitLines(s string) []string {
	var lines []string
	current := ""
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, current)
			current = ""
		} else if s[i] != '\r' {
			current += string(s[i])
		}
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}

func toLower(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 32
		}
	}
	return string(b)
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
