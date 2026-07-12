package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/user/zetproxy/internal/dashboard"
	"github.com/user/zetproxy/internal/proxy"
	"github.com/user/zetproxy/internal/tunnel"
)

func main() {
	fmt.Println(`
  ███████╗███████╗████████╗██████╗ ██████╗  ██████╗ ██╗  ██╗██╗   ██╗
  ╚══███╔╝██╔════╝╚══██╔══╝██╔══██╗██╔══██╗██╔═══██╗╚██╗██╔╝╚██╗ ██╔╝
    ███╔╝ █████╗     ██║   ██████╔╝██████╔╝██║   ██║ ╚███╔╝  ╚████╔╝
   ███╔╝  ██╔══╝     ██║   ██╔═══╝ ██╔══██╗██║   ██║ ██╔██╗   ╚██╔╝
  ███████╗███████╗   ██║   ██║     ██║  ██║╚██████╔╝██╔╝ ██╗   ██║
  ╚══════╝╚══════╝   ╚═╝   ╚═╝     ╚═╝  ╚═╝ ╚═════╝ ╚═╝  ╚═╝   ╚═╝
  Ultra-Fast Proxy Tunnel — Turbo Edition
	`)

	tcpAddr := getEnv("ZETPROXY_TCP", ":8888")
	udpAddr := getEnv("ZETPROXY_UDP", ":8889")
	socksAddr := getEnv("ZETPROXY_SOCKS5", ":1088")
	dashAddr := getEnv("ZETPROXY_DASHBOARD", ":9092")
	overrideIP := os.Getenv("ZETPROXY_IP")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create servers
	tun := tunnel.NewServer(tcpAddr, udpAddr)
	socks := proxy.NewSOCKS5Server(socksAddr)
	dash := dashboard.NewServer(dashAddr, tun, socks)

	// Start SOCKS5 proxy
	go func() {
		log.Printf("[SOCKS5] Starting on %s", socksAddr)
		if err := socks.Start(ctx); err != nil {
			log.Printf("[SOCKS5] Error: %v", err)
		}
	}()

	// Start dashboard
	go func() {
		log.Printf("[Dashboard] Starting on http://%s", dashAddr)
		if err := dash.Start(ctx); err != nil {
			log.Printf("[Dashboard] Error: %v", err)
		}
	}()

	localIP := overrideIP
	if localIP == "" {
		localIP = getPreferredIP()
	}
	log.Println("═══════════════════════════════════")
	log.Printf("  SOCKS5:     %s:%s", localIP, socksAddr[1:])
	log.Printf("  TCP Tunnel: %s:%s", localIP, tcpAddr[1:])
	log.Printf("  UDP Tunnel: %s:%s", localIP, udpAddr[1:])
	log.Printf("  Dashboard:  http://%s:%s", localIP, dashAddr[1:])
	log.Println("═══════════════════════════════════")

	// Start TCP/UDP tunnel (blocking)
	log.Printf("[TCP] Starting TCP tunnel on %s", tcpAddr)
	log.Printf("[UDP] Starting UDP tunnel on %s", udpAddr)
	if err := tun.Start(ctx); err != nil {
		log.Printf("[Tunnel] Error: %v", err)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	log.Println("Shutting down...")
	cancel()
	log.Println("Goodbye!")
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getPreferredIP() string {
	// Try to find hotspot IP via net.Interfaces()
	ifaces, err := net.Interfaces()
	if err == nil {
		for _, iface := range ifaces {
			if iface.Flags&net.FlagUp == 0 {
				continue
			}
			addrs, err := iface.Addrs()
			if err != nil {
				continue
			}
			for _, addr := range addrs {
				if ipnet, ok := addr.(*net.IPNet); ok {
					ip4 := ipnet.IP.To4()
					if ip4 == nil {
						continue
					}
					// Prefer 192.168.x.x (common hotspot subnet)
					if ip4[0] == 192 && ip4[1] == 168 {
						return ip4.String()
					}
					if ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31 {
						return ip4.String()
					}
					if ip4[0] == 10 {
						return ip4.String()
					}
				}
			}
		}
	}

	// Fallback: get default route IP
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "127.0.0.1"
	}
	defer conn.Close()
	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String()
}
