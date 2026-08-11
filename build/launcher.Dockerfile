# syntax=docker/dockerfile:1

ARG BUILDPLATFORM=linux/amd64

FROM --platform=${BUILDPLATFORM} golang:1.25-bookworm AS builder

WORKDIR /clabernetes

RUN mkdir build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION
ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/root/.cache/go-build \
    TARGET_OS="${TARGETOS:-linux}" && \
    TARGET_ARCH="${TARGETARCH:-$(go env GOARCH)}" && \
    CGO_ENABLED=0 \
    GOOS="${TARGET_OS}" \
    GOARCH="${TARGET_ARCH}" \
    go build \
    -ldflags "-s -w -X github.com/clabernetes/clabernetes/constants.Version=${VERSION}" \
    -trimpath \
    -o \
    build/manager \
    cmd/clabernetes/main.go

FROM debian:bookworm-slim

LABEL org.opencontainers.image.source="https://github.com/clabernetes/clabernetes"

SHELL ["/bin/bash", "-o", "pipefail", "-c"]

# BuildKit mounts the optional host CA only for network operations. It is never copied into the
# image filesystem, so the final image retains only its normal public CA bundle.
RUN --mount=type=secret,id=host_ca,target=/run/host_ca,required=false,mode=0444 \
    set -euo pipefail; \
    apt_args=(); \
    if [[ -s /run/host_ca ]]; then \
      apt_args+=(-o "Acquire::https::CAInfo=/run/host_ca"); \
    fi; \
    apt-get "${apt_args[@]}" update && \
    apt-get "${apt_args[@]}" install -yq --no-install-recommends \
    ca-certificates \
    curl \
    wget \
    gnupg \
    lsb-release \
    vim \
    iproute2 \
    tcpdump \
    procps \
    openssh-client \
    inetutils-ping \
    traceroute && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/* /tmp/* /var/tmp/* /var/cache/apt/archive/*.deb

RUN echo "deb [trusted=yes] https://apt.fury.io/netdevops/ /" | \
    tee -a /etc/apt/sources.list.d/netdevops.list

RUN --mount=type=secret,id=host_ca,target=/run/host_ca,required=false,mode=0444 \
    set -euo pipefail; \
    curl_args=(); \
    if [[ -s /run/host_ca ]]; then \
      curl_args+=(--cacert /run/host_ca); \
    fi; \
    curl "${curl_args[@]}" -fsSL https://download.docker.com/linux/debian/gpg | \
    gpg --dearmor -o /usr/share/keyrings/docker-archive-keyring.gpg

RUN echo \
    "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/docker-archive-keyring.gpg] https://download.docker.com/linux/debian \
    $(lsb_release -cs) stable" | tee /etc/apt/sources.list.d/docker.list > /dev/null

ARG DOCKER_VERSION="5:28.*"
ARG CONTAINERLAB_VERSION="0.78.0+"
RUN --mount=type=secret,id=host_ca,target=/run/host_ca,required=false,mode=0444 \
    set -euo pipefail; \
    apt_args=(); \
    if [[ -s /run/host_ca ]]; then \
      apt_args+=(-o "Acquire::https::CAInfo=/run/host_ca"); \
    fi; \
    apt-get "${apt_args[@]}" update && \
    apt-get "${apt_args[@]}" install -yq --no-install-recommends \
    containerlab=${CONTAINERLAB_VERSION} \
    docker-ce=${DOCKER_VERSION} \
    docker-ce-cli=${DOCKER_VERSION} && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/* /tmp/* /var/tmp/* /var/cache/apt/archive/*.deb

ARG TARGETARCH
ARG NERDCTL_VERSION="2.1.4"
RUN --mount=type=secret,id=host_ca,target=/run/host_ca,required=false,mode=0444 \
    set -euo pipefail; \
    curl_args=(); \
    if [[ -s /run/host_ca ]]; then \
      curl_args+=(--cacert /run/host_ca); \
    fi; \
    TARGET_ARCH="${TARGETARCH:-$(dpkg --print-architecture)}" && \
    case "${TARGET_ARCH}" in \
      amd64|arm64) NERDCTL_ARCH="${TARGET_ARCH}" ;; \
      *) echo "unsupported TARGETARCH for nerdctl: ${TARGET_ARCH}" >&2; exit 1 ;; \
    esac && \
    curl "${curl_args[@]}" -L "https://github.com/containerd/nerdctl/releases/download/v${NERDCTL_VERSION}/nerdctl-${NERDCTL_VERSION}-linux-${NERDCTL_ARCH}.tar.gz" | \
      tar -xz -C /usr/bin/ && \
    rm /usr/bin/containerd-rootless*.sh

# https://github.com/docker/cli/issues/4807
RUN sed -i 's/ulimit -Hn/# ulimit -Hn/g' /etc/init.d/docker

# copy a basic but nicer than standard bashrc for the user
COPY build/launcher/.bashrc /root/.bashrc

# copy default ssh keys to the launcher image
# to make use of password-less ssh access
COPY --chmod=0600 build/launcher/default_id_rsa /root/.ssh/id_rsa
COPY build/launcher/default_id_rsa.pub /root/.ssh/id_rsa.pub

# copy custom ssh config to enable easy ssh access from launcher
COPY build/launcher/ssh_config /etc/ssh/ssh_config

# copy sshin command to simplify ssh access to the containers
COPY build/launcher/sshin /usr/local/bin/sshin

# copy shellin command to simplify shell access to the containers
COPY build/launcher/shellin /usr/local/bin/shellin

WORKDIR /clabernetes

RUN mkdir .node .image

COPY --from=builder /clabernetes/build/manager .
USER root

ENTRYPOINT ["/clabernetes/manager", "launch"]
