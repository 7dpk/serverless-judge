#!/bin/sh
# Run the submission. Output is tab-separated with a header row and NULL
# spelled out, the same shape the MySQL runner produces.
# The server-side timeout is a safety net just past the judge's own limit.
export PGOPTIONS="-c statement_timeout=$((JUDGE_CPU_MS + 500))"
exec psql -X -q -h /tmp -U judge -d "t$JUDGE_TEST" -v ON_ERROR_STOP=1 \
  -P pager=off -P footer=off -P null=NULL -A -F "$(printf '\t')" -f main.sql
