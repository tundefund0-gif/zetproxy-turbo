package main

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	ctrlMu    sync.Mutex
	ctrl      = make(map[string]net.Conn)
	lastPhone string

	waitMu sync.Mutex
	wait   = make(map[string]waitEntry)

	nextID   int
	connCID  int
)

type waitEntry struct {
	client net.Conn
	dataCh chan net.Conn
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("PORT=%s", port)
	l, err := net.Listen("tcp", ":"+port)
	if err != nil {
		log.Fatalf("Listen :%s: %v", port, err)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() { <-sig; l.Close(); os.Exit(0) }()

	log.Printf("Relay on :%s", port)
	for {
		conn, err := l.Accept()
		if err != nil {
			continue
		}
		go safeHandle(conn)
	}
}

func safeHandle(conn net.Conn) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("Panic: %v", r)
		}
		conn.Close()
	}()
	handle(conn)
}

func handle(conn net.Conn) {
	conn.SetDeadline(time.Now().Add(15 * time.Second))

	r := bufio.NewReaderSize(conn, 65536)
	b, err := r.Peek(1)
	if err != nil {
		return
	}

	if b[0] == 0x05 {
		handleSOCKS5(conn, r)
		return
	}

	line, err := r.ReadString('\n')
	if err != nil {
		return
	}
	line = strings.TrimRight(line, "\r\n")

	switch {

	case line == "register":
		conn.SetDeadline(time.Time{})
		ctrlMu.Lock()
		nextID++
		id := fmt.Sprintf("p%d", nextID)
		ctrl[id] = conn
		lastPhone = id
		ctrlMu.Unlock()
		fmt.Fprintf(conn, "id:%s\n", id)
		log.Printf("[%s] Register", id)
		b := make([]byte, 1)
		for {
			_, err := conn.Read(b)
			if err != nil {
				break
			}
		}
		ctrlMu.Lock()
		delete(ctrl, id)
		if lastPhone == id {
			lastPhone = ""
		}
		ctrlMu.Unlock()

	case strings.HasPrefix(line, "connect:"):
		id := line[8:]
		if id == "" {
			return
		}
		fmt.Fprintf(conn, "ready\n")
		conn.SetDeadline(time.Time{})

		ch := make(chan net.Conn, 1)
		waitMu.Lock()
		wait[id] = waitEntry{client: conn, dataCh: ch}
		waitMu.Unlock()

		ctrlMu.Lock()
		sc, ok := ctrl[id]
		ctrlMu.Unlock()
		if ok {
			sc.Write([]byte("conn\n"))
		}

		select {
		case data := <-ch:
			setNoDelay(conn, data)
			pipe(conn, r, data, bufio.NewReaderSize(data, 65536))
		case <-time.After(60 * time.Second):
		}

	case strings.HasPrefix(line, "data:"):
		id := line[5:]
		fmt.Fprintf(conn, "ready\n")
		conn.SetDeadline(time.Time{})

		waitMu.Lock()
		entry, ok := wait[id]
		if ok {
			delete(wait, id)
		}
		waitMu.Unlock()
		if ok {
			entry.dataCh <- conn
		}

	case line == "ping":
		fmt.Fprintf(conn, "pong\n")

	default:
		fmt.Fprintf(conn, "relay\n")
	}
}

