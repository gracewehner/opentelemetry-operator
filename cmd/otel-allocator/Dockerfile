# Build the otel-allocator binary
FROM mcr.microsoft.com/oss/go/microsoft/golang:1.19 as builder

WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./

RUN go mod download

COPY . .

# Build the Go app
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o main .

######## Start a new stage from scratch #######
FROM mcr.microsoft.com/cbl-mariner/distroless/debug:2.0

WORKDIR /root/

# Copy the pre-built binary file from the previous stage
COPY --from=builder /app/main .

ENTRYPOINT ["./main"]
