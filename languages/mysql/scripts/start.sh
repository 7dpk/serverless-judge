#!/bin/sh
# Make sure the server is running and answering.
#
# This runs when a sandbox is created (so the server is already up when a
# job arrives) and again at the start of each job. The second run must cope
# with whatever the first left behind: a healthy server, one still starting,
# or one that died while the sandbox sat idle.
# --user=root is required: every sandbox process is uid 0.
S=/tmp/mysql.sock
PIDFILE=/tmp/mysql.launch.pid
ready() { mysqladmin -S "$S" -uroot --silent ping >/dev/null 2>&1; }
alive() { [ -f "$PIDFILE" ] && kill -0 "$(cat "$PIDFILE")" 2>/dev/null; }
launch() {
  rm -f "$S" "$S.lock"
  mysqld --no-defaults --user=root --datadir=/opt/mysql-data --socket="$S" \
    --pid-file=/tmp/mysql.pid --log-error=/tmp/mysql.err \
    --skip-networking --mysqlx=OFF --performance-schema=OFF --skip-log-bin --skip-name-resolve \
    --innodb-buffer-pool-size=64M --innodb-redo-log-capacity=8M \
    --innodb-flush-log-at-trx-commit=0 --innodb-doublewrite=OFF --innodb-use-native-aio=OFF \
    --secure-file-priv=NULL --local-infile=OFF --max-connections=16 --table-open-cache=64 \
    >>/tmp/mysql.out 2>&1 </dev/null &
  echo $! > "$PIDFILE"
}
wait_ready() { # seconds
  end=$(( $(date +%s) + $1 ))
  until ready; do
    alive || return 1
    [ "$(date +%s)" -ge "$end" ] && return 1
    sleep 0.02
  done
}

ready && exit 0
if alive; then
  wait_ready 8 && exit 0
  kill -9 "$(cat "$PIDFILE")" 2>/dev/null
  sleep 0.2
fi
launch
wait_ready 18 && exit 0
echo "mysqld did not start:"; tail -n 25 /tmp/mysql.err 2>/dev/null; tail -n 5 /tmp/mysql.out 2>/dev/null
exit 1
