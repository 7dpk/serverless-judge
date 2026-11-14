#!/bin/sh
# Make sure the server is running and accepting connections.
#
# This runs when a sandbox is created (so the server is already up when a
# job arrives) and again at the start of each job. The second run must cope
# with whatever the first left behind: a healthy server, one still starting,
# or one that died while the sandbox sat idle.
PIDFILE=/tmp/pg.launch.pid
ready() { pg_isready -q -h /tmp -U postgres 2>/dev/null; }
alive() { [ -f "$PIDFILE" ] && kill -0 "$(cat "$PIDFILE")" 2>/dev/null; }
launch() {
  rm -f /opt/pgdata/postmaster.pid
  postgres -D /opt/pgdata -k /tmp >>/tmp/pg.log 2>&1 </dev/null &
  echo $! > "$PIDFILE"
}
wait_ready() { # seconds
  end=$(( $(date +%s) + $1 ))
  until ready; do
    alive || return 1
    [ "$(date +%s)" -ge "$end" ] && return 1
    sleep 0.01
  done
}

ready && exit 0
if alive; then
  wait_ready 5 && exit 0
  kill -9 "$(cat "$PIDFILE")" 2>/dev/null
  sleep 0.2
fi
launch
wait_ready 15 && exit 0
echo "postgres did not start:"; tail -n 20 /tmp/pg.log
exit 1
