FROM golang:1.24-alpine AS build
WORKDIR /app
COPY go.mod ./
COPY main.go ./
RUN go build -o screengate .

FROM alpine:3.21
COPY --from=build /app/screengate /screengate
EXPOSE 8080
CMD ["/screengate"]
