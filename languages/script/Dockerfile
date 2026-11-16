ARG JUDGE_BIN
FROM ${JUDGE_BIN} AS judge

# The interpreted languages that are one Debian package each. None of them
# is asked for often enough to earn a service of its own, so they share this
# one and its instances.
FROM debian:trixie-slim
COPY apt-packages.txt /tmp/apt-packages.txt
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates ruby perl lua5.4 r-base-core sqlite3 \
      $(grep -vE '^[[:space:]]*(#|$)' /tmp/apt-packages.txt) \
 && ln -sf /usr/bin/lua5.4 /usr/local/bin/lua \
 && rm -rf /var/lib/apt/lists/* /tmp/apt-packages.txt

# The build fails here if any of them cannot run a program.
RUN set -e; cd /tmp; \
    test "$(echo 21 | ruby -e 'puts gets.to_i * 2')" = 42; \
    test "$(echo 21 | perl -e 'print <STDIN> * 2')" = 42; \
    test "$(echo 21 | lua -e 'print(io.read("n") * 2)')" = 42; \
    test "$(echo 21 | bash -c 'read n; echo $((n * 2))')" = 42; \
    test "$(echo 21 | Rscript --vanilla -e 'cat(as.integer(readLines(file("stdin"), 1)) * 2)')" = 42; \
    test "$(sqlite3 -batch :memory: 'select 21 * 2')" = 42; \
    ruby -v; perl -e 'print "perl $^V\n"'; lua -v; bash --version | head -1; R --version | head -1; sqlite3 --version

COPY --from=judge /judge /jexec /opt/judge/
COPY langs/ /opt/judge/langs/
ENTRYPOINT ["/opt/judge/judge", "serve"]
