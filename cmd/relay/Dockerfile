FROM golang:1.22-alpine AS builder
WORKDIR /build
COPY . .
RUN go build -ldflags="-s -w" -o /relay ./cmd/relay

FROM alpine:3.19
RUN apk add --no-cache ca-certificates
COPY --from=builder /relay /relay
EXPOSE 7800
CMD ["/relay"]
