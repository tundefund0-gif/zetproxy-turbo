package pool

import (
	"io"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
)

const (
	BufferSize     = 64 * 1024
	SmallBufSize   = 4 * 1024
	LargeBufSize   = 256 * 1024
	WarmupBuffers  = 128
)

var globalStats PoolStats

type PoolStats struct {
	Allocated int64 `json:"allocated"`
	Reused    int64 `json:"reused"`
	PutBack   int64 `json:"put_back"`
	Dropped   int64 `json:"dropped"`
}

type BufferPool struct {
	small  sync.Pool
	medium sync.Pool
	large  sync.Pool
	stats  *PoolStats
}

func New() *BufferPool {
	bp := &BufferPool{stats: &globalStats}

	bp.small = sync.Pool{
		New: func() interface{} {
			atomic.AddInt64(&bp.stats.Allocated, 1)
			b := make([]byte, SmallBufSize)
			return &b
		},
	}
	bp.medium = sync.Pool{
		New: func() interface{} {
			atomic.AddInt64(&bp.stats.Allocated, 1)
			b := make([]byte, BufferSize)
			return &b
		},
	}
	bp.large = sync.Pool{
		New: func() interface{} {
			atomic.AddInt64(&bp.stats.Allocated, 1)
			b := make([]byte, LargeBufSize)
			return &b
		},
	}

	for i := 0; i < WarmupBuffers; i++ {
		bp.small.Put(bp.small.Get())
		bp.medium.Put(bp.medium.Get())
		bp.large.Put(bp.large.Get())
	}

	return bp
}

func (bp *BufferPool) Get() []byte {
	atomic.AddInt64(&bp.stats.Reused, 1)
	return *(bp.medium.Get().(*[]byte))
}

func (bp *BufferPool) Put(b []byte) {
	if b == nil {
		return
	}
	switch {
	case cap(b) >= LargeBufSize:
		bp.large.Put(&b)
	case cap(b) >= BufferSize:
		bp.medium.Put(&b)
	case cap(b) >= SmallBufSize:
		bp.small.Put(&b)
	default:
		atomic.AddInt64(&bp.stats.Dropped, 1)
		return
	}
	atomic.AddInt64(&bp.stats.PutBack, 1)
}

func (bp *BufferPool) GetSmall() []byte {
	atomic.AddInt64(&bp.stats.Reused, 1)
	return *(bp.small.Get().(*[]byte))
}

func (bp *BufferPool) GetLarge() []byte {
	atomic.AddInt64(&bp.stats.Reused, 1)
	return *(bp.large.Get().(*[]byte))
}

func (bp *BufferPool) PutSmall(b []byte) {
	if b == nil {
		return
	}
	bp.small.Put(&b)
	atomic.AddInt64(&bp.stats.PutBack, 1)
}

func (bp *BufferPool) PutLarge(b []byte) {
	if b == nil {
		return
	}
	bp.large.Put(&b)
	atomic.AddInt64(&bp.stats.PutBack, 1)
}

func (bp *BufferPool) GetStats() PoolStats {
	return PoolStats{
		Allocated: atomic.LoadInt64(&bp.stats.Allocated),
		Reused:    atomic.LoadInt64(&bp.stats.Reused),
		PutBack:   atomic.LoadInt64(&bp.stats.PutBack),
		Dropped:   atomic.LoadInt64(&bp.stats.Dropped),
	}
}

func (bp *BufferPool) Relay(conn1, conn2 net.Conn) (int64, int64) {
	var wg sync.WaitGroup
	wg.Add(2)
	var n1, n2 int64

	go func() {
		defer wg.Done()
		buf := bp.GetLarge()
		defer bp.PutLarge(buf)
		n1, _ = io.CopyBuffer(conn2, conn1, buf)
		closeWrite(conn2)
	}()

	go func() {
		defer wg.Done()
		buf := bp.GetLarge()
		defer bp.PutLarge(buf)
		n2, _ = io.CopyBuffer(conn1, conn2, buf)
		closeWrite(conn1)
	}()

	wg.Wait()
	return n1, n2
}

func closeWrite(conn net.Conn) {
	if tc, ok := conn.(*net.TCPConn); ok {
		tc.CloseWrite()
	}
}

func init() {
	runtime.GOMAXPROCS(runtime.NumCPU())
}
