FROM golang:1.15-alpine as build
RUN apk add --no-cache git

WORKDIR /build
COPY go.* *.go ./
COPY bot/ ./bot
RUN go get -v ./... \
    && CGO_ENABLED=0 go build

FROM alpine:latest
COPY --from=build /build/gitbot /app/gitbot
ENTRYPOINT /app/gitbot