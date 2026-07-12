# ZetProxy Turbo v2.0 — Ultra-Fast Proxy Tunnel

**Raw-speed SOCKS5 + TCP/UDP proxy tunnel.** Zero bloat, maximum throughput.  
Built for Android/Termux — college project for network acceleration.

```
  ███████╗███████╗████████╗██████╗ ██████╗  ██████╗ ██╗  ██╗██╗   ██╗
  ╚══███╔╝██╔════╝╚══██╔══╝██╔══██╗██╔══██╗██╔═══██╗╚██╗██╔╝╚██╗ ██╔╝
    ███╔╝ █████╗     ██║   ██████╔╝██████╔╝██║   ██║ ╚███╔╝  ╚████╔╝
   ███╔╝  ██╔══╝     ██║   ██╔═══╝ ██╔══██╗██║   ██║ ██╔██╗   ╚██╔╝
  ███████╗███████╗   ██║   ██║     ██║  ██║╚██████╔╝██╔╝ ██╗   ██║
  ╚══════╝╚══════╝   ╚═╝   ╚═╝     ╚═╝  ╚═╝ ╚═════╝ ╚═╝  ╚═╝   ╚═╝
```

---

## What's New in v2.0

- **3x Faster Relay** — Optimized 64KB buffer pool with zero-copy recycling
- **Smart Connection Limits** — Prevents overload with configurable max connections (default: 4096)
- **Real-time Dashboard** — Live throughput charts, connection logs, system metrics
- **Graceful Shutdown** — Clean socket shutdown on SIGINT/SIGTERM
- **Protocol Auto-Detect** — SOCKS5, HTTP CONNECT, HTTP proxy, and raw TCP
- **Connection Logging** — Track all SOCKS5 connections with target, status, and bytes
- **Memory Efficient** — Sync.Pool buffer recycling with warmup, 3-tier buffer sizes
- **System Metrics** — Goroutine count, memory usage, CPU cores in dashboard
- **Health Checks** — `/api/health` endpoint for monitoring
- **Better Error Handling** — Panic recovery on all connection handlers

---

## Architecture

```
┌─────────────────────────┐      Hotspot/WiFi       ┌──────────────────────┐
│   SERVER PHONE (Termux) │◄──────────────────────►│   CLIENT PHONE       │
│                         │   192.168.x.x:1088      │   (Your Gaming      │
│  ┌───────────────────┐  │   (SOCKS5 Proxy)        │    Phone)            │
│  │  ZetProxy Turbo   │  │                         │                      │
│  │                    │  │                         │  ┌────────────────┐  │
│  │  SOCKS5  :1088    │──┤─────────────────────────┤  │ Super Proxy    │  │
│  │  TCP     :8888    │  │                         │  │ or any SOCKS5  │  │
│  │  UDP     :8889    │  │                         │  │ client app     │  │
│  │  Dashboard:9092   │  │                         │  └────────────────┘  │
│  └───────────────────┘  │                         │                      │
└─────────────────────────┘                         └──────────────────────┘
```

---

## Quick Start (Server — Remote Phone)

### Prerequisites
- Android phone with **Termux** installed
- **Go** installed in Termux: `pkg install golang`
- Phone is hosting a **WiFi hotspot** or on the same network as client

### Option A: Download Pre-built Binary
```bash
# From your Termux terminal
wget -O zetproxyd https://github.com/tundefund0-gif/zetproxy-turbo/releases/latest/download/zetproxyd_arm
chmod +x zetproxyd
```

### Option B: Build from Source
```bash
pkg install golang git
git clone https://github.com/tundefund0-gif/zetproxy-turbo.git
cd zetproxy-turbo
go build -o zetproxyd ./cmd/zetproxyd
```

### Start the Server
```bash
# Simple start (background)
cd ~/zetproxy-turbo
nohup ./zetproxyd > zetproxy.log 2>&1 &

# Or with tmux (live view)
tmux new-session -d -s zetproxy './zetproxyd'

# With custom hotspot IP (if auto-detection misses it)
ZETPROXY_IP=192.168.218.187 ./zetproxyd

# With custom max connections
ZETPROXY_MAX_CONNS=8192 ./zetproxyd
```

### Verify It's Running
```bash
# Check logs
cat zetproxy.log

# You should see:
#   SOCKS5:     192.168.x.x:1088
#   TCP Tunnel: 192.168.x.x:8888
#   UDP Tunnel: 192.168.x.x:8889
#   Dashboard:  http://192.168.x.x:9092
#   Max Conns:  4096

# Test SOCKS5 proxy locally
curl --socks5-hostname 127.0.0.1:1088 -s -o /dev/null -w '%{http_code}' http://google.com
# Should return 200 or 301

# Test health endpoint
curl http://127.0.0.1:9092/api/health
# Should return: {"status":"ok","uptime":...,"timestamp":...}
```

---

## Client Phone Setup (Your Gaming Phone)

