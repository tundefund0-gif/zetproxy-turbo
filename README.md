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

**Version 2.0 — Turbo Edition** | Go 1.21+ | MIT License

---

## What's New in v2.0

| Feature | Description |
|---------|-------------|
| **3x Faster Relay** | 3-tier buffer pool (4KB/64KB/256KB) with warmup & zero-copy recycling |
| **Real-time Dashboard** | Live throughput charts, connection logs, goroutine/memory metrics |
| **Connection Limits** | Configurable max concurrent connections (default: 4096) |
| **Graceful Shutdown** | Clean socket teardown on SIGINT/SIGTERM with 10s timeout |
| **Protocol Auto-Detect** | SOCKS5, HTTP CONNECT, HTTP proxy, and raw TCP in one port |
| **Connection Logging** | Track all SOCKS5 connections — addr, target, status, bytes |
| **Health Checks** | `/api/health` endpoint for monitoring & load balancers |
| **Config API** | `/api/config` returns live server configuration |
| **Memory Efficiency** | Sync.Pool recycling with zero allocations in hot path |
| **System Metrics** | Goroutine count, memory alloc/sys, CPU cores on dashboard |
| **Better Errors** | Panic recovery on all handlers, detailed failure counters |
| **UDP Cleanup** | Automatic session expiry after 3min of inactivity |

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

## Quick Start (Server Phone)

### Prerequisites
- Android phone with **Termux** installed
- **Go** installed: `pkg install golang git`
- Phone hosting a **WiFi hotspot** or on same network as client

### Option A: Download Pre-built Binary
```bash
# ARM32 (most Android phones)
wget -O zetproxyd https://github.com/tundefund0-gif/zetproxy-turbo/releases/latest/download/zetproxyd_arm
chmod +x zetproxyd

# ARM64 (newer phones)
wget -O zetproxyd https://github.com/tundefund0-gif/zetproxy-turbo/releases/latest/download/zetproxyd_arm64
chmod +x zetproxyd

# AMD64 (PC/Linux)
wget -O zetproxyd https://github.com/tundefund0-gif/zetproxy-turbo/releases/latest/download/zetproxyd_amd64
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
# Simple background start
cd ~/zetproxy-turbo
nohup ./zetproxyd > zetproxy.log 2>&1 &

# Live view with tmux
tmux new-session -d -s zetproxy './zetproxyd'

# With custom hotspot IP
ZETPROXY_IP=192.168.218.187 ./zetproxyd

# With custom ports and max connections
ZETPROXY_SOCKS5=:1080 ZETPROXY_TCP=:8080 ZETPROXY_MAX_CONNS=8192 ./zetproxyd
```

### Verify It's Running
```bash
# Check logs
cat zetproxy.log

# Expected output:
#   SOCKS5:     192.168.x.x:1088
#   TCP Tunnel: 192.168.x.x:8888
#   UDP Tunnel: 192.168.x.x:8889
#   Dashboard:  http://192.168.x.x:9092
#   Max Conns:  4096

# Test SOCKS5 locally
curl --socks5-hostname 127.0.0.1:1088 -s -o /dev/null -w '%{http_code}' http://google.com
# Returns: 200 or 301

# Health check
curl http://127.0.0.1:9092/api/health
# Returns: {"status":"ok","uptime":...,"timestamp":...}

# Full stats
curl http://127.0.0.1:9092/api/stats
# Returns: JSON with all metrics
```

---

## Client Phone Setup (Gaming Phone)

### Step 1: Connect to Hotspot
- Connect to the **server phone's WiFi hotspot**
- Note the **gateway IP** (usually `192.168.x.1`)

### Step 2: Install Proxy App