func handleSOCKS5(conn net.Conn, r *bufio.Reader) {
	conn.SetDeadline(time.Now().Add(15 * time.Second))

	// Read handshake: version(1) nmethods(1) methods(nmethods)
	hdr := make([]byte, 2)
	_, err := io.ReadFull(r, hdr)
	if err != nil {
		return
	}
	nmethods := int(hdr[1])
	if nmethods > 255 {
		return
	}
	_, err = io.ReadFull(r, make([]byte, nmethods))
	if err != nil {
		return
	}
	// Reply: no auth
	conn.Write([]byte{0x05, 0x00})

	// Read request: ver(1) cmd(1) rsv(1) atyp(1) dst.addr(varies) dst.port(2)
	reqHdr := make([]byte, 4)
	_, err = io.ReadFull(r, reqHdr)
	if err != nil {
		return
	}
	cmd := reqHdr[1]
	atyp := reqHdr[3]

	if cmd != 0x01 { // only CONNECT
		conn.Write([]byte{0x05, 0x07, 0x00, 0x01, 0, 0, 0, 0, 0, 0}) // cmd not supported
		return
	}

	target, err := readAddr(r, atyp)
	if err != nil {
		conn.Write([]byte{0x05, 0x08, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	port, err := readPort(r)
	if err != nil {
		conn.Write([]byte{0x05, 0x08, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	targetAddr := net.JoinHostPort(target, fmt.Sprintf("%d", port))
	conn.SetDeadline(time.Time{})

	ctrlMu.Lock()
	phoneID := lastPhone
	ctrlMu.Unlock()
	if phoneID == "" {
		log.Printf("[SOCKS5] No phone registered")
		conn.Write([]byte{0x05, 0x01, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}

	waitMu.Lock()
	connCID++
	cid := fmt.Sprintf("c%d", connCID)
	ch := make(chan net.Conn, 1)
	wait[cid] = waitEntry{client: conn, dataCh: ch}
	waitMu.Unlock()

	ctrlMsg := fmt.Sprintf("conn:%s:%s\n", cid, targetAddr)

	ctrlMu.Lock()
	sc, ok := ctrl[phoneID]
	ctrlMu.Unlock()
	if !ok {
		waitMu.Lock()
		delete(wait, cid)
		waitMu.Unlock()
		conn.Write([]byte{0x05, 0x01, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}

	_, err = sc.Write([]byte(ctrlMsg))
	if err != nil {
		waitMu.Lock()
		delete(wait, cid)
		waitMu.Unlock()
		conn.Write([]byte{0x05, 0x01, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}

	log.Printf("[SOCKS5] %s -> %s via %s", conn.RemoteAddr(), targetAddr, phoneID)

	select {
	case data := <-ch:
		conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		setNoDelay(conn, data)
		pipe(conn, r, data, bufio.NewReaderSize(data, 65536))
		log.Printf("[SOCKS5] %s closed", targetAddr)
	case <-time.After(60 * time.Second):
		log.Printf("[SOCKS5] %s timeout", targetAddr)
		waitMu.Lock()
		delete(wait, cid)
		waitMu.Unlock()
		conn.Write([]byte{0x05, 0x03, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
	}
}

func readAddr(r *bufio.Reader, atyp byte) (string, error) {
	switch atyp {
	case 0x01: // IPv4
		b := make([]byte, 4)
		_, err := io.ReadFull(r, b)
		if err != nil {
			return "", err
		}
		return net.IP(b).String(), nil
	case 0x03: // domain
		n, err := r.ReadByte()
		if err != nil {
			return "", err
		}
		b := make([]byte, n)
		_, err = io.ReadFull(r, b)
		if err != nil {
			return "", err
		}
		return string(b), nil
	case 0x04: // IPv6
		b := make([]byte, 16)
		_, err := io.ReadFull(r, b)
		if err != nil {
			return "", err
		}
		return net.IP(b).String(), nil
	}
	return "", fmt.Errorf("unknown atyp %d", atyp)
}

func readPort(r *bufio.Reader) (int, error) {
	b := make([]byte, 2)
	_, err := io.ReadFull(r, b)
	if err != nil {
		return 0, err
	}
	return int(binary.BigEndian.Uint16(b)), nil
}

func setNoDelay(conns ...net.Conn) {
	for _, c := range conns {
		if t, ok := c.(*net.TCPConn); ok {
			t.SetNoDelay(true)
		}
	}
}

func pipe(a net.Conn, ar *bufio.Reader, b net.Conn, br *bufio.Reader) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if ar != nil {
			io.Copy(b, ar)
		}
		halfClose(b)
	}()
	go func() {
		defer wg.Done()
		if br != nil {
			io.Copy(a, br)
		}
		halfClose(a)
	}()
	wg.Wait()
}

func halfClose(c net.Conn) {
	if t, ok := c.(*net.TCPConn); ok {
		t.CloseWrite()
	} else {
		c.Close()
	}
}
