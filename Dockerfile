# Cross-compiles on the build machine (no emulation), runs as non-root on a distroless base.
FROM --platform=$BUILDPLATFORM golang:1.27.3 AS build
ARG TARGETOS TARGETARCH VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o /analyzer ./cmd/analyzer

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /analyzer /analyzer
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/analyzer"]