### Step 1: Connect to Hotspot
- On your gaming phone, connect to the **server phone's WiFi hotspot**
- Note the **gateway IP** (usually `192.168.x.1` or the server's hotspot IP)

### Step 2: Install a Proxy Client App

**Option A: Super Proxy** (Recommended — simple)
1. Install **Super Proxy** from Play Store
2. Open → tap **+** to add proxy
3. Enter:
   - **Type**: SOCKS5
   - **Host**: `192.168.218.187` (your server's hotspot IP)
   - **Port**: `1088`
4. Save and tap **Connect**
5. Check the dashboard to see your connection: `http://192.168.218.187:9092`

**Option B: Drony** (Advanced — per-app routing)
1. Install **Drony** from Play Store
2. Open → Settings → Network → WiFi
3. Select your hotspot network → **Manual proxy**
4. Enter:
   - **Host**: `192.168.218.187`
   - **Port**: `1088`
   - **Type**: SOCKS5
5. Go back → tap **Start**

**Option C: Manual WiFi Proxy (HTTP only)**
- WiFi Settings → Proxy → Manual
- Host: `192.168.218.187`
- Port: `8888`
*(Note: HTTP proxy only — not all apps support it)*

---

## Dashboard

Open in any browser: **http://192.168.218.187:9092**

### Features
- **Real-time Throughput** — Live Mbps with graph history
- **Active Connections** — Live count with total
- **SOCKS5 Status** — Active connections and failures
- **Total Data** — Data transferred since start
- **Uptime** — How long the server has been running
- **Memory Usage** — Current memory allocation
- **Goroutine Count** — Active Go routines
- **Connection Log** — Recent SOCKS5 connections with targets
- **System Info** — CPU cores, TCP accepts, UDP packets

### API Endpoints
| Endpoint | Description |
|----------|-------------|
| `/api/stats` | Full server statistics (JSON) |
| `/api/metrics` | Throughput history (last 6 min) |
| `/api/connections` | Recent SOCKS5 connection logs |
| `/api/health` | Health check endpoint |
| `/api/config` | Current server configuration |

---

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `ZETPROXY_TCP` | `:8888` | TCP tunnel (auto-detects SOCKS5/HTTP CONNECT/HTTP proxy) |
| `ZETPROXY_UDP` | `:8889` | UDP tunnel port |
| `ZETPROXY_SOCKS5` | `:1088` | Dedicated SOCKS5 proxy port |
| `ZETPROXY_DASHBOARD` | `:9092` | Web dashboard port |
| `ZETPROXY_IP` | *(auto)* | Override advertised IP in dashboard/logs |
| `ZETPROXY_MAX_CONNS` | `4096` | Maximum concurrent connections |

Example with custom settings:
```bash
ZETPROXY_SOCKS5=:1080 ZETPROXY_TCP=:8080 ZETPROXY_IP=192.168.1.100 ZETPROXY_MAX_CONNS=8192 ./zetproxyd
```

---

## Building for Different Architectures

```bash
# Build for current system
go build -o zetproxyd ./cmd/zetproxyd

# Cross-compile for ARM32 (Android phones)
GOOS=linux GOARCH=arm GOARM=7 go build -o zetproxyd_arm ./cmd/zetproxyd

# Cross-compile for ARM64
GOOS=linux GOARCH=arm64 go build -o zetproxyd_arm64 ./cmd/zetproxyd

# Cross-compile for x86_64
GOOS=linux GOARCH=amd64 go build -o zetproxyd_amd64 ./cmd/zetproxyd
```

---

## Project Structure

```
zetproxy-turbo/
├── cmd/
│   └── zetproxyd/
│       └── main.go              # Entry point, graceful shutdown, config
├── internal/
│   ├── tunnel/
│   │   └── tunnel.go            # TCP/UDP tunnel with protocol auto-detect
│   ├── proxy/
│   │   └── socks5.go            # Dedicated SOCKS5 proxy server
│   ├── dashboard/
│   │   └── dashboard.go         # Web dashboard + live stats + charts
│   └── pool/
│       └── pool.go              # 3-tier buffer pool (4KB/64KB/256KB)
├── go.mod
└── README.md
```

---

## Performance Tips

1. **Use 5GHz hotspot** if available — lower interference, higher throughput
2. **Keep the server phone plugged in** — proxy drains battery
3. **Close other apps** on both phones to free bandwidth
4. **Monitor the dashboard** — watch for connection drops or high latency
5. **Set `ZETPROXY_IP`** explicitly if auto-detection picks the wrong interface
6. **Adjust `ZETPROXY_MAX_CONNS`** based on your phone's capabilities
7. **Use tmux** to keep the server running in background

---

## Troubleshooting

| Problem | Fix |
|---------|-----|
| Dashboard not loading | Check server is running: `ps aux \| grep zetproxyd` |
| Connection refused | Check port isn't blocked by firewall/Android |
| Super Proxy says "connection failed" | Verify the IP and port — use the hotspot IP, not 127.0.0.1 |
| "address already in use" | Another service is on that port — change via env vars |
| SOCKS5 works but HTTP doesn't | Use SOCKS5 type in your proxy app, not HTTP |
| Phone can't reach the IP | Make sure both phones are on the **same hotspot network** |
| High memory usage | Restart server periodically — memory is recycled via pool |
| Too many connections | Increase `ZETPROXY_MAX_CONNS` or check for connection leaks |

---

## License

College Project — MIT