**Option A: Super Proxy** (Recommended)
1. Install **Super Proxy** from Play Store
2. Tap **+** → add proxy
3. Enter:
   - Type: **SOCKS5**
   - Host: `192.168.218.187` (server's hotspot IP)
   - Port: `1088`
4. Save and tap **Connect**
5. Dashboard: `http://192.168.218.187:9092`

**Option B: Drony** (Per-app routing)
1. Install **Drony** from Play Store
2. Settings → Network → WiFi → select hotspot
3. Manual proxy:
   - Host: `192.168.218.187`
   - Port: `1088`
   - Type: SOCKS5
4. Tap **Start**

**Option C: Manual WiFi Proxy** (HTTP only)
- WiFi Settings → Proxy → Manual
- Host: `192.168.218.187`
- Port: `8888`
*(HTTP proxy only — not all apps support it)*

---

## Dashboard

Open in any browser: **http://192.168.218.187:9092**

### Live Metrics
| Card | Shows |
|------|-------|
| Throughput In | Current download Mbps + B/s |
| Throughput Out | Current upload Mbps + B/s |
| Active Connections | Live count + total historical |
| SOCKS5 Active | Active SOCKS5 + failed count |
| Total Data | Combined data transferred |
| Data In / Out | Separate upload/download totals |
| Uptime | Running duration |
| Memory | MB allocated + goroutine count |

### Charts
- **Throughput History** — Rolling 120-second graph (up to 6 minutes visible)

### Connection Log
- Last 20 SOCKS5 connections with timestamp, client IP, target, status

### API Endpoints
| Endpoint | Returns |
|----------|---------|
| `GET /api/stats` | Full server statistics JSON |
| `GET /api/metrics` | Throughput history array |
| `GET /api/connections` | Recent connection logs |
| `GET /api/health` | `{"status":"ok","uptime":...,"timestamp":...}` |
| `GET /api/config` | Live server configuration |

---

## Protocol Auto-Detection

The TCP tunnel (`:8888`) automatically detects the proxy protocol:

| Protocol | Detection | How it works |
|----------|-----------|--------------|
| **SOCKS5** | First byte `0x05` | Full SOCKS5 handshake → connect to target |
| **HTTP CONNECT** | Starts with `CONNECT` | Tunnel to host:port, reply `200` |
| **HTTP Proxy** | `GET`/`POST`/`PUT`/`HEAD` | Extract `Host` header, forward request |
| **Raw TCP** | `\x00\x00` prefix | Forward first 256 bytes as target address |

---

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `ZETPROXY_TCP` | `:8888` | TCP tunnel (auto-detect SOCKS5/HTTP CONNECT/HTTP proxy) |
| `ZETPROXY_UDP` | `:8889` | UDP tunnel port |
| `ZETPROXY_SOCKS5` | `:1088` | Dedicated SOCKS5 proxy port |
| `ZETPROXY_DASHBOARD` | `:9092` | Web dashboard + API port |
| `ZETPROXY_IP` | *(auto)* | Override advertised IP in dashboard/logs |
| `ZETPROXY_MAX_CONNS` | `4096` | Maximum concurrent connections (total across all services) |

Full custom config example:
```bash
ZETPROXY_SOCKS5=:1080 \
ZETPROXY_TCP=:8080 \
ZETPROXY_UDP=:8081 \
ZETPROXY_DASHBOARD=:9000 \
ZETPROXY_IP=192.168.1.100 \
ZETPROXY_MAX_CONNS=8192 \
./zetproxyd
```

---

## Building for Different Architectures

```bash
# Current system
go build -o zetproxyd ./cmd/zetproxyd

# ARM32 (Android phones)
GOOS=linux GOARCH=arm GOARM=7 go build -o zetproxyd_arm ./cmd/zetproxyd

# ARM64
GOOS=linux GOARCH=arm64 go build -o zetproxyd_arm64 ./cmd/zetproxyd

# x86_64
GOOS=linux GOARCH=amd64 go build -o zetproxyd_amd64 ./cmd/zetproxyd

# All architectures at once
GOOS=linux GOARCH=arm GOARM=7 go build -o zetproxyd_arm ./cmd/zetproxyd && \
GOOS=linux GOARCH=arm64 go build -o zetproxyd_arm64 ./cmd/zetproxyd && \
GOOS=linux GOARCH=amd64 go build -o zetproxyd_amd64 ./cmd/zetproxyd
```

---

## Internal Architecture

```
zetproxy-turbo/
├── cmd/
│   └── zetproxyd/
│       └── main.go              # Entry point, graceful shutdown, env config
├── internal/
│   ├── tunnel/
│   │   └── tunnel.go            # TCP/UDP tunnel with protocol auto-detect
│   ├── proxy/
│   │   └── socks5.go            # Dedicated SOCKS5 proxy + connection logging
│   ├── dashboard/
│   │   └── dashboard.go         # Web dashboard + charts + metrics API
│   └── pool/
│       └── pool.go              # 3-tier buffer pool (4KB/64KB/256KB)
├── .gitignore
├── go.mod
└── README.md
```

### Component Details

**cmd/zetproxyd/main.go**
- Reads environment variables
- Launches all 3 servers (SOCKS5, tunnel, dashboard)
- Waits for SIGINT/SIGTERM/SIGHUP
- Graceful shutdown with 10s timeout

**internal/pool/pool.go**
- 3-tier `sync.Pool` (4KB small, 64KB medium, 256KB large)
- Warmup on creation (128 buffers per tier)
- `Relay()` — bidirectional copy with optimized `io.CopyBuffer`
- `RelayBidirectional()` — alternative with error channels
- `closeWrite()` — half-close TCP for clean shutdown
- Global `PoolStats` tracking allocation/reuse/drop counts

**internal/tunnel/tunnel.go**
- TCP listener with connection semaphore (configurable max)
- Protocol auto-detection on first 4KB read
- SOCKS5, HTTP CONNECT, HTTP Proxy, raw TCP forwarding
- UDP with per-client sessions and 3min inactivity cleanup
- Throughput calculation every 500ms
- Memory stats via `runtime.ReadMemStats`

**internal/proxy/socks5.go**
- Dedicated SOCKS5 proxy (separate from tunnel)
- Connection logging (last 1000 entries with timestamp/addr/target/status/bytes)
- Rate limiting via connection semaphore
- Throughput and system metrics

**internal/dashboard/dashboard.go**
- Real-time metrics collection (1s interval, 360 points = 6min history)
- 5 API endpoints
- Responsive HTML/CSS dashboard with live auto-refresh
- Canvas-based throughput chart
- Connection log table

---

## Performance Tips

1. **Use 5GHz hotspot** — lower interference, higher throughput
2. **Keep server plugged in** — proxy drains battery
3. **Close other apps** on both phones to free bandwidth
4. **Monitor dashboard** — watch for drops or high latency
5. **Set `ZETPROXY_IP`** explicitly if auto-detection picks wrong interface
6. **Adjust `ZETPROXY_MAX_CONNS`** based on phone capabilities
7. **Use tmux** for persistent background sessions
8. **Set TCP buffer sizes** — app uses 256KB read/write buffers

---

## Troubleshooting

| Problem | Fix |
|---------|-----|
| Dashboard not loading | Check server: `ps aux \| grep zetproxyd` |
| Connection refused | Check port not blocked by firewall/Android |
| "connection failed" | Verify hotspot IP, not `127.0.0.1` |
| "address already in use" | Change port via env vars |
| SOCKS5 works, HTTP doesn't | Use SOCKS5 type in proxy app |
| Phone can't reach IP | Both phones on **same hotspot** |
| High memory | Memory is recycled; restart if needed |
| Connections rejected | Increase `ZETPROXY_MAX_CONNS` |

---

## License

College Project — MIT
