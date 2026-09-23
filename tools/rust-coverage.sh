#!/usr/bin/env bash
# Fails unless every executable line (excluding main.rs wiring) is hit. Uses rustup's llvm-tools-preview when present, else LLVM_BIN (must match rustc's LLVM major).
set -euo pipefail
cd "$(dirname "$0")/.."
SYSROOT_BIN="$(rustc --print sysroot)/lib/rustlib/$(rustc -vV | sed -n 's/^host: //p')/bin"
if [ -n "${LLVM_BIN:-}" ]; then L="$LLVM_BIN"; elif [ -x "$SYSROOT_BIN/llvm-cov" ]; then L="$SYSROOT_BIN"; else L=/usr/lib/llvm-20/bin; fi
T="$(realpath -m "${CARGO_TARGET_DIR:-target}")/cov"
rm -rf "$T/prof"; mkdir -p "$T/prof"
RUSTFLAGS="-C instrument-coverage" LLVM_PROFILE_FILE="$T/prof/%p-%m.profraw" cargo test --workspace --locked --target-dir "$T/build"
"$L/llvm-profdata" merge -sparse "$T"/prof/*.profraw -o "$T/m.profdata"
objs=$(find "$T/build/debug/deps" -maxdepth 1 -type f -executable ! -name '*.so' ! -name '*.d' | sed 's/^/-object /' | tr '\n' ' ')
# shellcheck disable=SC2086
"$L/llvm-cov" export $objs -instr-profile="$T/m.profdata" --ignore-filename-regex='(\.cargo|rustc|main\.rs|/out/|/tests/)' -format=lcov > "$T/lcov.info"
missed=$(awk -F'[:,]' '/^SF:/{f=$2} /^DA:/{if($3==0)print f":"$2}' "$T/lcov.info")
if [ -n "$missed" ]; then echo "Rust uncovered lines:"; echo "$missed"; exit 1; fi
echo "Rust coverage: 100% of lines"
