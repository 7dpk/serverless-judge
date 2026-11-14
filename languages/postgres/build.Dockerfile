# Slow part of the PostgreSQL runner, built once and reused: the server
# compiled from source plus a ready-made data directory.
#
# One change to upstream: the server's refusal to run as root is removed.
# Inside a Cloud Run sandbox every process is uid 0 and cannot switch user,
# so the stock binary would not start. The sandbox, not the unix user, is
# the isolation boundary here.
FROM debian:trixie-slim
ARG PG_VERSION=18.7
RUN apt-get update && apt-get install -y --no-install-recommends \
      build-essential bison flex perl ca-certificates curl bzip2 zlib1g-dev pkg-config \
 && rm -rf /var/lib/apt/lists/*
RUN set -e; curl -fsSL "https://ftp.postgresql.org/pub/source/v${PG_VERSION}/postgresql-${PG_VERSION}.tar.bz2" | tar xj -C /tmp; \
    cd /tmp/postgresql-${PG_VERSION}; \
    grep -q 'if (geteuid() == 0)' src/backend/main/main.c; \
    sed -i 's/if (geteuid() == 0)/if (0)/' src/backend/main/main.c; \
    ./configure --prefix=/opt/pg --without-icu --without-readline CFLAGS="-O2" >/dev/null; \
    make -s -j"$(nproc)" >/dev/null; make -s install >/dev/null; \
    strip /opt/pg/bin/* 2>/dev/null || true; \
    rm -rf /opt/pg/include /opt/pg/lib/*.a /opt/pg/share/doc /tmp/postgresql-${PG_VERSION}

# Create the cluster as an ordinary user so no initdb runs per request.
COPY postgresql.conf /tmp/judge.conf
RUN set -e; useradd -m pg; mkdir -p /opt/pgdata; chown pg /opt/pgdata
USER pg
RUN set -e; export PATH=/opt/pg/bin:$PATH; \
    initdb -D /opt/pgdata -U postgres --auth=trust --no-sync --encoding=UTF8 --locale=C.UTF-8 --wal-segsize=1 >/dev/null; \
    cat /tmp/judge.conf >> /opt/pgdata/postgresql.conf; \
    pg_ctl -D /opt/pgdata -w -o "-k /tmp" start >/dev/null; \
    psql -h /tmp -U postgres -d postgres -c "CREATE ROLE judge LOGIN" >/dev/null; \
    pg_ctl -D /opt/pgdata -w -m fast stop >/dev/null
