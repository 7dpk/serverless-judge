ARG JUDGE_BIN
FROM ${JUDGE_BIN} AS judge

# Haskell, OCaml, Elixir and Erlang, as packaged by Debian. They share an
# image and its instances because none is asked for often.
FROM debian:trixie-slim
COPY apt-packages.txt /tmp/apt-packages.txt
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates ghc ocaml elixir erlang-base \
      $(grep -vE '^[[:space:]]*(#|$)' /tmp/apt-packages.txt) \
 && rm -rf /var/lib/apt/lists/* /tmp/apt-packages.txt /usr/share/doc/* /usr/share/man/*
COPY selftest.sh /opt/lang/selftest.sh
RUN sh /opt/lang/selftest.sh

COPY --from=judge /judge /jexec /opt/judge/
COPY langs/ /opt/judge/langs/
ENTRYPOINT ["/opt/judge/judge", "serve"]
