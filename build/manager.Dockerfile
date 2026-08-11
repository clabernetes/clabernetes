# syntax=docker/dockerfile:1

ARG BUILDPLATFORM=linux/amd64

FROM --platform=${BUILDPLATFORM} golang:1.25-bookworm AS builder

WORKDIR /clabernetes

RUN mkdir build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# certificates and subdirs need to be owned by root group for openshift reasons -- otherwise we
# get permission denied issues when the controller tries to create ca/client subdirs
RUN mkdir -p certificates/ca certificates/client certificates/webhook && \
    chgrp -R root /clabernetes/certificates && \
    chmod -R 0770 /clabernetes/certificates

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

FROM gcr.io/distroless/static-debian12:nonroot

LABEL org.opencontainers.image.source="https://github.com/clabernetes/clabernetes"

WORKDIR /clabernetes
COPY --from=builder --chown=nonroot:root /clabernetes/certificates /clabernetes/certificates
COPY --from=builder /clabernetes/build/manager .
USER nonroot:nonroot

ENTRYPOINT ["/clabernetes/manager", "run"]
