FROM golang:1.25-alpine AS builder
WORKDIR /srv
COPY go.mod ./
RUN go mod download
COPY . .
RUN go build -o /bin/engine ./cmd/engine

FROM alpine:3.20
RUN adduser -D -u 1000 engine && mkdir -p /data && chown engine:engine /data
WORKDIR /srv
COPY --from=builder /bin/engine /bin/engine
VOLUME /data
EXPOSE 9100
USER engine
ENTRYPOINT ["/bin/engine", "--dir", "/data", "--addr", ":9100"]
