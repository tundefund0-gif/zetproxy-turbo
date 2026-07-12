package proxy

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/user/zetproxy/internal/pool"
)

type SOCKS5Stats struct {
	BytesIn       int64   `json:"bytes_in"`
	BytesOut      int64   `json:"bytes_out"`
	ConnsTotal    int64   `json:"conns_total"`
	ConnsFailed   int64   `json:"conns_failed"`
	ConnsRejected int64   `json:"conns_rejected"`
	StartTime     int64   `json:"start_time"`
	ThroughputIn  float64 `json:"throughput_in_mbps"`
	ThroughputOut float64 `json:"throughput_out_mbps"`
	BytesInPrev   int64
	BytesOutPrev  int64
	LastCalcTime  time.Time
	ConnsActive   int32 `json:"conns_active"`
	MemAlloc      uint32 `json:"mem_alloc_mb"`
	MemSys        uint32 `json:"mem_sys_mb"`
	NumGoroutine  int    `json:"num_goroutine"`
}

type SOCKS5Server struct {
	addr       string
	stats      SOCKS5Stats
	pool       *pool.BufferPool
	mu         sync.RWMutex
	maxConns   int32
	connSem    chan struct{}
	listener   net.Listener
	ctx        context.Context
	cancel     context.CancelFunc
	connLog    []ConnLog
	connLogMu  sync.Mutex
	maxLogSize int
	lastCalcTime time.Time
}

type ConnLog struct {
	Timestamp time.Time `json:"timestamp"`
	Addr      string    `json:"addr"`
	Target    string    `json:"target"`
	Status    string    `json:"status"`
	Bytes     int64     `json:"bytes"`
}

func NewSOCKS5Server(addr string) *SOCKS5Server {
	s := &SOCKS5Server{
		addr:         addr,
		pool:         pool.New(),
		maxConns:     2048,
		maxLogSize:   1000,
		lastCalcTime: time.Now(),
		stats: SOCKS5Stats{
			StartTime: time.Now().Unix(),
		},
	}
	s.connSem = make(chan struct{}, s.maxConns)
	return s
}

func (s *SOCKS5Server) SetMaxConns(max int32) {
	s.maxConns = max
	s.connSem = make(chan struct{}, max)
}

func (s *SOCKS5Server) GetStats() SOCKS5Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now()
	elapsed := now.Sub(s.lastCalcTime).Seconds()

	if elapsed >= 0.5 {
		in := atomic.LoadInt64(&s.stats.BytesIn)
		out := atomic.LoadInt64(&s.stats.BytesOut)
		s.stats.ThroughputIn = (float64(in-s.stats.BytesInPrev) * 8) / (elapsed * 1000 * 1000)
		s.stats.ThroughputOut = (float64(out-s.stats.BytesOutPrev) * 8) / (elapsed * 1000 * 1000)
		s.stats.BytesInPrev = in
		s.stats.BytesOutPrev = out
		s.lastCalcTime = now
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	s.stats.MemAlloc = uint32(m.Alloc / 1024 / 1024)
	s.stats.MemSys = uint32(m.Sys / 1024 / 1024)
	s.stats.NumGoroutine = runtime.NumGoroutine()

	return SOCKS5Stats{
		BytesIn:       atomic.LoadInt64(&s.stats.BytesIn),
		BytesOut:      atomic.LoadInt64(&s.stats.BytesOut),
		ConnsActive:   atomic.LoadInt32(&s.stats.ConnsActive),
		ConnsTotal:    atomic.LoadInt64(&s.stats.ConnsTotal),
		ConnsFailed:   atomic.LoadInt64(&s.stats.ConnsFailed),
		ConnsRejected: atomic.LoadInt64(&s.stats.ConnsRejected),
		StartTime:     s.stats.StartTime,
		ThroughputIn:  s.stats.ThroughputIn,
		ThroughputOut: s.stats.ThroughputOut,
		MemAlloc:      s.stats.MemAlloc,
		MemSys:        s.stats.MemSys,
		NumGoroutine:  s.stats.NumGoroutine,
	}
}

func (s *SOCKS5Server) GetConnLogs() []ConnLog {
	s.connLogMu.Lock()
	defer s.connLogMu.Unlock()
	logs := make([]ConnLog, len(s.connLog))
	copy(logs, s.connLog)
	return logs
}

