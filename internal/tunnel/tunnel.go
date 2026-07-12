package tunnel

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/user/zetproxy/internal/pool"
)

type Stats struct {
	BytesIn        int64   `json:"bytes_in"`
	BytesOut       int64   `json:"bytes_out"`
	ConnsTotal     int64   `json:"conns_total"`
	ConnsActive    int32   `json:"conns_active"`
	ConnsRejected  int64   `json:"conns_rejected"`
	ConnsFailed    int64   `json:"conns_failed"`
	StartTime      int64   `json:"start_time"`
	ThroughputIn   float64 `json:"throughput_in_mbps"`
	ThroughputOut  float64 `json:"throughput_out_mbps"`
	BytesInPrev    int64
	BytesOutPrev   int64
	LastCalcTime   time.Time
	MemAlloc       uint32 `json:"mem_alloc_mb"`
	MemSys         uint32 `json:"mem_sys_mb"`
	NumGoroutine   int    `json:"num_goroutine"`
	TCPAccepts     int64  `json:"tcp_accepts"`
	UDPPackets     int64  `json:"udp_packets"`
}

type Server struct {
	tcpAddr     string
	udpAddr     string
	stats       Stats
	pool        *pool.BufferPool
	mu          sync.RWMutex
	lastIn      int64
	lastOut     int64
	lastTime    time.Time
	maxConns    int32
	connSem     chan struct{}
	tcpListener net.Listener
	udpConn     net.PacketConn
	ctx         context.Context
	cancel      context.CancelFunc
}

func NewServer(tcpAddr, udpAddr string) *Server {
	s := &Server{
		tcpAddr:  tcpAddr,
		udpAddr:  udpAddr,
		pool:     pool.New(),
		lastTime: time.Now(),
		maxConns: 4096,
		stats: Stats{
			StartTime: time.Now().Unix(),
		},
	}
	s.connSem = make(chan struct{}, s.maxConns)
	return s
}

func (s *Server) SetMaxConns(max int32) {
	s.maxConns = max
	s.connSem = make(chan struct{}, max)
}

func (s *Server) GetStats() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now()
	elapsed := now.Sub(s.lastTime).Seconds()

	if elapsed >= 0.5 {
		in := atomic.LoadInt64(&s.stats.BytesIn)
		out := atomic.LoadInt64(&s.stats.BytesOut)
		s.stats.ThroughputIn = (float64(in-s.lastIn) * 8) / (elapsed * 1000 * 1000)
		s.stats.ThroughputOut = (float64(out-s.lastOut) * 8) / (elapsed * 1000 * 1000)
		s.lastIn = in
		s.lastOut = out
		s.lastTime = now
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	s.stats.MemAlloc = uint32(m.Alloc / 1024 / 1024)
	s.stats.MemSys = uint32(m.Sys / 1024 / 1024)
	s.stats.NumGoroutine = runtime.NumGoroutine()

	return Stats{
		BytesIn:       atomic.LoadInt64(&s.stats.BytesIn),
		BytesOut:      atomic.LoadInt64(&s.stats.BytesOut),
		ConnsTotal:    atomic.LoadInt64(&s.stats.ConnsTotal),
		ConnsActive:   atomic.LoadInt32(&s.stats.ConnsActive),
		ConnsRejected: atomic.LoadInt64(&s.stats.ConnsRejected),
		ConnsFailed:   atomic.LoadInt64(&s.stats.ConnsFailed),
		StartTime:     s.stats.StartTime,
		ThroughputIn:  s.stats.ThroughputIn,
		ThroughputOut: s.stats.ThroughputOut,
		MemAlloc:      s.stats.MemAlloc,
		MemSys:        s.stats.MemSys,
		NumGoroutine:  s.stats.NumGoroutine,
		TCPAccepts:    atomic.LoadInt64(&s.stats.TCPAccepts),
		UDPPackets:    atomic.LoadInt64(&s.stats.UDPPackets),
	}
}

func (s *Server) Start(ctx context.Context) error {
	s.ctx, s.cancel = context.WithCancel(ctx)
	errCh := make(chan error, 2)

	go func() {
		if err := s.startTCP(s.ctx); err != nil {
			errCh <- fmt.Errorf("TCP: %w", err)
		}
	}()

	go func() {
		if err := s.startUDP(s.ctx); err != nil {
			errCh <- fmt.Errorf("UDP: %w", err)
		}
	}()

	return <-errCh
}

func (s *Server) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	if s.tcpListener != nil {
		s.tcpListener.Close()
	}
	if s.udpConn != nil {
		s.udpConn.Close()
	}
}

