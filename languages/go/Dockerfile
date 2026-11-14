ARG JUDGE_BIN
FROM ${JUDGE_BIN} AS judge

FROM golang:1.26-alpine
ENV GOCACHE=/opt/lang/gocache GOMODCACHE=/opt/lang/gomod GOFLAGS=-mod=mod \
    GOTOOLCHAIN=local CGO_ENABLED=0 GOTELEMETRY=off
# Modules listed in packages.txt (module@version) are downloaded here, then
# the network is switched off for good with GOPROXY=off.
COPY packages.txt warm.go /tmp/
RUN set -e; mkdir -p /opt/lang/mod && cd /opt/lang/mod; \
    go mod init judge >/dev/null 2>&1; \
    grep -vE '^[[:space:]]*(#|$)' /tmp/packages.txt | while read -r p; do go get "$p"; done; \
    cp /tmp/warm.go main.go && go build -ldflags="-s -w" -o /tmp/warm . && rm -f main.go /tmp/warm /tmp/warm.go /tmp/packages.txt
ENV GOPROXY=off
COPY compile.sh /opt/lang/compile.sh
RUN chmod +x /opt/lang/compile.sh
# The build cache lives in the image, so the sandbox needs a writable overlay.
ENV JUDGE_SANDBOX_WRITE=1

COPY --from=judge /judge /jexec /opt/judge/
COPY langs/ /opt/judge/langs/
ENTRYPOINT ["/opt/judge/judge", "serve"]
