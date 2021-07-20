FROM golang:1.16.5 AS builder

WORKDIR /src/common
COPY common/* ./

WORKDIR /src/plugin
COPY go.mod go.sum ./
RUN go mod download 
COPY main.go ./
RUN CGO_ENABLED=0 go build -ldflags='-buildid= -w -s'

FROM alpine:3.14
COPY --from=builder /src/plugin/projectmanager /app/projectmanager
ENTRYPOINT ["/app/projectmanager"]