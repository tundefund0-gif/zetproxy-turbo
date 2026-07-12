# ZetProxy Turbo — Ultra-Fast Proxy Tunnel

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

# Test SOCKS5 proxy locally
curl --socks5-hostname 127.0.0.1:1088 -s -o /dev/null -w '%{http_code}' http://google.com
# Should return 200 or 301
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

![Dashboard](https://via.placeholder.com/400x200?text=ZetProxy+Dashboard)

Shows:
- **Throughput** — current Mbps
- **Active Connections** — live count
- **SOCKS5 Active** — currently tunnelled clients
- **Total Data** — data transferred since start
- **Uptime** — how long the server has been running

API: `http://192.168.218.187:9092/api/stats` returns JSON.

---

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `ZETPROXY_TCP` | `:8888` | TCP tunnel (auto-detects SOCKS5/HTTP CONNECT/HTTP proxy) |
| `ZETPROXY_UDP` | `:8889` | UDP tunnel port |
| `ZETPROXY_SOCKS5` | `:1088` | Dedicated SOCKS5 proxy port |
| `ZETPROXY_DASHBOARD` | `:9092` | Web dashboard port |
| `ZETPROXY_IP` | *(auto)* | Override advertised IP in dashboard/logs |

Example with custom ports:
```bash
ZETPROXY_SOCKS5=:1080 ZETPROXY_TCP=:8080 ZETPROXY_IP=192.168.1.100 ./zetproxyd
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
zetproxy/
├── cmd/
│   └── zetproxyd/
│       └── main.go              # Entry point, server orchestration
├── internal/
│   ├── tunnel/
│   │   └── tunnel.go            # TCP/UDP tunnel with protocol auto-detect
│   ├── proxy/
│   │   └── socks5.go            # Dedicated SOCKS5 proxy server
│   ├── dashboard/
│   │   └── dashboard.go         # Web dashboard + live stats API
│   └── pool/
│       └── pool.go              # 64KB buffer pool for zero-copy relay
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

---

## License

College Project — MIT
