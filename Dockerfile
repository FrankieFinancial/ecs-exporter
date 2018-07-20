FROM golang:1.9 as builder
WORKDIR /go/src/github.com/slok/ecs-exporter/
ADD  . .
RUN go build -o ecs-exporter --ldflags "-w -linkmode external -extldflags '-static'" ./cmd/ecs-exporter


FROM alpine:3.8
RUN apk --no-cache add ca-certificates
WORKDIR /app
COPY --from=builder /go/src/github.com/slok/ecs-exporter/ecs-exporter .
ENTRYPOINT ["./ecs-exporter"]


