#!/bin/sh
# Type-check and compile main.ts with the native TypeScript 7 compiler.
# node_modules is linked in so imports of installed packages resolve.
ln -sfn /opt/lang/node_modules node_modules
exec /opt/lang/node_modules/.bin/tsc --pretty false --target es2023 --module commonjs \
  --strict false --types node --skipLibCheck --outDir . "$@" main.ts
