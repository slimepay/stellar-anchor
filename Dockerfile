FROM golang:1.25-alpine AS builder

WORKDIR /src

RUN apk add --no-cache ca-certificates tzdata git

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/stellar-anchor ./cmd/server

FROM alpine:3.20 AS runner

WORKDIR /app

RUN apk add --no-cache ca-certificates tzdata && addgroup -S anchor && adduser -S anchor -G anchor

COPY --from=builder /out/stellar-anchor /app/stellar-anchor

EXPOSE 8081

USER anchor

CMD ["/app/stellar-anchor"]
