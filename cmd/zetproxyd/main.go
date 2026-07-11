package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/user/zetproxy/internal/dashboard"
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
  Ultra-Fast Proxy Tunnel
	`)

	tcpAddr := getEnv("ZETPROXY_TCP", ":8080")
	udpAddr := getEnv("ZETPROXY_UDP", ":5353")
	dashAddr := getEnv("ZETPROXY_DASHBOARD", ":9090")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tun := tunnel.NewServer(tcpAddr, udpAddr)
	dash := dashboard.NewServer(dashAddr, tun)

	go func() {
		if err := dash.Start(ctx); err != nil {
			log.Printf("[Web] Error: %v", err)
		}
	}()

	log.Println("═══════════════════════════════════")
	log.Printf("  TCP Tunnel:  %s", tcpAddr)
	log.Printf("  UDP Tunnel:  %s", udpAddr)
	log.Printf("  Dashboard:   http://%s", dashAddr)
	log.Println("═══════════════════════════════════")

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
