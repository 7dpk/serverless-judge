#!/bin/sh
# Run while the image is built: every language in it must compile and run a
# program that reads a number and prints twice that. The build fails if one
# cannot, and says which. The commands are the ones in langs/*.json.
export HOME=/tmp/home LANG=C.UTF-8
# What erlang.json and elixir.json set. The VM's JIT otherwise maps a file
# larger than a judged program may write, and its idle scheduler threads
# otherwise spin, which is charged to the program as CPU time.
export ERL_AFLAGS="+JMsingle true +sbwt none +sbwtdcpu none +sbwtdio none"
mkdir -p /tmp/home /tmp/t && cd /tmp/t || exit 1
fail=0
# Programs are judged with a 1 MB limit on any file they write (ulimit -f
# counts 512-byte blocks), so run them under it here too.
check() {
  name=$1; shift
  got=$( (ulimit -f 2048; echo 21 | "$@") 2>/tmp/stderr)
  if [ "$got" = 42 ]; then echo "ok    $name"; else echo "FAIL  $name: $got $(cat /tmp/stderr)"; fail=1; fi
  rm -rf /tmp/t/*
}

cat > main.hs <<'EOF'
main :: IO ()
main = interact $ \s -> show (2 * read s :: Int) ++ "\n"
EOF
ghc -O2 -o main main.hs >build.log 2>&1 || cat build.log
check haskell ./main

cat > main.ml <<'EOF'
let () = Scanf.scanf " %d" (fun n -> Printf.printf "%d\n" (n * 2))
EOF
ocamlopt -I +str -I +unix -o main str.cmxa unix.cmxa main.ml
check ocaml ./main

cat > main.exs <<'EOF'
n = IO.read(:stdio, :line) |> String.trim() |> String.to_integer()
IO.puts(n * 2)
EOF
check elixir elixir main.exs

cat > main.erl <<'EOF'
main(_) ->
    {ok, [N]} = io:fread("", "~d"),
    io:format("~p~n", [N * 2]).
EOF
{ echo; cat main.erl; } > run.erl
check erlang escript run.erl

cd / && rm -rf /tmp/t /tmp/home
ghc --version; echo "ocaml $(ocamlopt -version)"; elixir --version | tail -1
exit $fail
