# Estágio 1: Build estático em Go
FROM golang:1.22-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /coldchain ./cmd/coldchain

# Estágio 2: Container final scratch ultra-enxuto
FROM scratch

COPY --from=builder /coldchain /app/coldchain

USER 10001:10001
EXPOSE 5000

ENTRYPOINT ["/app/coldchain"]
