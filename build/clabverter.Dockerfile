# syntax=docker/dockerfile:1

ARG BUILDPLATFORM

FROM --platform=${BUILDPLATFORM} golang:1.25-bookworm AS builder

ARG VERSION
ARG TARGETOS
ARG TARGETARCH

WORKDIR /clabernetes

RUN mkdir build && \
    mkdir work

COPY . .

RUN go mod download

RUN TARGET_OS="${TARGETOS:-linux}" && \
    TARGET_ARCH="${TARGETARCH:-$(go env GOARCH)}" && \
    CGO_ENABLED=0 \
    GOOS="${TARGET_OS}" \
    GOARCH="${TARGET_ARCH}" \
    go build \
    -ldflags "-s -w -X github.com/clabernetes/clabernetes/constants.Version=${VERSION}" \
    -trimpath \
    -a \
    -o \
    build/clabverter \
    cmd/clabverter/main.go

FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /clabernetes
COPY --from=builder --chown=nonroot:nonroot /clabernetes/work /clabernetes/work
COPY --from=builder /clabernetes/build/clabverter .

WORKDIR /clabernetes/work

ENTRYPOINT ["/clabernetes/clabverter"]
