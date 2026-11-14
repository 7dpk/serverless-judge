ARG JUDGE_BIN
FROM ${JUDGE_BIN} AS judge

FROM mysql:8.4
# Create the data directory once so no --initialize runs per request, and add
# the unprivileged account submissions run as. It may only touch databases
# named t<number>, which the runner creates per test.
RUN set -e; \
    mysqld --no-defaults --initialize-insecure --user=mysql --datadir=/opt/mysql-data \
           --innodb-redo-log-capacity=8M >/dev/null 2>&1; \
    mysqld --no-defaults --user=mysql --datadir=/opt/mysql-data --socket=/tmp/b.sock \
           --pid-file=/tmp/b.pid --skip-networking --mysqlx=OFF --innodb-redo-log-capacity=8M \
           --log-error=/tmp/b.err & \
    i=0; until mysqladmin -S /tmp/b.sock -uroot --silent ping >/dev/null 2>&1; do \
      i=$((i+1)); if [ "$i" -gt 600 ]; then cat /tmp/b.err; exit 1; fi; sleep 0.1; done; \
    mysql -S /tmp/b.sock -uroot -e "CREATE USER 'judge'@'localhost'; GRANT ALL PRIVILEGES ON \`t%\`.* TO 'judge'@'localhost'; FLUSH PRIVILEGES;"; \
    mysqladmin -S /tmp/b.sock -uroot shutdown; \
    i=0; while [ -e /tmp/b.pid ]; do i=$((i+1)); [ "$i" -gt 300 ] && exit 1; sleep 0.1; done; \
    chown -R root:root /opt/mysql-data; rm -f /tmp/b.*
COPY scripts/ /opt/lang/
RUN chmod +x /opt/lang/*.sh
# The data directory is modified in place, in the sandbox's memory overlay.
ENV JUDGE_SANDBOX_WRITE=1

COPY --from=judge /judge /jexec /opt/judge/
COPY langs/ /opt/judge/langs/
ENTRYPOINT ["/opt/judge/judge", "serve"]
