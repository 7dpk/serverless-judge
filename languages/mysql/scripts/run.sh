#!/bin/sh
# Run the submission. --batch prints tab-separated rows with a header and
# NULL spelled out, the same shape the PostgreSQL runner produces.
exec mysql -S /tmp/mysql.sock -ujudge --batch \
  --init-command="SET SESSION max_execution_time=$((JUDGE_CPU_MS + 500))" \
  "t$JUDGE_TEST" < main.sql
