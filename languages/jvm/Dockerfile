ARG JUDGE_BIN
FROM ${JUDGE_BIN} AS judge

# The languages that compile to JVM bytecode, apart from Java, which has its
# own image tuned for it. The base is the same as Java's, so the registry
# stores it once.
FROM eclipse-temurin:25-jdk
ARG KOTLIN=2.4.21
ARG SCALA=3.3.9
ARG GROOVY=6.0.0
ARG CLOJURE=1.12.4
ARG CLOJURE_SPEC=0.5.238
ARG CLOJURE_CORE_SPECS=0.4.74
RUN set -e; apt-get update; apt-get install -y --no-install-recommends curl ca-certificates; rm -rf /var/lib/apt/lists/*; \
    mkdir -p /opt/lang/clojure /tmp/dl; cd /tmp/dl; \
    curl -fsSL -o kotlin.zip "https://github.com/JetBrains/kotlin/releases/download/v$KOTLIN/kotlin-compiler-$KOTLIN.zip"; \
    curl -fsSL -o scala.tgz "https://github.com/scala/scala3/releases/download/$SCALA/scala3-$SCALA.tar.gz"; \
    curl -fsSL -o groovy.zip "https://archive.apache.org/dist/groovy/$GROOVY/distribution/apache-groovy-binary-$GROOVY.zip"; \
    (cd /opt/lang && jar xf /tmp/dl/kotlin.zip && jar xf /tmp/dl/groovy.zip && tar -xzf /tmp/dl/scala.tgz); \
    mv "/opt/lang/scala3-$SCALA" /opt/lang/scala; mv "/opt/lang/groovy-$GROOVY" /opt/lang/groovy; \
    chmod +x /opt/lang/kotlinc/bin/* /opt/lang/groovy/bin/* /opt/lang/scala/bin/*; \
    M=https://repo1.maven.org/maven2/org/clojure; \
    curl -fsSL -o /opt/lang/clojure/clojure.jar "$M/clojure/$CLOJURE/clojure-$CLOJURE.jar"; \
    curl -fsSL -o /opt/lang/clojure/spec.alpha.jar "$M/spec.alpha/$CLOJURE_SPEC/spec.alpha-$CLOJURE_SPEC.jar"; \
    curl -fsSL -o /opt/lang/clojure/core.specs.alpha.jar "$M/core.specs.alpha/$CLOJURE_CORE_SPECS/core.specs.alpha-$CLOJURE_CORE_SPECS.jar"; \
    rm -rf /tmp/dl
ENV PATH=/opt/lang/kotlinc/bin:/opt/lang/scala/bin:/opt/lang/groovy/bin:$PATH
COPY aot.sh selftest.sh /opt/lang/
RUN sh /opt/lang/aot.sh && sh /opt/lang/selftest.sh

COPY --from=judge /judge /jexec /opt/judge/
COPY langs/ /opt/judge/langs/
ENTRYPOINT ["/opt/judge/judge", "serve"]
