FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /gateway ./cmd/gateway-mock
RUN mkdir -p /gateway-state
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /gateway /gateway
# Named volumes copy this directory on first use, retaining the non-root
# ownership needed for durable idempotency state.
COPY --from=build --chown=65532:65532 /gateway-state /var/lib/gateway
USER nonroot:nonroot
ENTRYPOINT ["/gateway"]
