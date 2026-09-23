# syntax=docker/dockerfile:1.7
FROM golang:1.24-alpine AS build
WORKDIR /src/services/gateway
COPY services/gateway/go.mod ./
RUN go mod download
COPY services/gateway ./
COPY contracts /src/contracts
ARG BIN=gateway
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/app ./cmd/${BIN}

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/app /app
USER nonroot
ENTRYPOINT ["/app"]
