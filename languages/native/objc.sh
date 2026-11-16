#!/bin/sh
# Compile main.m with Clang against GNUstep's Foundation and the GNU
# Objective-C runtime. Extra arguments are the caller's compiler options.
inc=""
for d in /usr/include/GNUstep /usr/include/*/GNUstep /usr/lib/gcc/*/*/include; do
  [ -d "$d" ] && inc="$inc -I$d"
done
exec clang -fobjc-runtime=gcc -fconstant-string-class=NSConstantString -fexceptions -fobjc-exceptions \
  -D_NATIVE_OBJC_EXCEPTIONS -DGNUSTEP -DGNUSTEP_BASE_LIBRARY=1 -DGNU_RUNTIME=1 \
  $inc -w -O2 -o main "$@" main.m -lgnustep-base -lobjc -lm
