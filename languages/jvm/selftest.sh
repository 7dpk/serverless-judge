#!/bin/sh
# Run while the image is built: every language in it must compile and run a
# program that reads a number and prints twice that. The build fails if one
# cannot, and says which. The commands are the ones in langs/*.json.
export JAVA_OPTS="-XX:+UseSerialGC -XX:TieredStopAtLevel=1"
mkdir -p /tmp/t && cd /tmp/t || exit 1
fail=0
# Programs are judged with a 1 MB limit on any file they write (ulimit -f
# counts 512-byte blocks), so run them under it here too.
check() {
  name=$1; shift
  got=$( (ulimit -f 2048; echo 21 | "$@") 2>/tmp/stderr)
  if [ "$got" = 42 ]; then echo "ok    $name"; else echo "FAIL  $name: $got $(cat /tmp/stderr)"; fail=1; fi
  rm -rf /tmp/t/*
}

cat > Main.kt <<'EOF'
fun main() { println(readLine()!!.toInt() * 2) }
EOF
kotlinc -nowarn -d . Main.kt
check kotlin java -cp .:/opt/lang/kotlinc/lib/kotlin-stdlib.jar MainKt

cat > Main.scala <<'EOF'
object Main { def main(args: Array[String]): Unit = println(scala.io.StdIn.readInt() * 2) }
EOF
scalac -d . Main.scala
check scala java -cp ".:/opt/lang/scala/lib/*" Main

cat > main.groovy <<'EOF'
println(System.in.newReader().readLine().toInteger() * 2)
EOF
groovyc -d . main.groovy
check groovy java @/opt/lang/groovy/run.args main

cat > main.clj <<'EOF'
(println (* 2 (Integer/parseInt (read-line))))
EOF
check clojure java @/opt/lang/clojure/run.args clojure.main main.clj

cd / && rm -rf /tmp/t
kotlinc -version 2>&1 | tail -1; scalac -version 2>&1 | tail -1; groovyc -version 2>&1 | tail -1
exit $fail
