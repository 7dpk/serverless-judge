ARG JUDGE_BIN
ARG PREBUILT
FROM ${JUDGE_BIN} AS judge
FROM ${PREBUILT} AS pg

FROM debian:trixie-slim
COPY --from=pg /opt/pg /opt/pg
# Owned by root: that is who the server runs as inside the sandbox.
COPY --from=pg --chown=0:0 /opt/pgdata /opt/pgdata
COPY scripts/ /opt/lang/
RUN chmod 700 /opt/pgdata && chmod +x /opt/lang/*.sh
# The data directory is modified in place, in the sandbox's memory overlay.
ENV JUDGE_SANDBOX_WRITE=1 PATH=/opt/pg/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

COPY --from=judge /judge /jexec /opt/judge/
COPY langs/ /opt/judge/langs/
ENTRYPOINT ["/opt/judge/judge", "serve"]
