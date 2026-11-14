ARG JUDGE_BIN
FROM ${JUDGE_BIN} AS judge

FROM debian:trixie-slim
COPY apt-packages.txt /tmp/apt-packages.txt
RUN apt-get update \
 && apt-get install -y --no-install-recommends gcc g++ libc6-dev \
      $(grep -vE '^[[:space:]]*(#|$)' /tmp/apt-packages.txt) \
 && rm -rf /var/lib/apt/lists/* /tmp/apt-packages.txt

# Precompiled <bits/stdc++.h>, built with exactly the flags cpp.json uses.
# The build fails if the compiler would not actually pick it up.
RUN set -e; hdr=$(find /usr/include -path '*/bits/stdc++.h' | head -1); test -n "$hdr"; \
    mkdir -p /opt/pch/bits; \
    g++ -O2 -std=gnu++20 -x c++-header "$hdr" -o /opt/pch/bits/stdc++.h.gch; \
    printf '#include <bits/stdc++.h>\nint main(){}\n' > /tmp/t.cpp; \
    g++ -O2 -std=gnu++20 -I/opt/pch -H -o /tmp/t /tmp/t.cpp 2>&1 | grep -q '^! /opt/pch'; \
    rm -f /tmp/t /tmp/t.cpp

COPY --from=judge /judge /jexec /opt/judge/
COPY langs/ /opt/judge/langs/
ENTRYPOINT ["/opt/judge/judge", "serve"]
