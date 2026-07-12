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

	fmt.Printf(`
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
	tunnelMode := os.Getenv("ZETPROXY_TUNNEL")
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

	socksPort := socksAddr[1:]
	if socksPort == "" {
		socksPort = "1088"
	}

	if tunnelMode != "" {
		mode, remote := tunnel.ParseTunnelConfig(tunnelMode)
		go func() {
			time.Sleep(2 * time.Second)
			switch mode {
			case "relay":
				log.Printf("[Tunnel] Starting relay tunnel to %s ...", remote)
				if err := tunnel.StartRelayTunnel(socksPort, remote); err != nil {
					log.Printf("[Tunnel] Error: %v", err)
				}
			case "serveo":
				log.Println("")
				log.Println("╔══════════════════════════════════════════════════╗")
				log.Println("║  serveo.net is a REVERSE proxy (exposes local   ║")
				log.Println("║  servers). It CANNOT be used as a forward proxy ║")
				log.Println("║  (SOCKS5/HTTP CONNECT). Protocol errors occur.  ║")
				log.Println("╠══════════════════════════════════════════════════╣")
				log.Println("║  For SOCKS5 proxy over internet, use:           ║")
				log.Println("║                                                ║")
				log.Println("║  Option 1: Custom SSH server (most reliable)   ║")
				log.Println("║    ZETPROXY_TUNNEL=ssh:user@your-vps.com       ║")
				log.Println("║                                                ║")
				log.Println("║  Option 2: Relay on Railway (free SOCKS5)      ║")
				log.Println("║    ZETPROXY_TUNNEL=relay:host:7800             ║")
				log.Println("║                                                ║")
				log.Println("║  Deploy relay: github.com/tundefund0-gif/      ║")
				log.Println("║    zetproxy-turbo → Deploy on Railway          ║")
				log.Println("╚══════════════════════════════════════════════════╝")
				log.Println("")
				log.Println("[Tunnel] serveo.net is not supported as a proxy tunnel. Use relay or SSH mode.")
			default:
				log.Printf("[Tunnel] Starting SSH tunnel to %s ...", remote)
				if err := tunnel.StartSSHTunnel(socksPort, remote); err != nil {
					log.Printf("[Tunnel] Error: %v", err)
				}
			}
		}()
	}

	localIP := overrideIP
	if localIP == "" {
		localIP = getPreferredIP()
	}
	allIPs := getAllIPs()
	log.Printf("Advertised IP: %s (override=%q)", localIP, overrideIP)

	log.Println("═══════════════════════════════════════════")
	log.Printf("  SOCKS5:     %s:%s", localIP, socksAddr[1:])
	log.Printf("  TCP Tunnel: %s:%s", localIP, tcpAddr[1:])
	log.Printf("  UDP Tunnel: %s:%s", localIP, udpAddr[1:])
	log.Printf("  Dashboard:  http://%s:%s", localIP, dashAddr[1:])
	log.Printf("  Max Conns:  %d", maxConns)
	log.Println("───────────────────────────────────────────")
	log.Printf("  Reachable via:")
	for _, ip := range allIPs {
		label := "LAN"
		if isTailscaleIP(net.ParseIP(ip).To4()) {
			label = "TAILSCALE"
		}
		log.Printf("    %-12s %s:%s (SOCKS5)", label, ip, socksAddr[1:])
		log.Printf("    %-12s http://%s:%s (Dashboard)", label, ip, dashAddr[1:])
	}
	if tunnelMode != "" {
		mode, remote := tunnel.ParseTunnelConfig(tunnelMode)
		log.Println("───────────────────────────────────────────")
		if mode == "serveo" {
			log.Printf("  Tunnel mode: serveo.net (NOT SUPPORTED)")
		} else if mode == "relay" {
			log.Printf("  Tunnel: relay -> %s", remote)
		} else {
			log.Printf("  Tunnel: SSH -> %s", remote)
		}
	}
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
		tunnel.StopTunnel()
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
	ips := getAllIPs()
	for _, ip := range ips {
		parsed := net.ParseIP(ip).To4()
		if parsed == nil {
			continue
		}
		if isTailscaleIP(parsed) {
			continue
		}
		if isPrivateIP(parsed) {
			return ip
		}
	}
	if len(ips) > 0 {
		return ips[0]
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

func getAllIPs() []string {
	var ips []string
	seen := map[string]bool{}
	ifaces, err := net.Interfaces()
	if err != nil {
		return ips
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok {
				ip4 := ipnet.IP.To4()
				if ip4 == nil || seen[ip4.String()] {
					continue
				}
				seen[ip4.String()] = true
				ips = append(ips, ip4.String())
			}
		}
	}
	return ips
}

func isPrivateIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip[0] == 192 && ip[1] == 168 {
		return true
	}
	if ip[0] == 172 && ip[1] >= 16 && ip[1] <= 31 {
		return true
	}
	if ip[0] == 10 {
		return true
	}
	return false
}

func isTailscaleIP(ip net.IP) bool {
	if ip == nil || len(ip) < 4 {
		return false
	}
	return ip[0] == 100 && ip[1] >= 64 && ip[1] <= 127
}
