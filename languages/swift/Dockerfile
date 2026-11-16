ARG JUDGE_BIN
FROM ${JUDGE_BIN} AS judge

# The Swift toolchain image is 1.3 GB compressed. A judge needs the compiler
# and the standard libraries. It does not need the debugger, the package
# manager, the language server, the documentation compiler or the static
# copies of the libraries, so those are removed and what is left is copied
# into a fresh image as a single layer.
FROM swift:6.4-noble AS toolchain
RUN set -e; du -sh /usr 2>/dev/null; \
    rm -rf /usr/lib/swift_static /usr/lib/liblldb* /usr/lib/lldb /usr/bin/lldb* /usr/bin/repl_swift \
           /usr/bin/sourcekit-lsp /usr/lib/libsourcekitdInProc.so /usr/bin/docc /usr/share/docc \
           /usr/bin/swift-build* /usr/bin/swift-package* /usr/bin/swift-test /usr/bin/swift-run \
           /usr/bin/swift-sdk /usr/bin/swift-experimental-sdk /usr/bin/swift-format /usr/libexec/swift/pm /usr/lib/swift/pm \
           /var/lib/apt/lists/* /var/cache/apt/archives/*; \
    du -sh /usr 2>/dev/null; \
    cd /tmp; printf 'import Foundation\nlet n = Int(readLine()!)!\nprint(n * 2)\n' > main.swift; \
    swiftc -O -o main main.swift; test "$(echo 21 | ./main)" = 42; rm -rf /tmp/* /root/.cache; swiftc --version

FROM scratch
COPY --from=toolchain / /
ENV PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

COPY --from=judge /judge /jexec /opt/judge/
COPY langs/ /opt/judge/langs/
ENTRYPOINT ["/opt/judge/judge", "serve"]
