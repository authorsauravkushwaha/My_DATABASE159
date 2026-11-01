FROM golang:1.22-alpine AS builder
WORKDIR /srv
COPY go.mod ./
RUN go mod download
COPY . .
RUN go build -o /bin/gateway .

FROM alpine:3.20
RUN adduser -D -u 1000 gateway
WORKDIR /srv
COPY --from=builder /bin/gateway /bin/gateway
EXPOSE 8100
USER gateway
ENTRYPOINT ["/bin/gateway", "--addr", ":8100", "--storage", "storage:9100"]
