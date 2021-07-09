FROM golang:1.16.5 AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download 
COPY main.go ./
RUN CGO_ENABLED=0 go build -ldflags='-buildid= -w -s'

FROM alpine:3.14
COPY --from=builder /src/groundwork /app/groundwork
ENTRYPOINT ["/app/groundwork"]