#!/bin/sh
# Run while the image is built. Clojure and Groovy spend most of a small
# program's time starting up: loading a few thousand classes and compiling
# them. This records an ahead-of-time cache of those classes for each (JDK
# 25) and writes the java arguments that use it, which clojure.json and
# groovy.json refer to as @/opt/lang/<language>/run.args.
#
# Measured in a sandbox for a program that adds two numbers: Clojure 2.0 s of
# CPU without any of this, 0.8 s with it; Groovy 2.1 s and about 1 s. The
# limit is 2 s.
set -e
FLAGS="-XX:+UseSerialGC -XX:TieredStopAtLevel=1 -Xss64m"
mkdir -p /tmp/aot && cd /tmp/aot

CP=/opt/lang/clojure/clojure.jar:/opt/lang/clojure/spec.alpha.jar:/opt/lang/clojure/core.specs.alpha.jar
echo "(require '[clojure.string :as str]) (println (* 2 (Long/parseLong (str/trim (read-line)))))" > main.clj
echo 21 | java $FLAGS -XX:AOTCacheOutput=/opt/lang/clojure/clojure.aot -cp "$CP" clojure.main main.clj
test -s /opt/lang/clojure/clojure.aot
echo "$FLAGS -XX:AOTCache=/opt/lang/clojure/clojure.aot -cp $CP" > /opt/lang/clojure/run.args

# A cache is only valid for the class path it was recorded with, in the same
# order, so the jars are listed out rather than left to a wildcard. The
# program's own directory comes last: entries may be added after the
# recorded ones, not before.
JARS=$(ls /opt/lang/groovy/lib/*.jar | sort | tr '\n' ':'); JARS=${JARS%:}
echo 21 | java $FLAGS -XX:AOTCacheOutput=/opt/lang/groovy/groovy.aot -cp "$JARS" groovy.ui.GroovyMain \
  -e 'println(System.in.newReader().readLine().toInteger() * 2)'
test -s /opt/lang/groovy/groovy.aot
echo "$FLAGS -XX:AOTCache=/opt/lang/groovy/groovy.aot -cp $JARS:." > /opt/lang/groovy/run.args

cd / && rm -rf /tmp/aot
ls -la /opt/lang/clojure/clojure.aot /opt/lang/groovy/groovy.aot
