ARG JUDGE_BIN
FROM ${JUDGE_BIN} AS judge

FROM dart:3.14-sdk AS dart

# Compiled languages that are asked for now and then: Dart, Fortran, Pascal,
# Objective-C, D, assembly, COBOL, and C and C++ as Clang builds them. They
# share an image and its instances.
FROM debian:trixie-slim
COPY apt-packages.txt /tmp/apt-packages.txt
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates gcc g++ libc6-dev clang gfortran gdc nasm \
      fp-compiler fp-units-rtl fp-units-fcl gobjc libgnustep-base-dev \
      $(grep -vE '^[[:space:]]*(#|$)' /tmp/apt-packages.txt) \
 && (apt-get install -y --no-install-recommends gnucobol || apt-get install -y --no-install-recommends gnucobol3 || apt-get install -y --no-install-recommends gnucobol4) \
 && rm -rf /var/lib/apt/lists/* /tmp/apt-packages.txt /usr/share/doc/* /usr/share/man/*
COPY --from=dart /usr/lib/dart /usr/lib/dart
ENV PATH=/usr/lib/dart/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
COPY objc.sh selftest.sh /opt/lang/
RUN chmod +x /opt/lang/objc.sh && sh /opt/lang/selftest.sh

COPY --from=judge /judge /jexec /opt/judge/
COPY langs/ /opt/judge/langs/
ENTRYPOINT ["/opt/judge/judge", "serve"]
