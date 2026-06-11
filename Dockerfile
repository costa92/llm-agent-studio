# Stage 1: build
FROM golang:1.26 AS builder

WORKDIR /src

# Copy module files first for layer-cache efficiency.
COPY go.mod go.sum ./
RUN GOWORK=off go mod download

# Copy source.
COPY . .

# Build the studiod binary. GOWORK=off: standalone module, no umbrella go.work.
RUN GOWORK=off CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /studiod ./cmd/studiod

# Stage 2: minimal runtime image
FROM gcr.io/distroless/static:nonroot

COPY --from=builder /studiod /studiod

EXPOSE 8083

ENTRYPOINT ["/studiod"]
