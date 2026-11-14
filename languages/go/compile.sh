#!/bin/sh
# Build main.go as module "judge" so imports of preinstalled modules resolve.
cp /opt/lang/mod/go.mod go.mod
[ -f /opt/lang/mod/go.sum ] && cp /opt/lang/mod/go.sum go.sum
exec go build -ldflags="-s -w" -o main "$@" .
