FROM golang:1.25-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
COPY migrations ./migrations
COPY templates ./templates
COPY cmd/client ./cmd/client
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o screengate .
RUN CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-s -w -H=windowsgui" -o screengate-client.exe ./cmd/client

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S -g 10001 screengate && adduser -S -D -H -u 10001 -G screengate screengate && \
    mkdir /data && chown screengate:screengate /data
ENV TZ=Europe/Oslo SCREENGATE_TIMEZONE=Europe/Oslo DATABASE_PATH=/data/screengate.db
COPY --from=build /app/screengate /screengate
COPY --from=build /app/screengate-client.exe /client/screengate-client.exe
USER screengate
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1
CMD ["/screengate"]
