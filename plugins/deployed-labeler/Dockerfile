FROM golang:1.16.5 AS builder

WORKDIR /src/plugin
COPY go.mod go.sum ./
RUN go mod download 
COPY *.go ./
RUN CGO_ENABLED=0 go build -ldflags='-buildid= -w -s'

FROM alpine:3.14
COPY --from=builder /src/plugin/deployed-labeler /app/deployed-labeler
ENTRYPOINT ["/app/deployed-labeler"]