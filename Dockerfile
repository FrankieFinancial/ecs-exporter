FROM golang:1.11 as builder
WORKDIR /go/src/github.com/coveo/ecs-exporter/
ADD  . .
RUN go build -o ecs-exporter --ldflags "-w -linkmode external -extldflags '-static'" ./cmd/ecs-exporter


FROM alpine:3.8
RUN apk --no-cache add ca-certificates
WORKDIR /app
COPY --from=builder /go/src/github.com/coveo/ecs-exporter/ecs-exporter .
# Create user
ARG uid=1000
ARG gid=1000
ARG username="ecs-exporter"
RUN addgroup -g $gid $username
RUN adduser -D -u $uid -G $username $username
RUN chown -R $username:$username /app

# Run container as $username
USER $username

ENTRYPOINT ["./ecs-exporter"]


