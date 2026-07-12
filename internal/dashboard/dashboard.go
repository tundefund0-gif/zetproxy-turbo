package dashboard

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"os"
	"net/http"
	"runtime"
	"sync"
	"time"

	"github.com/user/zetproxy/internal/proxy"
	"github.com/user/zetproxy/internal/tunnel"
)

type Server struct {
	addr     string
	tun      *tunnel.Server
	socks    *proxy.SOCKS5Server
	start    time.Time
	httpSrv  *http.Server
	metrics  []MetricPoint
	metricsMu sync.Mutex
	maxMetrics int
}

type MetricPoint struct {
	Timestamp  int64   `json:"t"`
	BytesIn    int64   `json:"bytes_in"`
	BytesOut   int64   `json:"bytes_out"`
	Throughput float64 `json:"throughput"`
	Conns      int32   `json:"conns"`
}

func NewServer(addr string, t *tunnel.Server, s *proxy.SOCKS5Server) *Server {
	return &Server{
		addr:       addr,
		tun:        t,
		socks:      s,
		start:      time.Now(),
		maxMetrics: 360,
	}
}

func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/stats", s.handleStats)
	mux.HandleFunc("/api/metrics", s.handleMetrics)
	mux.HandleFunc("/api/connections", s.handleConnections)
	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/config", s.handleConfig)

	s.httpSrv = &http.Server{
		Addr:         s.addr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go s.collectMetrics(ctx)

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.httpSrv.Shutdown(shutdownCtx)
	}()

	log.Printf("[Dashboard] Web UI at http://%s", s.addr)
	return s.httpSrv.ListenAndServe()
}

func (s *Server) collectMetrics(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tunStats := s.tun.GetStats()
			socksStats := s.socks.GetStats()

			s.metricsMu.Lock()
			s.metrics = append(s.metrics, MetricPoint{
				Timestamp:  time.Now().Unix(),
				BytesIn:    tunStats.BytesIn + socksStats.BytesIn,
				BytesOut:   tunStats.BytesOut + socksStats.BytesOut,
				Throughput: tunStats.ThroughputIn + socksStats.ThroughputIn,
				Conns:      tunStats.ConnsActive + socksStats.ConnsActive,
			})
			if len(s.metrics) > s.maxMetrics {
				s.metrics = s.metrics[len(s.metrics)-s.maxMetrics:]
			}
			s.metricsMu.Unlock()
		}
	}
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(indexHTML))
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	tunStats := s.tun.GetStats()
	socksStats := s.socks.GetStats()
	uptime := time.Since(s.start).Seconds()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	data := map[string]interface{}{
		"bytes_in":         tunStats.BytesIn + socksStats.BytesIn,
		"bytes_out":        tunStats.BytesOut + socksStats.BytesOut,
		"conns_active":     tunStats.ConnsActive + socksStats.ConnsActive,
		"conns_total":      tunStats.ConnsTotal + socksStats.ConnsTotal,
		"conns_rejected":   tunStats.ConnsRejected + socksStats.ConnsRejected,
		"conns_failed":     tunStats.ConnsFailed + socksStats.ConnsFailed,
		"throughput_in":    tunStats.ThroughputIn + socksStats.ThroughputIn,
		"throughput_out":   tunStats.ThroughputOut + socksStats.ThroughputOut,
		"socks_bytes_in":   socksStats.BytesIn,
		"socks_bytes_out":  socksStats.BytesOut,
		"socks_conns":      socksStats.ConnsActive,
		"socks_total":      socksStats.ConnsTotal,
		"socks_failed":     socksStats.ConnsFailed,
		"socks_rejected":   socksStats.ConnsRejected,
		"tcp_accepts":      tunStats.TCPAccepts,
		"udp_packets":      tunStats.UDPPackets,
		"uptime_sec":       uptime,
		"local_ip":         getPreferredIP(),
		"all_ips":          getAllIPs(),
		"tunnel_url":       tunnel.GetTunnelURL(),
		"mem_alloc_mb":     m.Alloc / 1024 / 1024,
		"mem_sys_mb":       m.Sys / 1024 / 1024,
		"num_goroutine":    runtime.NumGoroutine(),
		"num_cpu":          runtime.NumCPU(),
		"start_time":       s.start.Unix(),
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(data)
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	s.metricsMu.Lock()
	metrics := make([]MetricPoint, len(s.metrics))
	copy(metrics, s.metrics)
	s.metricsMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(metrics)
}

