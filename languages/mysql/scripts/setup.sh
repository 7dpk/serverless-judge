#!/bin/sh
# Per test: a fresh database, then the caller's fixture SQL (the
# submission's stdin) as the unprivileged account.
S=/tmp/mysql.sock
db="t$JUDGE_TEST"
mysql -S "$S" -uroot -e "CREATE DATABASE \`$db\`" || exit 1
exec mysql -S "$S" -ujudge "$db" >/dev/null
