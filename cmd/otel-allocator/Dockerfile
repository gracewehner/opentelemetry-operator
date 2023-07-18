# Build the otel-allocator binary
FROM mcr.microsoft.com/oss/go/microsoft/golang:1.19 as targetallocator-builder
WORKDIR /app
# Copy go mod and sum files
COPY ./opentelemetry-operator/cmd/otel-allocator/go.mod ./opentelemetry-operator/cmd/otel-allocator/go.sum ./opentelemetry-operator/cmd/otel-allocator/
WORKDIR /app/opentelemetry-operator/cmd/otel-allocator/
RUN go mod download
COPY ./opentelemetry-operator/cmd/otel-allocator /app/opentelemetry-operator/cmd/otel-allocator/
# Build the Go app
# RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o main .
RUN if [ "$TARGETARCH" = "arm64" ] ; then CC=aarch64-linux-gnu-gcc CGO_ENABLED=1 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -buildmode=pie -ldflags '-linkmode external -extldflags=-Wl,-z,now' -o targetallocator . ; else CGO_ENABLED=1 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -buildmode=pie -ldflags '-linkmode external -extldflags=-Wl,-z,now' -o targetallocator . ; fi

FROM mcr.microsoft.com/cbl-mariner/base/core:2.0 as builder
LABEL description="Azure Monitor Prometheus metrics collector - target allocator"
LABEL maintainer="ciprometheus@microsoft.com"
ENV OS_TYPE "linux"
ENV tmpdir /opt
COPY ./scripts/livenessprobe-targetallocator.sh $tmpdir/microsoft/liveness/livenessprobe-targetallocator.sh
COPY ./scripts/setup-targetallocator.sh ./scripts/main-targetallocator.sh $tmpdir/
COPY ./LICENSE $tmpdir/microsoft
COPY ./NOTICE $tmpdir/microsoft
RUN tdnf clean all
RUN tdnf repolist --refresh
RUN tdnf update -y
RUN tdnf install -y wget sudo vim busybox curl
RUN mkdir /busybin && busybox --install /busybin
RUN chmod 775 $tmpdir/*.sh;
RUN sync;
RUN $tmpdir/setup-targetallocator.sh


######## Start a new stage from scratch #######
FROM mcr.microsoft.com/cbl-mariner/distroless/debug:2.0
ENV tmpdir /opt
ENV PATH="/busybin:${PATH}"

# files
COPY --from=builder /opt /opt
COPY --from=builder /busybin /busybin
COPY --from=builder /usr/bin/inotifywait /usr/bin/inotifywait
COPY --from=builder /usr/bin/bash /usr/bin/bash
COPY --from=builder /usr/sbin/busybox /usr/sbin/busybox
COPY --from=builder /usr/bin/vim /usr/bin/vim
# COPY --from=builder /usr/share/vim /usr/share/vim
COPY --from=builder /usr/bin/gzip /usr/bin/
# COPY --from=builder /usr/bin/curl /usr/bin/
COPY --from=builder /bin/sh /bin/sh

# bash dependencies
COPY --from=builder /lib/libreadline.so.8 /lib/
COPY --from=builder /usr/lib/libncursesw.so.6 /usr/lib/libtinfo.so.6 /usr/lib/
# inotifywait dependencies
COPY --from=builder /lib/libinotifytools.so.0 /lib/
# vim dependencies
COPY --from=builder /lib/libm.so.6 /lib/libtinfo.so.6 /lib/
# curl dependencies
# COPY --from=builder /lib/libcurl.so.4 /lib/libz.so.1 /lib/libc.so.6 /lib/libnghttp2.so.14 /lib/libssh2.so.1 /lib/libssl.so.1.1   /lib/libcrypto.so.1.1  /lib/libgssapi_krb5.so.2 /lib/libzstd.so.1 /lib/
# COPY --from=builder /usr/lib/libkrb5.so.3 /usr/lib/libk5crypto.so.3 /usr/lib/libcom_err.so.2 /usr/lib/libkrb5support.so.0 /usr/lib/libresolv.so.2 /usr/lib/
# sh dependencies
COPY --from=builder /lib/libreadline.so.8 /lib/libc.so.6 /usr/lib/libncursesw.so.6 /usr/lib/libtinfo.so.6 /lib/




#WORKDIR /root/

# Copy the pre-built binary file from the previous stage
COPY --from=targetallocator-builder /app/opentelemetry-operator/cmd/otel-allocator/targetallocator $tmpdir/

# ENTRYPOINT ["./main"]
ENTRYPOINT [ "/bin/bash" ]
CMD [ "/opt/main-targetallocator.sh" ]
