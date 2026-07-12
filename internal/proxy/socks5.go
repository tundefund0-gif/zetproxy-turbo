package proxy

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

// SOCKS5Stats tracks SOCKS5 proxy performance
type SOCKS5Stats struct {
	BytesIn     int64 `json:"bytes_in"`
	BytesOut    int64 `json:"bytes_out"`
	ConnsTotal  int64 `json:"conns_total"`
	ConnsFailed int64 `json:"conns_failed"`
	StartTime   int64 `json:"start_time"`
	ConnsActive int32 `json:"conns_active"`
}

// SOCKS5Server is a dedicated SOCKS5 proxy
type SOCKS5Server struct {
	addr  string
	stats SOCKS5Stats
	pool  *pool.BufferPool
	mu    sync.Mutex
}

// NewSOCKS5Server creates a new SOCKS5 proxy server
func NewSOCKS5Server(addr string) *SOCKS5Server {
	return &SOCKS5Server{
		addr: addr,
		pool: pool.New(),
		stats: SOCKS5Stats{
			StartTime: time.Now().Unix(),
		},
	}
}

// GetStats returns current statistics
func (s *SOCKS5Server) GetStats() SOCKS5Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return SOCKS5Stats{
		BytesIn:     atomic.LoadInt64(&s.stats.BytesIn),
		BytesOut:    atomic.LoadInt64(&s.stats.BytesOut),
		ConnsActive: atomic.LoadInt32(&s.stats.ConnsActive),
		ConnsTotal:  atomic.LoadInt64(&s.stats.ConnsTotal),
		ConnsFailed: atomic.LoadInt64(&s.stats.ConnsFailed),
		StartTime:   atomic.LoadInt64(&s.stats.StartTime),
	}
}

// Start begins the SOCKS5 proxy server
func (s *SOCKS5Server) Start(ctx context.Context) error {
	lc := net.ListenConfig{KeepAlive: 30 * time.Second}
	listener, err := lc.Listen(ctx, "tcp", s.addr)
	if err != nil {
		return fmt.Errorf("SOCKS5 listen on %s: %w", s.addr, err)
	}
	log.Printf("[SOCKS5] Listening on %s", s.addr)

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

		go s.handle(conn)
	}
}

func (s *SOCKS5Server) handle(conn net.Conn) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[SOCKS5] Panic: %v", r)
		}
	}()
	defer conn.Close()
	defer atomic.AddInt32(&s.stats.ConnsActive, -1)

	conn.SetDeadline(time.Now().Add(30 * time.Second))

	// Read auth methods
	buf := make([]byte, 257)
	_, err := io.ReadFull(conn, buf[:2])
	if err != nil {
		atomic.AddInt64(&s.stats.ConnsFailed, 1)
		return
	}

	nMethods := int(buf[1])
	if nMethods > 0 {
		_, err = io.ReadFull(conn, buf[:nMethods])
		if err != nil {
			atomic.AddInt64(&s.stats.ConnsFailed, 1)
			return
		}
	}

	// No auth
	conn.Write([]byte{0x05, 0x00})

	// Read request
	_, err = io.ReadFull(conn, buf[:4])
	if err != nil {
		atomic.AddInt64(&s.stats.ConnsFailed, 1)
		return
	}

	cmd := buf[1]
	atyp := buf[3]

	if cmd != 1 { // Only CONNECT
		conn.Write([]byte{0x05, 0x07, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
		atomic.AddInt64(&s.stats.ConnsFailed, 1)
		return
	}

	var host string
	var port uint16

	switch atyp {
	case 1: // IPv4
		_, err = io.ReadFull(conn, buf[:4])
		if err != nil {
			return
		}
		host = net.IP(buf[:4]).String()
	case 3: // Domain
		_, err = io.ReadFull(conn, buf[:1])
		if err != nil {
			return
		}
		domainLen := int(buf[0])
		_, err = io.ReadFull(conn, buf[:domainLen])
		if err != nil {
			return
		}
		host = string(buf[:domainLen])
	case 4: // IPv6
		_, err = io.ReadFull(conn, buf[:16])
		if err != nil {
			return
		}
		host = net.IP(buf[:16]).String()
	default:
		return
	}

	_, err = io.ReadFull(conn, buf[:2])
	if err != nil {
		return
	}
	port = binary.BigEndian.Uint16(buf[:2])

	targetAddr := fmt.Sprintf("%s:%d", host, port)

	// Connect to target
	target, err := net.DialTimeout("tcp", targetAddr, 10*time.Second)
	if err != nil {
		conn.Write([]byte{0x05, 0x04, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
		atomic.AddInt64(&s.stats.ConnsFailed, 1)
		return
	}
	defer target.Close()

	// Apply TCP optimizations
	if tcp, ok := target.(*net.TCPConn); ok {
		tcp.SetNoDelay(true)
		tcp.SetKeepAlive(true)
		tcp.SetKeepAlivePeriod(15 * time.Second)
		tcp.SetReadBuffer(512 * 1024)
		tcp.SetWriteBuffer(512 * 1024)
	}

	// Success response
	localAddr := target.LocalAddr().(*net.TCPAddr)
	resp := make([]byte, 10)
	resp[0] = 0x05
	resp[1] = 0x00
	resp[2] = 0x00
	resp[3] = 0x01
	copy(resp[4:8], localAddr.IP.To4())
	binary.BigEndian.PutUint16(resp[8:10], uint16(localAddr.Port))
	conn.Write(resp)

	conn.SetDeadline(time.Time{})

	// Bidirectional copy
	n1, n2 := s.pool.Relay(conn, target)
	atomic.AddInt64(&s.stats.BytesIn, n1)
	atomic.AddInt64(&s.stats.BytesOut, n2)
}
