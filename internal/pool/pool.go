package pool

import (
	"io"
	"net"
	"sync"
)

const BufferSize = 64 * 1024 // 64KB - optimized for throughput

type BufferPool struct {
	pool sync.Pool
}

func New() *BufferPool {
	return &BufferPool{
		pool: sync.Pool{
			New: func() interface{} {
				b := make([]byte, BufferSize)
				return &b
			},
		},
	}
}

func (bp *BufferPool) Get() []byte {
	return *(bp.pool.Get().(*[]byte))
}

func (bp *BufferPool) Put(b []byte) {
	bp.pool.Put(&b)
}

// Relay copies data bidirectionally between two connections
func (bp *BufferPool) Relay(conn1, conn2 net.Conn) (int64, int64) {
	var wg sync.WaitGroup
	wg.Add(2)
	var n1, n2 int64
	go func() {
		defer wg.Done()
		buf := bp.Get()
		defer bp.Put(buf)
		n1, _ = io.CopyBuffer(conn2, conn1, buf)
		conn2.Close()
	}()
	go func() {
		defer wg.Done()
		buf := bp.Get()
		defer bp.Put(buf)
		n2, _ = io.CopyBuffer(conn1, conn2, buf)
		conn1.Close()
	}()
	wg.Wait()
	return n1, n2
}
