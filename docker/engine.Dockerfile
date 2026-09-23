# syntax=docker/dockerfile:1.7
FROM rust:1.89-slim AS build
WORKDIR /src
COPY . .
RUN --mount=type=cache,id=cargo-registry,target=/usr/local/cargo/registry,sharing=locked \
    --mount=type=cache,id=cargo-target-engine,target=/src/target,sharing=locked \
    cargo build --release --locked -p engine --bin engine && cp target/release/engine /engine

# The engine holds no keys and makes no outbound calls; it listens on a Unix socket (or mTLS TCP).
FROM gcr.io/distroless/cc-debian12:nonroot
COPY --from=build /engine /engine
USER nonroot
ENTRYPOINT ["/engine"]
