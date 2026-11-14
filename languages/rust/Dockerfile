ARG JUDGE_BIN
FROM ${JUDGE_BIN} AS judge

FROM rust:1-slim
# Crates in Cargo.toml are built once here and handed to rustc as --extern,
# so submissions stay single-file and compile without cargo or a network.
COPY Cargo.toml /opt/lang/Cargo.toml
RUN set -e; cd /opt/lang; mkdir -p src; : > src/lib.rs; : > externs.rsp; \
    deps=$(sed -n '/^\[dependencies\]/,/^\[/p' Cargo.toml | grep -E '^[A-Za-z0-9_-]+[[:space:]]*=' | sed -E 's/[[:space:]]*=.*//' || true); \
    if [ -n "$deps" ]; then \
      cargo build --release; \
      echo "-L" >> externs.rsp; echo "dependency=/opt/lang/target/release/deps" >> externs.rsp; \
      for d in $deps; do \
        c=$(echo "$d" | tr - _); \
        f=$(ls target/release/deps/lib"$c"-*.rlib target/release/deps/lib"$c"-*.so 2>/dev/null | head -1); \
        test -n "$f"; \
        echo "--extern" >> externs.rsp; echo "$c=/opt/lang/$f" >> externs.rsp; \
      done; \
      rm -rf target/release/build target/release/.fingerprint target/release/incremental; \
    fi; \
    rm -rf /usr/local/cargo/registry

COPY --from=judge /judge /jexec /opt/judge/
COPY langs/ /opt/judge/langs/
ENTRYPOINT ["/opt/judge/judge", "serve"]
