# The following are targers that do not exist in the filesystem as real files and should be always executed by make
.PHONY: default deps login base build dev shell start stop image push test

# Name of this service/application
SERVICE_NAME := ecs-exporter

# Docker image name for this project
IMAGE_NAME := slok/$(SERVICE_NAME)

# Get the main unix group for the user running make (to be used by docker-compose later)
GID := $(shell id -g)

# Get the unix user id for the user running make (to be used by docker-compose later)
UID := $(shell id -u)

# Get the username of theuser running make. On the devbox, we give priority to /etc/username
USERNAME ?= $(shell ( [ -f /etc/username ] && cat /etc/username  ) || whoami)

# File to keep track of the last login to the docker registry, so that login is not ran every time
LOGIN_FILE := ~/.devlogin

# Bash history file for container shell
HISTORY_FILE := ~/.bash_history.$(SERVICE_NAME)

# Try to detect current branch if not provided from environment
BRANCH ?= $(shell git rev-parse --abbrev-ref HEAD)

# Commit hash from git
COMMIT=$(shell git rev-parse --short HEAD)

# Remove login flag if older than 10 hours
_ := $(shell find $(LOGIN_FILE) -mmin +600 -delete)

# The default action of this Makefile is to build the development docker image
default: build


# Build the base docker image which is shared between the development and production images
base:
	docker build -t $(IMAGE_NAME)_base:latest .

# Build the development docker image
build: base
	cd environment/dev && docker-compose build

# Run the development environment in non-daemonized mode (foreground)
dev: build
	cd environment/dev && \
	( docker-compose up; \
		docker-compose stop; \
		docker-compose rm -f; )

# Run a shell into the development docker image
shell: build
	-touch $(HISTORY_FILE)
	cd environment/dev && docker-compose run --service-ports --rm $(SERVICE_NAME) /bin/bash

# Run the development environment in the background
start: build
	cd environment/dev && \
		docker-compose up -d

# Stop the development environment (background and/or foreground)
stop:
	cd environment/dev && ( \
		docker-compose stop; \
		docker-compose rm -f; \
		)

# Build release, target on /bin
build_release:build
		cd environment/dev && docker-compose run --rm $(SERVICE_NAME) /bin/bash -c "./build.sh"

# Update project dependencies to tle latest version
dep_update:build
	cd environment/dev && docker-compose run --rm $(SERVICE_NAME) /bin/bash -c 'glide up --strip-vcs --update-vendored'
# Install new dependency make dep_install args="github.com/Sirupsen/logrus"
dep_install:build
		cd environment/dev && docker-compose run --rm $(SERVICE_NAME) /bin/bash -c 'glide get --strip-vcs $(args)'

# Pass the golang vet check
vet: build
	cd environment/dev && docker-compose run --rm $(SERVICE_NAME) /bin/bash -c 'go vet `glide nv`'

# Execute unit tests
test:build
	cd environment/dev && docker-compose run --rm $(SERVICE_NAME) /bin/bash -c 'go test `glide nv` --tags="integration" -v'

# Generate required code (mocks...)
gogen: build
	cd environment/dev && docker-compose run --rm $(SERVICE_NAME) /bin/bash -c 'go generate `glide nv`'

# Build the production image
image: base
	docker build \
	--label version=$(COMMIT) \
	-t $(SERVICE_NAME) \
	-t $(REPOSITORY):latest \
	-t $(REPOSITORY):$(COMMIT) \
	-t $(REPOSITORY):$(BRANCH) \
	-f environment/prod/Dockerfile \
	environment/prod
