FROM golang:1.25-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY main.go ./
COPY cmd/client ./cmd/client
RUN go build -o screengate .
RUN GOOS=windows GOARCH=amd64 go build -o screengate-client.exe ./cmd/client

FROM alpine:3.21
RUN apk add --no-cache tzdata
ENV TZ=Europe/Oslo
COPY --from=build /app/screengate /screengate
COPY --from=build /app/screengate-client.exe /client/screengate-client.exe
COPY --from=build /app/cmd/client/install.ps1 /client/install.ps1
EXPOSE 8080
CMD ["/screengate"]
