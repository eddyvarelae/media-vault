# Stage 1 — build the Go binary
FROM golang:1.23-alpine AS builder
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/vault ./cmd/vault

# Stage 2 — minimal runtime
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /out/vault /usr/local/bin/vault
ENV VAULT_CONFIG=/config
VOLUME ["/sources", "/dest", "/config"]
EXPOSE 8765
ENTRYPOINT ["vault"]
