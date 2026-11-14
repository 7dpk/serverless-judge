#!/bin/sh
# End anything the submission left running, then drop its database.
S=/tmp/mysql.sock
mysql -S "$S" -uroot -N -B -e "SELECT CONCAT('KILL ', id, ';') FROM information_schema.processlist WHERE user = 'judge'" \
  | mysql -S "$S" -uroot 2>/dev/null
exec mysql -S "$S" -uroot -e "DROP DATABASE IF EXISTS \`t$JUDGE_TEST\`"
