#!/bin/sh
# Per test: a fresh database owned by the unprivileged role, then the
# caller's fixture SQL (the submission's stdin).
db="t$JUDGE_TEST"
createdb -h /tmp -U postgres -O judge "$db" || exit 1
exec psql -X -q -h /tmp -U judge -d "$db" -v ON_ERROR_STOP=1 -f - >/dev/null
