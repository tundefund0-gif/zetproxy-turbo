package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/user/zetproxy/internal/dashboard"
	"github.com/user/zetproxy/internal/proxy"
	"github.com/user/zetproxy/internal/tunnel"
)

const Version = "2.0.0-turbo"

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.SetPrefix("[ZetProxy] ")

	fmt.Println(`
  ███████╗███████╗████████╗██████╗ ██████╗  ██████╗ ██╗  ██╗██╗   ██╗
  ╚══███╔╝██╔════╝╚══██╔══╝██╔══██╗██╔══██╗██╔═══██╗╚██╗██╔╝╚██╗ ██╔╝
    ███╔╝ █████╗     ██║   ██████╔╝██████╔╝██║   ██║ ╚███╔╝  ╚████╔╝
   ███╔╝  ██╔══╝     ██║   ██╔═══╝ ██╔══██╗██║   ██║ ██╔██╗   ╚██╔╝
  ███████╗███████╗   ██║   ██║     ██║  ██║╚██████╔╝██╔╝ ██╗   ██║
  ╚══════╝╚══════╝   ╚═╝   ╚═╝     ╚═╝  ╚═╝ ╚═════╝ ╚═╝  ╚═╝   ╚═╝
  Ultra-Fast Proxy Tunnel — Turbo Edition v%s
	`, Version)

	log.Printf("Starting ZetProxy Turbo v%s", Version)
	log.Printf("Go %s | %d CPUs | GOMAXPROCS=%d", runtime.Version(), runtime.NumCPU(), runtime.GOMAXPROCS(0))

	tcpAddr := getEnv("ZETPROXY_TCP", ":8888")
	udpAddr := getEnv("ZETPROXY_UDP", ":8889")
	socksAddr := getEnv("ZETPROXY_SOCKS5", ":1088")
	dashAddr := getEnv("ZETPROXY_DASHBOARD", ":9092")
	overrideIP := os.Getenv("ZETPROXY_IP")
	maxConns := int32(getEnvInt("ZETPROXY_MAX_CONNS", 4096))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tun := tunnel.NewServer(tcpAddr, udpAddr)
	tun.SetMaxConns(maxConns)

	socks := proxy.NewSOCKS5Server(socksAddr)
	socks.SetMaxConns(maxConns / 2)

	dash := dashboard.NewServer(dashAddr, tun, socks)

	go func() {
		log.Printf("[SOCKS5] Starting on %s", socksAddr)
		if err := socks.Start(ctx); err != nil {
			log.Printf("[SOCKS5] Error: %v", err)
		}
	}()

	go func() {
		log.Printf("[Dashboard] Starting on http://%s", dashAddr)
		if err := dash.Start(ctx); err != nil && err != context.Canceled {
			log.Printf("[Dashboard] Error: %v", err)
		}
	}()

	localIP := overrideIP
	if localIP == "" {
		localIP = getPreferredIP()
	}
	log.Printf("Advertised IP: %s (override=%q)", localIP, overrideIP)

	log.Println("═══════════════════════════════════════════")
	log.Printf("  SOCKS5:     %s:%s", localIP, socksAddr[1:])
	log.Printf("  TCP Tunnel: %s:%s", localIP, tcpAddr[1:])
	log.Printf("  UDP Tunnel: %s:%s", localIP, udpAddr[1:])
	log.Printf("  Dashboard:  http://%s:%s", localIP, dashAddr[1:])
	log.Printf("  Max Conns:  %d", maxConns)
	log.Println("═══════════════════════════════════════════")

	go func() {
		log.Printf("[TCP] Starting TCP tunnel on %s", tcpAddr)
		log.Printf("[UDP] Starting UDP tunnel on %s", udpAddr)
		if err := tun.Start(ctx); err != nil {
			log.Printf("[Tunnel] Error: %v", err)
			cancel()
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	sigReceived := <-sig
	log.Printf("Received signal: %v", sigReceived)

	log.Println("Initiating graceful shutdown...")
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	done := make(chan struct{})
	go func() {
		tun.Stop()
		socks.Stop()
		close(done)
	}()

	select {
	case <-done:
		log.Println("All servers stopped gracefully")
	case <-shutdownCtx.Done():
		log.Println("Shutdown timeout reached, forcing exit")
	}

	log.Println("Goodbye!")
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 {
			return n
		}
	}
	return def
}

func getPreferredIP() string {
	ifaces, err := net.Interfaces()
	if err == nil {
		var fallbackIPs []string
		for _, iface := range ifaces {
			if iface.Flags&net.FlagUp == 0 {
				continue
			}
			if iface.Flags&net.FlagLoopback != 0 {
				continue
			}
			name := iface.Name
			if isVirtualInterface(name) {
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
					if isTailscaleIP(ip4) {
						continue
					}
					if ip4[0] == 192 && ip4[1] == 168 {
						return ip4.String()
					}
					if ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31 {
						return ip4.String()
					}
					if ip4[0] == 10 {
						return ip4.String()
					}
					fallbackIPs = append(fallbackIPs, ip4.String())
				}
			}
		}
		if len(fallbackIPs) > 0 {
			return fallbackIPs[0]
		}
	}

	conn, err := net.DialTimeout("udp", "8.8.8.8:53", 3*time.Second)
	if err != nil {
		return "0.0.0.0"
	}
	defer conn.Close()
	localAddr := conn.LocalAddr().(*net.UDPAddr)
	if localAddr.IP != nil && !isTailscaleIP(localAddr.IP.To4()) {
		return localAddr.IP.String()
	}
	return "0.0.0.0"
}

var virtualIfaces = map[string]bool{
	"tailscale": true, "docker": true, "tun": true, "tap": true,
	"bridge": true, "lo": true, "virbr": true, "lxc": true,
	"veth": true, "dummy": true, "sit": true, "ip6tnl": true,
}

func isVirtualInterface(name string) bool {
	for i := 0; i < len(name); i++ {
		if name[i] >= '0' && name[i] <= '9' {
			prefix := name[:i]
			if virtualIfaces[prefix] {
				return true
			}
		}
	}
	return virtualIfaces[name]
}

func isTailscaleIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip[0] == 100 && ip[1] >= 64 && ip[1] <= 127 {
		return true
	}
	return false
}