func (s *Server) startTCP(ctx context.Context) error {
	lc := net.ListenConfig{
		KeepAlive: 30 * time.Second,
		Control:   nil,
	}
	listener, err := lc.Listen(ctx, "tcp", s.tcpAddr)
	if err != nil {
		return err
	}
	s.tcpListener = listener
	log.Printf("[TCP] Listening on %s (max conns: %d)", s.tcpAddr, s.maxConns)

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

		select {
		case s.connSem <- struct{}{}:
		default:
			atomic.AddInt64(&s.stats.ConnsRejected, 1)
			conn.Close()
			continue
		}

		atomic.AddInt32(&s.stats.ConnsActive, 1)
		atomic.AddInt64(&s.stats.ConnsTotal, 1)
		atomic.AddInt64(&s.stats.TCPAccepts, 1)

		if tcp, ok := conn.(*net.TCPConn); ok {
			tcp.SetNoDelay(true)
			tcp.SetKeepAlive(true)
			tcp.SetKeepAlivePeriod(15 * time.Second)
			tcp.SetReadBuffer(256 * 1024)
			tcp.SetWriteBuffer(256 * 1024)
		}

		go s.handleTCP(conn)
	}
}

func (s *Server) handleTCP(client net.Conn) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[TCP] Panic recovered: %v", r)
			atomic.AddInt64(&s.stats.ConnsFailed, 1)
		}
	}()
	defer func() {
		<-s.connSem
		client.Close()
		atomic.AddInt32(&s.stats.ConnsActive, -1)
	}()

	client.SetDeadline(time.Now().Add(120 * time.Second))

	buf := s.pool.GetSmall()
	defer s.pool.PutSmall(buf)

	n, err := client.Read(buf)
	if err != nil || n == 0 {
		return
	}

	peek := buf[:n]

	switch {
	case len(peek) > 0 && peek[0] == 5:
		s.handleSOCKS5(client, peek)
	case len(peek) >= 7 && string(peek[:7]) == "CONNECT":
		s.handleHTTPConnect(client, peek)
	case len(peek) >= 4 && string(peek[:4]) == "GET " || string(peek[:4]) == "POST" || string(peek[:4]) == "PUT " || string(peek[:4]) == "HEAD":
		s.handleHTTPProxy(client, peek)
	case len(peek) >= 2 && peek[0] == 0 && peek[1] == 0:
		s.handleRawProxy(client, peek)
	default:
		atomic.AddInt64(&s.stats.ConnsFailed, 1)
	}
}