func (s *Server) handleConnections(w http.ResponseWriter, r *http.Request) {
	logs := s.socks.GetConnLogs()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(logs)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    "ok",
		"uptime":    time.Since(s.start).Seconds(),
		"timestamp": time.Now().Unix(),
	})
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"socks5_port":   getEnv("ZETPROXY_SOCKS5", ":1088"),
		"tcp_port":      getEnv("ZETPROXY_TCP", ":8888"),
		"udp_port":      getEnv("ZETPROXY_UDP", ":8889"),
		"dashboard_port": getEnv("ZETPROXY_DASHBOARD", ":9092"),
		"override_ip":   os.Getenv("ZETPROXY_IP"),
		"max_conns":     4096,
		"version":       "2.0.0-turbo",
	})
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getPreferredIP() string {
	if ip := os.Getenv("ZETPROXY_IP"); ip != "" {
		return ip
	}
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

// Deprecated — kept for reference
var _ = isPrivateIP

const indexHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>ZetProxy Turbo — Dashboard</title>
<style>
*{margin:0;padding:0;box-sizing:border-box}
:root{--bg:#0a0e17;--card:#121829;--card2:#1a2332;--accent:#00f0ff;--green:#22d45c;--orange:#f59e0b;--red:#ef4444;--purple:#a855f7;--text:#e2e8f0;--muted:#64748b;--border:rgba(255,255,255,0.06)}
body{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,monospace;background:var(--bg);color:var(--text);min-height:100vh;overflow-x:hidden}
.container{max-width:1200px;margin:0 auto;padding:16px}
.header{display:flex;justify-content:space-between;align-items:center;margin-bottom:20px;flex-wrap:wrap;gap:12px}
.logo{font-size:28px;font-weight:900;letter-spacing:-2px;font-family:monospace}
.logo span{color:var(--accent);text-shadow:0 0 20px rgba(0,240,255,0.3)}
.badge{padding:6px 14px;border-radius:20px;font-size:11px;font-weight:700;text-transform:uppercase;letter-spacing:1px}
.badge-live{background:var(--green);color:#000;animation:pulse 2s infinite}
.badge-socks{background:var(--orange);color:#000}
@keyframes pulse{0%,100%{opacity:1}50%{opacity:0.7}}
.server-bar{display:flex;gap:12px;margin-bottom:20px;flex-wrap:wrap}
.server-chip{background:var(--card);border:1px solid var(--border);border-radius:8px;padding:8px 14px;font-size:12px;font-family:monospace;display:flex;align-items:center;gap:6px}
.server-chip code{color:var(--accent);font-weight:600}
.grid{display:grid;grid-template-columns:repeat(4,1fr);gap:12px;margin-bottom:20px}
.card{background:var(--card);border:1px solid var(--border);border-radius:12px;padding:16px;position:relative;overflow:hidden}
.card::before{content:'';position:absolute;top:0;left:0;right:0;height:2px}
.card.throughput::before{background:linear-gradient(90deg,var(--accent),var(--purple))}
.card.conns::before{background:linear-gradient(90deg,var(--green),var(--accent))}
.card.socks::before{background:linear-gradient(90deg,var(--orange),var(--red))}
.card.data::before{background:linear-gradient(90deg,var(--purple),var(--accent))}
.card.system::before{background:linear-gradient(90deg,var(--green),var(--orange))}
.card.full{grid-column:1/-1}
.card.wide{grid-column:span 2}
.label{font-size:10px;color:var(--muted);text-transform:uppercase;letter-spacing:1.5px;margin-bottom:8px;font-weight:600}
.value{font-size:28px;font-weight:800;font-variant-numeric:tabular-nums;font-family:monospace;line-height:1}
.value.sm{font-size:20px}
.value.accent{color:var(--accent)}
.value.green{color:var(--green)}
.value.orange{color:var(--orange)}
.value.red{color:var(--red)}
.value.purple{color:var(--purple)}
.unit{font-size:12px;color:var(--muted);font-weight:400;margin-left:4px}
.sub{font-size:11px;color:var(--muted);margin-top:6px}
.chart-container{background:var(--card);border:1px solid var(--border);border-radius:12px;padding:16px;margin-bottom:20px}
.chart-title{font-size:10px;color:var(--muted);text-transform:uppercase;letter-spacing:1.5px;margin-bottom:12px;font-weight:600}
canvas{width:100%!important;height:120px!important}
.log-container{background:var(--card);border:1px solid var(--border);border-radius:12px;padding:16px;max-height:300px;overflow-y:auto}
.log-entry{display:flex;gap:12px;padding:8px 0;border-bottom:1px solid var(--border);font-size:12px;font-family:monospace}
.log-entry:last-child{border-bottom:none}
.log-time{color:var(--muted);min-width:80px}
.log-addr{color:var(--accent);min-width:140px}
.log-target{color:var(--text);flex:1}
.log-status{min-width:70px;text-align:right}
.log-status.ok{color:var(--green)}
.log-status.fail{color:var(--red)}
.grid-2{display:grid;grid-template-columns:1fr 1fr;gap:12px;margin-bottom:20px}
.footer{text-align:center;font-size:11px;color:var(--muted);padding:20px;font-family:monospace}
@media(max-width:900px){.grid{grid-template-columns:repeat(2,1fr)}.grid-2{grid-template-columns:1fr}.card.wide{grid-column:span 1}}
@media(max-width:600px){.grid{grid-template-columns:1fr}.value{font-size:24px}}
</style>
</head>
<body>
<div class="container">
<div class="header">
<div class="logo">ZET<span>PROXY</span> <span style="font-size:14px;color:var(--muted);font-weight:400">TURBO v2.0</span></div>
<div><span class="badge badge-socks" id="socks-badge">SOCKS5</span> <span class="badge badge-live" id="status-badge">LIVE</span></div>
</div>
<div class="server-bar" id="server-bar"></div>
<div class="grid">
<div class="card throughput">
<div class="label">Throughput In</div>
<div class="value accent" id="thr-in">0<span class="unit">Mbps</span></div>
<div class="sub" id="thr-in-sub">0 B/s</div>
</div>
<div class="card throughput">
<div class="label">Throughput Out</div>
<div class="value purple" id="thr-out">0<span class="unit">Mbps</span></div>
<div class="sub" id="thr-out-sub">0 B/s</div>
</div>
<div class="card conns">
<div class="label">Active Connections</div>
<div class="value green" id="conns">0</div>
<div class="sub" id="conns-sub">0 total</div>
</div>
<div class="card socks">
<div class="label">SOCKS5 Active</div>
<div class="value orange" id="socks-active">0</div>
<div class="sub" id="socks-sub">0 failed</div>
</div>
</div>
<div class="grid">
<div class="card data">
<div class="label">Total Data</div>
<div class="value" id="total">0<span class="unit">MB</span></div>
</div>
<div class="card data">
<div class="label">Data In / Out</div>
<div class="value sm" id="data-io">0 / 0<span class="unit">MB</span></div>
</div>
<div class="card system">
<div class="label">Uptime</div>
<div class="value accent" id="uptime">0s</div>
</div>
<div class="card system">
<div class="label">Memory</div>
<div class="value green" id="memory">0<span class="unit">MB</span></div>
<div class="sub" id="memory-sub">0 goroutines</div>
</div>
</div>
<div class="chart-container">
<div class="chart-title">Throughput History (last 6 min)</div>
<canvas id="chart"></canvas>
</div>
<div class="grid-2">
<div class="log-container">
<div class="chart-title">Recent Connections</div>
<div id="conn-log">Loading...</div>
</div>
<div class="card">
<div class="chart-title">System Info</div>
<div id="sys-info" style="font-size:12px;font-family:monospace;line-height:2"></div>
</div>
</div>
<div class="footer">ZetProxy Turbo v2.0 — Ultra-Fast SOCKS5 + TCP/UDP Tunnel</div>
</div>
<script>
let chartData=[];
const maxChartPoints=120;
function formatBytes(b){if(b<1024)return b.toFixed(0)+' B';if(b<1048576)return(b/1024).toFixed(1)+' KB';if(b<1073741824)return(b/1048576).toFixed(1)+' MB';return(b/1073741824).toFixed(2)+' GB'}
function formatSpeed(mbps){if(mbps<1)return(mbps*1000).toFixed(0)+' Kbps';return mbps.toFixed(1)+' Mbps'}
function formatTime(s){const h=Math.floor(s/3600),m=Math.floor((s%3600)/60),sec=Math.floor(s%60);return(h>0?h+'h ':'')+(m>0?m+'m ':'')+sec+'s'}
function drawChart(){const c=document.getElementById('chart'),ctx=c.getContext('2d');const dpr=window.devicePixelRatio||1;const rect=c.getBoundingClientRect();c.width=rect.width*dpr;c.height=120*dpr;ctx.scale(dpr,dpr);const w=rect.width,h=120;ctx.clearRect(0,0,w,h);if(chartData.length<2)return;const maxVal=Math.max(...chartData.map(d=>d.throughput),1);const step=w/(maxChartPoints-1);ctx.beginPath();ctx.strokeStyle='#00f0ff';ctx.lineWidth=2;ctx.lineJoin='round';chartData.forEach((d,i)=>{const x=i*step;const y=h-((d.throughput/maxVal)*(h-20))-10;if(i===0)ctx.moveTo(x,y);else ctx.lineTo(x,y)});ctx.stroke();const grad=ctx.createLinearGradient(0,0,0,h);grad.addColorStop(0,'rgba(0,240,255,0.15)');grad.addColorStop(1,'rgba(0,240,255,0)');ctx.lineTo(w,h);ctx.lineTo(0,h);ctx.closePath();ctx.fillStyle=grad;ctx.fill();ctx.fillStyle='#64748b';ctx.font='10px monospace';ctx.fillText(formatSpeed(maxVal)+' max',4,12);ctx.fillText('0',4,h-4)}
async function fetchStats(){try{const r=await fetch('/api/stats');const d=await r.json();const ip=d.local_ip||'127.0.0.1';const allIps=d.all_ips||[ip];const tun=d.tunnel_url||'';let bar='';allIps.forEach(function(addr){const label=addr.startsWith('100.')?'TAILSCALE':'LAN';bar+='<div class="server-chip">'+label+' SOCKS5: <code>'+addr+':1088</code></div><div class="server-chip">'+label+' Dash: <code>http://'+addr+':9092</code></div>'});if(tun){bar+='<div class="server-chip" style="border-color:var(--accent)">PUBLIC SOCKS5: <code>'+tun+'</code></div>'};document.getElementById('server-bar').innerHTML=bar;document.getElementById('thr-in').innerHTML=formatSpeed(d.throughput_in).replace(/ /,'<span class="unit">')+'</span>';document.getElementById('thr-in-sub').textContent=formatBytes(d.bytes_in)+'/s';document.getElementById('thr-out').innerHTML=formatSpeed(d.throughput_out).replace(/ /,'<span class="unit">')+'</span>';document.getElementById('thr-out-sub').textContent=formatBytes(d.bytes_out)+'/s';document.getElementById('conns').textContent=d.conns_active;document.getElementById('conns-sub').textContent=d.conns_total+' total';document.getElementById('socks-active').textContent=d.socks_conns;document.getElementById('socks-sub').textContent=d.socks_failed+' failed';const totalBytes=d.bytes_in+d.bytes_out;document.getElementById('total').innerHTML=formatBytes(totalBytes).replace(/ /,'<span class="unit">')+'</span>';document.getElementById('data-io').innerHTML=formatBytes(d.bytes_in)+' / '+formatBytes(d.bytes_out)+'<span class="unit"></span>';document.getElementById('uptime').textContent=formatTime(d.uptime_sec);document.getElementById('memory').innerHTML=d.mem_alloc_mb+'<span class="unit">MB</span>';document.getElementById('memory-sub').textContent=d.num_goroutine+' goroutines';document.getElementById('socks-badge').textContent='SOCKS5 '+(d.socks_conns>0?'LINKED':'IDLE');document.getElementById('sys-info').innerHTML='<div>CPU Cores: '+d.num_cpu+'</div><div>Memory Sys: '+d.mem_sys_mb+' MB</div><div>TCP Accepts: '+d.tcp_accepts+'</div><div>UDP Packets: '+d.udp_packets+'</div><div>Rejected: '+d.conns_rejected+'</div><div>Started: '+new Date(d.start_time*1000).toLocaleString()+'</div>';chartData.push({throughput:d.throughput_in+d.throughput_out});if(chartData.length>maxChartPoints)chartData.shift();drawChart()}catch(e){}}
async function fetchConnections(){try{const r=await fetch('/api/connections');const logs=await r.json();const el=document.getElementById('conn-log');if(!logs||logs.length===0){el.innerHTML='<div style="color:var(--muted);padding:20px;text-align:center">No connections yet</div>';return}const recent=logs.slice(-20).reverse();el.innerHTML=recent.map(l=>'<div class="log-entry"><span class="log-time">'+new Date(l.timestamp).toLocaleTimeString()+'</span><span class="log-addr">'+l.addr+'</span><span class="log-target">'+l.target+'</span><span class="log-status '+(l.status==='connected'?'ok':'fail')+'">'+l.status+'</span></div>').join('')}catch(e){}}
setInterval(fetchStats,1000);setInterval(fetchConnections,3000);fetchStats();fetchConnections();
window.addEventListener('resize',drawChart);
</script>
</body>
</html>`
