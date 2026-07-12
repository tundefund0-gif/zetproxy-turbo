package main

import (
	"bufio"
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
	ctrlMu sync.Mutex
	ctrl   = make(map[string]net.Conn)

	waitMu sync.Mutex
	wait   = make(map[string]waitEntry)

	nextID int
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
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	r := bufio.NewReaderSize(conn, 65536)

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
			if t, ok := conn.(*net.TCPConn); ok {
				t.SetNoDelay(true)
			}
			if t, ok := data.(*net.TCPConn); ok {
				t.SetNoDelay(true)
			}
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
