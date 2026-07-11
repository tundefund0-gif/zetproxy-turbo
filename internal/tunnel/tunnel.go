package tunnel

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/user/zetproxy/internal/pool"
)

// Protocol represents the proxy protocol
type Protocol int

const (
	ProtocolTCP  Protocol = iota
	ProtocolUDP
)

// Stats tracks tunnel performance
type Stats struct {
	BytesIn      int64
	BytesOut     int64
	ConnsTotal   int64
	StartTime    int64
	ThroughputIn float64
	ConnsActive  int32
}

// Server is the ultra-fast tunnel
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

		// Apply TCP optimizations
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
			log.Printf("[TCP] Recovered: %v", r)
		}
	}()
	defer client.Close()
	defer atomic.AddInt32(&s.stats.ConnsActive, -1)

	client.SetDeadline(time.Now().Add(60 * time.Second))

	// Read protocol header to determine destination
	buf := make([]byte, 1024)
	n, err := client.Read(buf)
	if err != nil {
		return
	}

	// Parse target from HTTP CONNECT or SOCKS5
	host, proxyType := parseTarget(buf[:n])
	if host == "" {
		return
	}

	// Connect with optimizations
	target, err := net.DialTimeout("tcp", host, 5*time.Second)
	if err != nil {
		return
	}
	defer target.Close()

	if tcp, ok := target.(*net.TCPConn); ok {
		tcp.SetNoDelay(true)
		tcp.SetKeepAlive(true)
		tcp.SetKeepAlivePeriod(15 * time.Second)
		tcp.SetReadBuffer(512 * 1024)
		tcp.SetWriteBuffer(512 * 1024)
	}

	client.SetDeadline(time.Time{})

	// For HTTP CONNECT, send 200
	if proxyType == "CONNECT" {
		client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	}

	// For HTTP, forward the initial data
	if proxyType == "HTTP" {
		target.Write(buf[:n])
	}

	// Bidirectional zero-copy relay
	atomic.AddInt64(&s.stats.BytesIn, 0) // placeholder for real counting
	n1, n2 := s.pool.Relay(client, target)
	atomic.AddInt64(&s.stats.BytesIn, n1)
	atomic.AddInt64(&s.stats.BytesOut, n2)
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

		// Resolve destination
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

func parseTarget(data []byte) (string, string) {
	if len(data) < 4 {
		return "", ""
	}

	// HTTP CONNECT
	if len(data) >= 7 && string(data[:7]) == "CONNECT" {
		end := len(data)
		for i := 7; i < len(data); i++ {
			if data[i] == ' ' || data[i] == '\r' || data[i] == '\n' {
				end = i
				break
			}
		}
		return string(data[8:end]), "CONNECT"
	}

	// SOCKS5
	if data[0] == 5 {
		return "", "" // Would need full handshake
	}

	// Regular HTTP - extract Host header
	if len(data) >= 4 && string(data[:4]) == "GET " || string(data[:4]) == "POST" {
		lines := string(data)
		for _, line := range splitLines(lines) {
			if len(line) > 6 && toLower(line[:6]) == "host: " {
				host := line[6:]
				if idx := indexOf(host, "\r"); idx != -1 {
					host = host[:idx]
				}
				return host, "HTTP"
			}
		}
	}

	return "", ""
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
