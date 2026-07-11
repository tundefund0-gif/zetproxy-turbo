# ZetProxy — Ultra-Fast Proxy Tunnel

Raw-speed TCP/UDP proxy tunnel. No bloat, just speed.

## Quick Start
```bash
go build -o zetproxyd ./cmd/zetproxyd
./zetproxyd
```

## Ports
- TCP Tunnel: :8888
- UDP Tunnel: :8889
- Dashboard: :9092

## Env Config
```bash
ZETPROXY_TCP=:8888 ZETPROXY_UDP=:8889 ZETPROXY_DASHBOARD=:9092 ./zetproxyd
```
