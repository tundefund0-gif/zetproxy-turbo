package dashboard

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/user/zetproxy/internal/tunnel"
)

type Server struct {
	addr   string
	tunnel *tunnel.Server
	start  time.Time
	conns  int64
}

func NewServer(addr string, t *tunnel.Server) *Server {
	return &Server{addr: addr, tunnel: t, start: time.Now()}
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

	log.Printf("[Web] Dashboard at http://%s", s.addr)
	return server.ListenAndServe()
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(indexHTML))
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	stats := s.tunnel.GetStats()
	uptime := time.Since(s.start).Seconds()

	data := map[string]interface{}{
		"bytes_in":       stats.BytesIn,
		"bytes_out":      stats.BytesOut,
		"conns_active":   stats.ConnsActive,
		"conns_total":    stats.ConnsTotal,
		"throughput_in":  stats.ThroughputIn,
		"uptime_sec":     uptime,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

const indexHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>ZetProxy</title>
<style>
*{margin:0;padding:0;box-sizing:border-box}
:root{--bg:#0a0e17;--card:#121829;--accent:#00f0ff;--green:#22d45c;--text:#e2e8f0;--muted:#64748b}
body{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;background:var(--bg);color:var(--text);padding:20px;min-height:100vh}
.header{display:flex;justify-content:space-between;align-items:center;margin-bottom:24px}
.logo{font-size:24px;font-weight:800;letter-spacing:-1px}
.logo span{color:var(--accent)}
.badge{background:var(--green);color:#000;padding:4px 12px;border-radius:20px;font-size:11px;font-weight:700}
.grid{display:grid;grid-template-columns:1fr 1fr;gap:12px}
.card{background:var(--card);border-radius:16px;padding:20px;border:1px solid rgba(255,255,255,0.05)}
.card.full{grid-column:1/-1}
.label{font-size:11px;color:var(--muted);text-transform:uppercase;letter-spacing:1px;margin-bottom:6px}
.value{font-size:32px;font-weight:700;font-variant-numeric:tabular-nums}
.value.green{color:var(--green)}
.value.accent{color:var(--accent)}
.unit{font-size:14px;color:var(--muted);font-weight:400;margin-left:4px}
.footer{text-align:center;font-size:11px;color:var(--muted);margin-top:24px;padding:12px}
</style>
</head>
<body>
<div class="header">
<div class="logo">Zet<span>Proxy</span></div>
<span class="badge">LIVE</span>
</div>
<div class="grid">
<div class="card">
<div class="label">Throughput In</div>
<div class="value accent" id="thr-in">0<span class="unit">Mbps</span></div>
</div>
<div class="card">
<div class="label">Active Conns</div>
<div class="value green" id="conns">0</div>
</div>
<div class="card">
<div class="label">Total Data</div>
<div class="value" id="total">0<span class="unit">MB</span></div>
</div>
<div class="card">
<div class="label">Uptime</div>
<div class="value accent" id="uptime">0s</div>
</div>
</div>
<div class="footer">ZetProxy v1.0 — Raw Speed</div>
<script>
async function fetchStats(){
try{
const r=await fetch('/api/stats');
const d=await r.json();
document.getElementById('thr-in').innerHTML=d.throughput_in.toFixed(1)+'<span class="unit">Mbps</span>';
document.getElementById('conns').textContent=d.conns_active;
const mb=(d.bytes_in+d.bytes_out)/(1024*1024);
document.getElementById('total').innerHTML=mb.toFixed(0)+'<span class="unit">MB</span>';
const u=Math.floor(d.uptime_sec);
const h=Math.floor(u/3600),m=Math.floor((u%3600)/60),s=u%60;
document.getElementById('uptime').textContent=(h>0?h+'h ':'')+(m>0?m+'m ':'')+s+'s';
}catch(e){}
}
setInterval(fetchStats,2000);
fetchStats();
</script>
</body>
</html>`