func (s *Server) handleSOCKS5(client net.Conn, initial []byte) {
	if len(initial) < 3 {
		return
	}
	nmethods := int(initial[1])
	expected := 2 + nmethods
	if len(initial) < expected {
		return
	}

	if _, err := client.Write([]byte{0x05, 0x00}); err != nil {
		return
	}

	reqBuf := make([]byte, 4)
	if _, err := io.ReadFull(client, reqBuf); err != nil {
		return
	}

	ver, cmd, _, atyp := reqBuf[0], reqBuf[1], reqBuf[2], reqBuf[3]
	if ver != 5 || cmd != 1 {
		client.Write([]byte{0x05, 0x07, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
		return
	}

	var host string
	var port uint16

	switch atyp {
	case 1:
		b := make([]byte, 4)
		if _, err := io.ReadFull(client, b); err != nil {
			return
		}
		host = net.IP(b).String()
	case 3:
		b := make([]byte, 1)
		if _, err := io.ReadFull(client, b); err != nil {
			return
		}
		l := int(b[0])
		b2 := make([]byte, l)
		if _, err := io.ReadFull(client, b2); err != nil {
			return
		}
		host = string(b2)
	case 4:
		b := make([]byte, 16)
		if _, err := io.ReadFull(client, b); err != nil {
			return
		}
		host = net.IP(b).String()
	default:
		return
	}

	pb := make([]byte, 2)
	if _, err := io.ReadFull(client, pb); err != nil {
		return
	}
	port = binary.BigEndian.Uint16(pb)

	targetAddr := fmt.Sprintf("%s:%d", host, port)

	target, err := net.DialTimeout("tcp", targetAddr, 10*time.Second)
	if err != nil {
		client.Write([]byte{0x05, 0x04, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
		atomic.AddInt64(&s.stats.ConnsFailed, 1)
		return
	}
	defer target.Close()

	s.applyTCPOpts(target)

	localAddr := target.LocalAddr().(*net.TCPAddr)
	resp := []byte{0x05, 0x00, 0x00, 0x01}
	resp = append(resp, localAddr.IP.To4()...)
	resp = append(resp, byte(port>>8), byte(port&0xff))
	if _, err := client.Write(resp); err != nil {
		return
	}

	client.SetDeadline(time.Time{})
	s.relayWithStats(client, target)
}

func (s *Server) handleHTTPConnect(client net.Conn, initial []byte) {
	host := extractCONNECTHost(initial)
	if host == "" {
		return
	}

	if !strings.Contains(host, ":") {
		host = host + ":443"
	}

	target, err := net.DialTimeout("tcp", host, 10*time.Second)
	if err != nil {
		return
	}
	defer target.Close()

	s.applyTCPOpts(target)

	if _, err := client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return
	}

	client.SetDeadline(time.Time{})
	s.relayWithStats(client, target)
}

func (s *Server) handleHTTPProxy(client net.Conn, initial []byte) {
	host := extractHTTPHost(initial)
	if host == "" {
		return
	}

	if !strings.Contains(host, ":") {
		host = host + ":80"
	}

	target, err := net.DialTimeout("tcp", host, 10*time.Second)
	if err != nil {
		return
	}
	defer target.Close()

	s.applyTCPOpts(target)

	if _, err := target.Write(initial); err != nil {
		return
	}

	client.SetDeadline(time.Time{})
	s.relayWithStats(client, target)
}

func (s *Server) handleRawProxy(client net.Conn, initial []byte) {
	buf := make([]byte, 256)
	n := copy(buf, initial)

	target, err := net.DialTimeout("tcp", string(buf[:n]), 10*time.Second)
	if err != nil {
		return
	}
	defer target.Close()

	s.applyTCPOpts(target)
	client.SetDeadline(time.Time{})
	s.relayWithStats(client, target)
}

func (s *Server) relayWithStats(client, target net.Conn) {
	n1, n2 := s.pool.Relay(client, target)
	atomic.AddInt64(&s.stats.BytesIn, n1)
	atomic.AddInt64(&s.stats.BytesOut, n2)
}

func (s *Server) applyTCPOpts(conn net.Conn) {
	if tcp, ok := conn.(*net.TCPConn); ok {
		tcp.SetNoDelay(true)
		tcp.SetKeepAlive(true)
		tcp.SetKeepAlivePeriod(15 * time.Second)
		tcp.SetReadBuffer(256 * 1024)
		tcp.SetWriteBuffer(256 * 1024)
	}
}

func (s *Server) startUDP(ctx context.Context) error {
	pc, err := net.ListenPacket("udp", s.udpAddr)
	if err != nil {
		return err
	}
	s.udpConn = pc
	log.Printf("[UDP] Listening on %s", s.udpAddr)

	go func() {
		<-ctx.Done()
		pc.Close()
	}()

	buf := make([]byte, 65507)
	sessions := make(map[string]*udpSession)
	var mu sync.Mutex

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				mu.Lock()
				now := time.Now()
				for k, sess := range sessions {
					if now.Sub(sess.lastActivity) > 3*time.Minute {
						sess.conn.Close()
						delete(sessions, k)
						atomic.AddInt32(&s.stats.ConnsActive, -1)
					}
				}
				mu.Unlock()
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		pc.SetReadDeadline(time.Now().Add(1 * time.Second))
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

		atomic.AddInt64(&s.stats.UDPPackets, 1)
		key := addr.String()

		mu.Lock()
		sess, exists := sessions[key]
		mu.Unlock()

		if exists {
			sess.lastActivity = time.Now()
			sess.conn.Write(buf[:n])
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

		newSession := &udpSession{
			conn:         conn,
			remoteAddr:   addr,
			lastActivity: time.Now(),
		}

		mu.Lock()
		sessions[key] = newSession
		mu.Unlock()

		go s.handleUDPSession(pc, newSession, key, &mu, sessions)
	}
}

type udpSession struct {
	conn         *net.UDPConn
	remoteAddr   net.Addr
	lastActivity time.Time
}

func (s *Server) handleUDPSession(pc net.PacketConn, sess *udpSession, key string, mu *sync.Mutex, sessions map[string]*udpSession) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[UDP] Panic: %v", r)
		}
	}()

	b := s.pool.GetLarge()
	defer s.pool.PutLarge(b)

	for {
		sess.conn.SetReadDeadline(time.Now().Add(2 * time.Minute))
		rn, err := sess.conn.Read(b)
		if err != nil {
			mu.Lock()
			delete(sessions, key)
			mu.Unlock()
			sess.conn.Close()
			atomic.AddInt32(&s.stats.ConnsActive, -1)
			return
		}
		pc.WriteTo(b[:rn], sess.remoteAddr)
		atomic.AddInt64(&s.stats.BytesIn, int64(rn))
		sess.lastActivity = time.Now()
	}
}

func extractCONNECTHost(data []byte) string {
	s := string(data)
	idx := strings.Index(s, " ")
	if idx < 0 {
		return ""
	}
	s = s[idx+1:]
	idx = strings.Index(s, " ")
	if idx < 0 {
		return ""
	}
	return s[:idx]
}

func extractHTTPHost(data []byte) string {
	s := string(data)
	lines := strings.Split(s, "\r\n")
	for _, line := range lines {
		if len(line) > 6 && strings.EqualFold(line[:6], "host: ") {
			host := line[6:]
			if idx := strings.Index(host, ":"); idx != -1 {
				return host
			}
			return host
		}
	}
	return ""
}
