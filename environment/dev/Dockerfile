FROM coveo/ecs-exporter_base:latest
USER root
RUN apk add --no-cache g++

USER ecs-exporter

RUN go get github.com/Masterminds/glide
RUN go get github.com/golang/mock/mockgen
