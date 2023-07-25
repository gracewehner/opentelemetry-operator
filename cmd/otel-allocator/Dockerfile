# Build the otel-allocator binary
FROM mcr.microsoft.com/oss/go/microsoft/golang:1.19 as builder

WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./
COPY targetallocator.yaml /conf/targetallocator.yaml

RUN go mod download

COPY . .

ARG TARGETOS TARGETARCH
# Build the Go app
RUN if [ "$TARGETARCH" = "arm64" ] ; then CC=aarch64-linux-gnu-gcc CGO_ENABLED=1 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -a -installsuffix -buildmode=pie -ldflags '-linkmode external -extldflags=-Wl,-z,now' -o main . ; else CGO_ENABLED=1 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -a -installsuffix -buildmode=pie -ldflags '-linkmode external -extldflags=-Wl,-z,now' -o main . ; fi
# RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o main .

######## Start a new stage from scratch #######
#FROM mcr.microsoft.com/cbl-mariner/distroless/debug:2.0
FROM mcr.microsoft.com/cbl-mariner/base/core:2.0
RUN tdnf install -y ca-certificates wget vim curl busybox
RUN mkdir /busybin && busybox --install /busybin
WORKDIR /root/

# Copy the pre-built binary file from the previous stage
COPY --from=builder /app/main .

# Copying targetallocator.yaml file since there is no configmap
COPY --from=builder /conf/targetallocator.yaml /conf/targetallocator.yaml

ENTRYPOINT ["./main"]