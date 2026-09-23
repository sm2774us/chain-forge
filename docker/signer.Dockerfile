# syntax=docker/dockerfile:1.7
FROM rust:1.98-slim AS build
WORKDIR /src
COPY . .
RUN --mount=type=cache,id=cargo-registry,target=/usr/local/cargo/registry,sharing=locked \
    --mount=type=cache,id=cargo-target-signer,target=/src/target,sharing=locked \
    cargo build --release --locked -p signer --bin signer && cp target/release/signer /signer

FROM gcr.io/distroless/cc-debian12:nonroot
COPY --from=build /signer /signer
USER nonroot
EXPOSE 8081
ENTRYPOINT ["/signer"]
