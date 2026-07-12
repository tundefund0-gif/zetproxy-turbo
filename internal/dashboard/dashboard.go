package dashboard

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"os"
	"net/http"
	"time"

	"github.com/user/zetproxy/internal/proxy"
	"github.com/user/zetproxy/internal/tunnel"
)

// Server is the web dashboard
type Server struct {
	addr  string
	tun   *tunnel.Server
	socks *proxy.SOCKS5Server
	start time.Time
}

func NewServer(addr string, t *tunnel.Server, s *proxy.SOCKS5Server) *Server {
	return &Server{addr: addr, tun: t, socks: s, start: time.Now()}
}

func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/stats", s.handleStats)

	server := &http.Server{Addr: s.addr, Handler: mux}
	go func() {
		<-ctx.Done()
		server.Shutdown(context.Background())
	}()

	log.Printf("[Dashboard] Web UI at http://%s", s.addr)
	return server.ListenAndServe()
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(indexHTML))
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	tunStats := s.tun.GetStats()
	socksStats := s.socks.GetStats()
	uptime := time.Since(s.start).Seconds()

	data := map[string]interface{}{
		"bytes_in":        tunStats.BytesIn,
		"bytes_out":       tunStats.BytesOut,
		"conns_active":    tunStats.ConnsActive,
		"conns_total":     tunStats.ConnsTotal,
		"throughput_in":   tunStats.ThroughputIn,
		"socks_bytes_in":  socksStats.BytesIn,
		"socks_bytes_out": socksStats.BytesOut,
		"socks_conns":     socksStats.ConnsActive + tunStats.ConnsActive,
		"socks_active":    socksStats.ConnsActive,
		"socks_total":     socksStats.ConnsTotal,
		"uptime_sec":      uptime,
		"local_ip":        getPreferredIP(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}


func getPreferredIP() string {
	// Allow override via env var
	if ip := os.Getenv("ZETPROXY_IP"); ip != "" {
		return ip
	}

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
					// Also 172.16-31.x.x
					if ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31 {
						return ip4.String()
					}
					// Also 10.x.x.x
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

const indexHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>ZetProxy - Dashboard</title>
<style>
*{margin:0;padding:0;box-sizing:border-box}
:root{--bg:#0a0e17;--card:#121829;--accent:#00f0ff;--green:#22d45c;--orange:#f59e0b;--red:#ef4444;--text:#e2e8f0;--muted:#64748b}
body{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;background:var(--bg);color:var(--text);padding:20px;min-height:100vh}
.header{display:flex;justify-content:space-between;align-items:center;margin-bottom:24px;flex-wrap:wrap;gap:12px}
.logo{font-size:24px;font-weight:800;letter-spacing:-1px}
.logo span{color:var(--accent)}
.badge{background:var(--green);color:#000;padding:4px 12px;border-radius:20px;font-size:11px;font-weight:700}
.badge-socks{background:var(--orange);color:#000;padding:4px 12px;border-radius:20px;font-size:11px;font-weight:700}
.server-info{font-size:13px;color:var(--muted);margin-bottom:16px;padding:12px 16px;background:var(--card);border-radius:12px;border:1px solid rgba(255,255,255,0.05)}
.server-info code{color:var(--accent);background:rgba(0,240,255,0.1);padding:2px 8px;border-radius:4px;font-size:12px}
.grid{display:grid;grid-template-columns:1fr 1fr;gap:12px}
.card{background:var(--card);border-radius:16px;padding:20px;border:1px solid rgba(255,255,255,0.05)}
.card.full{grid-column:1/-1}
.card.socks{background:rgba(245,158,11,0.05);border-color:rgba(245,158,11,0.15)}
.label{font-size:11px;color:var(--muted);text-transform:uppercase;letter-spacing:1px;margin-bottom:6px}
.value{font-size:32px;font-weight:700;font-variant-numeric:tabular-nums}
.value.green{color:var(--green)}
.value.accent{color:var(--accent)}
.value.orange{color:var(--orange)}
.unit{font-size:14px;color:var(--muted);font-weight:400;margin-left:4px}
.footer{text-align:center;font-size:11px;color:var(--muted);margin-top:24px;padding:12px}
@media(max-width:600px){.grid{grid-template-columns:1fr}.value{font-size:26px}}
</style>
</head>
<body>
<div class="header">
<div class="logo">Zet<span>Proxy</span></div>
<div>
<span class="badge-socks" id="socks-badge">SOCKS5</span>
<span class="badge" id="status-badge">LIVE</span>
</div>
</div>
<div class="server-info" id="server-info">
<span>🌐 Server: <code>loading...</code></span>
</div>
<div class="grid">
<div class="card">
<div class="label">Throughput</div>
<div class="value accent" id="thr-in">0<span class="unit">Mbps</span></div>
</div>
<div class="card">
<div class="label">Active Connections</div>
<div class="value green" id="conns">0</div>
</div>
<div class="card socks">
<div class="label">SOCKS5 Active</div>
<div class="value orange" id="socks-active">0</div>
</div>
<div class="card">
<div class="label">Total Data</div>
<div class="value" id="total">0<span class="unit">MB</span></div>
</div>
<div class="card">
<div class="label">Uptime</div>
<div class="value accent" id="uptime">0s</div>
</div>
<div class="card">
<div class="label">Total Connections</div>
<div class="value" id="total-conns">0</div>
</div>
</div>
<div class="footer">ZetProxy Turbo — SOCKS5 + TCP/UDP Tunnel</div>
<script>
async function fetchStats(){
try{
const r=await fetch('/api/stats');
const d=await r.json();
const ip=d.local_ip||'127.0.0.1';
document.getElementById('server-info').innerHTML='<span>🌐 SOCKS5 Proxy: <code>'+ip+':1088</code> &middot; TCP: <code>'+ip+':8888</code> &middot; UDP: <code>'+ip+':8889</code></span>';
document.getElementById('thr-in').innerHTML=d.throughput_in.toFixed(1)+'<span class="unit">Mbps</span>';
document.getElementById('conns').textContent=d.conns_active;
document.getElementById('socks-active').textContent=d.socks_active;
const mb=(d.bytes_in+d.bytes_out+d.socks_bytes_in+d.socks_bytes_out)/(1024*1024);
document.getElementById('total').innerHTML=mb.toFixed(0)+'<span class="unit">MB</span>';
document.getElementById('total-conns').textContent=d.socks_total+d.conns_total;
const u=Math.floor(d.uptime_sec);
const h=Math.floor(u/3600),m=Math.floor((u%3600)/60),s=u%60;
document.getElementById('uptime').textContent=(h>0?h+'h ':'')+(m>0?m+'m ':'')+s+'s';
document.getElementById('socks-badge').textContent='SOCKS5 '+(d.socks_active>0?'🔗':'🟢')
}catch(e){}
}
setInterval(fetchStats,2000);
fetchStats();
</script>
</body>
</html>`
