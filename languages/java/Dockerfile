ARG JUDGE_BIN
FROM ${JUDGE_BIN} AS judge

FROM eclipse-temurin:25-jdk
# Jars listed in packages.txt (group:artifact:version) go on the classpath.
COPY packages.txt /tmp/packages.txt
RUN set -e; mkdir -p /opt/lang/lib; \
    grep -vE '^[[:space:]]*(#|$)' /tmp/packages.txt | while IFS=: read -r g a v; do \
      curl -fsSL -o "/opt/lang/lib/$a-$v.jar" \
        "https://repo1.maven.org/maven2/$(echo "$g" | tr . /)/$a/$v/$a-$v.jar"; \
    done; rm /tmp/packages.txt
# Ahead-of-time cache for the compiler (JDK 25). javac is itself a Java
# program, and most of a small compile is the JVM loading and linking javac.
# Recording that once here cuts a typical compile from about 1 s to 0.35 s.
# The flags must match the ones java.json compiles with.
COPY train/ /tmp/train/
RUN set -e; cd /tmp/train; F="-J-XX:+UseSerialGC -J-XX:TieredStopAtLevel=1"; \
    javac $F -J-XX:AOTMode=record -J-XX:AOTConfiguration=/tmp/javac.aotconf -encoding UTF-8 -cp "/opt/lang/lib/*" -d /tmp/out Main.java; \
    javac $F -J-XX:AOTMode=create -J-XX:AOTConfiguration=/tmp/javac.aotconf -J-XX:AOTCache=/opt/lang/javac.aot -encoding UTF-8 -cp "/opt/lang/lib/*" -d /tmp/out Main.java; \
    test -s /opt/lang/javac.aot; \
    javac $F -J-XX:AOTCache=/opt/lang/javac.aot -J-XX:AOTMode=on -encoding UTF-8 -cp "/opt/lang/lib/*" -d /tmp/out Main.java; \
    echo "5 3" | java -XX:+UseSerialGC -cp /tmp/out Main >/dev/null; \
    rm -rf /tmp/train /tmp/out /tmp/javac.aotconf

COPY --from=judge /judge /jexec /opt/judge/
COPY langs/ /opt/judge/langs/
ENTRYPOINT ["/opt/judge/judge", "serve"]
