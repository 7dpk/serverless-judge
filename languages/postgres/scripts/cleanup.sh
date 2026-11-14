#!/bin/sh
# --force also ends any query the submission left running.
exec dropdb --force --if-exists -h /tmp -U postgres "t$JUDGE_TEST"
