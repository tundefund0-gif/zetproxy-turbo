FROM golang:1.22-alpine AS builder
WORKDIR /build
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /relay ./cmd/relay

FROM scratch
COPY --from=builder /relay /relay
CMD ["/relay"]