func (s *SOCKS5Server) addConnLog(entry ConnLog) {
	s.connLogMu.Lock()
	defer s.connLogMu.Unlock()
	s.connLog = append(s.connLog, entry)
	if len(s.connLog) > s.maxLogSize {
		s.connLog = s.connLog[len(s.connLog)-s.maxLogSize:]
	}
}

func (s *SOCKS5Server) Start(ctx context.Context) error {
	s.ctx, s.cancel = context.WithCancel(ctx)
	lc := net.ListenConfig{KeepAlive: 30 * time.Second}
	listener, err := lc.Listen(ctx, "tcp", s.addr)
	if err != nil {
		return fmt.Errorf("SOCKS5 listen on %s: %w", s.addr, err)
	}
	s.listener = listener
	log.Printf("[SOCKS5] Listening on %s (max conns: %d)", s.addr, s.maxConns)

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

		if tcp, ok := conn.(*net.TCPConn); ok {
			tcp.SetNoDelay(true)
			tcp.SetKeepAlive(true)
			tcp.SetKeepAlivePeriod(15 * time.Second)
			tcp.SetReadBuffer(256 * 1024)
			tcp.SetWriteBuffer(256 * 1024)
		}

		go s.handle(conn)
	}
}

func (s *SOCKS5Server) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	if s.listener != nil {
		s.listener.Close()
	}
}

func (s *SOCKS5Server) handle(conn net.Conn) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[SOCKS5] Panic recovered: %v", r)
			atomic.AddInt64(&s.stats.ConnsFailed, 1)
		}
	}()
	defer func() {
		<-s.connSem
		conn.Close()
		atomic.AddInt32(&s.stats.ConnsActive, -1)
	}()

	conn.SetDeadline(time.Now().Add(60 * time.Second))

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

	if _, err := conn.Write([]byte{0x05, 0x00}); err != nil {
		return
	}

	_, err = io.ReadFull(conn, buf[:4])
	if err != nil {
		atomic.AddInt64(&s.stats.ConnsFailed, 1)
		return
	}

	cmd := buf[1]
	atyp := buf[3]

	if cmd != 1 {
		conn.Write([]byte{0x05, 0x07, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
		atomic.AddInt64(&s.stats.ConnsFailed, 1)
		return
	}

	var host string
	var port uint16

	switch atyp {
	case 1:
		_, err = io.ReadFull(conn, buf[:4])
		if err != nil {
			return
		}
		host = net.IP(buf[:4]).String()
	case 3:
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
	case 4:
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

	target, err := net.DialTimeout("tcp", targetAddr, 10*time.Second)
	if err != nil {
		conn.Write([]byte{0x05, 0x04, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
		atomic.AddInt64(&s.stats.ConnsFailed, 1)
		s.addConnLog(ConnLog{
			Timestamp: time.Now(),
			Addr:      conn.RemoteAddr().String(),
			Target:    targetAddr,
			Status:    "failed",
		})
		return
	}
	defer target.Close()

	if tcp, ok := target.(*net.TCPConn); ok {
		tcp.SetNoDelay(true)
		tcp.SetKeepAlive(true)
		tcp.SetKeepAlivePeriod(15 * time.Second)
		tcp.SetReadBuffer(256 * 1024)
		tcp.SetWriteBuffer(256 * 1024)
	}

	localAddr := target.LocalAddr().(*net.TCPAddr)
	resp := make([]byte, 10)
	resp[0] = 0x05
	resp[1] = 0x00
	resp[2] = 0x00
	resp[3] = 0x01
	copy(resp[4:8], localAddr.IP.To4())
	binary.BigEndian.PutUint16(resp[8:10], uint16(localAddr.Port))
	if _, err := conn.Write(resp); err != nil {
		return
	}

	conn.SetDeadline(time.Time{})

	n1, n2 := s.pool.Relay(conn, target)
	atomic.AddInt64(&s.stats.BytesIn, n1)
	atomic.AddInt64(&s.stats.BytesOut, n2)

	s.addConnLog(ConnLog{
		Timestamp: time.Now(),
		Addr:      conn.RemoteAddr().String(),
		Target:    targetAddr,
		Status:    "connected",
		Bytes:     n1 + n2,
	})
}
