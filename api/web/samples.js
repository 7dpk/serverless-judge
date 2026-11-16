// One small program per language. Each reads two integers and prints their
// sum, so every sample shares the same input and expected output. The SQL
// samples take their table from the input instead.
"use strict";

const SUM_INPUT = "20 22\n";
const SUM_EXPECTED = "42\n";

const SQL_FIXTURE =
  "CREATE TABLE emp (id INT PRIMARY KEY, name VARCHAR(20), dept VARCHAR(20), salary INT);\n" +
  "INSERT INTO emp VALUES (1,'ann','eng',120),(2,'bob','eng',100),(3,'cy','ops',90),(4,'di','ops',NULL);\n";
const SQL_QUERY = "SELECT dept, COUNT(*) AS n, SUM(salary) AS total\nFROM emp\nGROUP BY dept\nORDER BY dept;\n";
const SQL_EXPECTED = "dept\tn\ttotal\neng\t2\t220\nops\t2\t90\n";

const SAMPLES = {
  c: {
    code: '#include <stdio.h>\n\nint main(void) {\n    long a, b;\n    scanf("%ld %ld", &a, &b);\n    printf("%ld\\n", a + b);\n    return 0;\n}\n',
  },
  cpp: {
    code: '#include <bits/stdc++.h>\nusing namespace std;\n\nint main() {\n    long long a, b;\n    cin >> a >> b;\n    cout << a + b << "\\n";\n}\n',
  },
  python: {
    code: "a, b = map(int, input().split())\nprint(a + b)\n",
  },
  javascript: {
    code: 'const [a, b] = require("fs").readFileSync(0, "utf8").trim().split(/\\s+/).map(Number);\nconsole.log(a + b);\n',
  },
  typescript: {
    code: 'import * as fs from "fs";\n\nconst nums: number[] = fs.readFileSync(0, "utf8").trim().split(/\\s+/).map(Number);\nconst total: number = nums.reduce((x, y) => x + y, 0);\nconsole.log(total);\n',
  },
  java: {
    code: "import java.util.Scanner;\n\npublic class Main {\n    public static void main(String[] args) {\n        Scanner in = new Scanner(System.in);\n        long a = in.nextLong(), b = in.nextLong();\n        System.out.println(a + b);\n    }\n}\n",
  },
  csharp: {
    code: "using System;\nusing System.Linq;\n\nclass Program {\n    static void Main() {\n        var n = Console.ReadLine().Split(' ').Select(long.Parse).ToArray();\n        Console.WriteLine(n[0] + n[1]);\n    }\n}\n",
  },
  go: {
    code: 'package main\n\nimport "fmt"\n\nfunc main() {\n\tvar a, b int64\n\tfmt.Scan(&a, &b)\n\tfmt.Println(a + b)\n}\n',
  },
  php: {
    code: '<?php\nfscanf(STDIN, "%d %d", $a, $b);\necho $a + $b, "\\n";\n',
  },
  rust: {
    code: 'use std::io::Read;\n\nfn main() {\n    let mut s = String::new();\n    std::io::stdin().read_to_string(&mut s).unwrap();\n    let v: Vec<i64> = s.split_whitespace().map(|x| x.parse().unwrap()).collect();\n    println!("{}", v[0] + v[1]);\n}\n',
  },
  postgresql: { code: SQL_QUERY, stdin: SQL_FIXTURE, expected: SQL_EXPECTED, sql: true },
  mysql: { code: SQL_QUERY, stdin: SQL_FIXTURE, expected: SQL_EXPECTED, sql: true },
  // The languages below share a service with others: script, jvm, swift,
  // functional and native.
  ruby: {
    code: "a, b = gets.split.map(&:to_i)\nputs a + b\n",
  },
  perl: {
    code: "my ($a, $b) = split \" \", <STDIN>;\nprint $a + $b, \"\\n\";\n",
  },
  lua: {
    code: "local a, b = io.read(\"n\", \"n\")\nprint(a + b)\n",
  },
  bash: {
    code: "read a b\necho $((a + b))\n",
  },
  r: {
    code: "x <- scan(file(\"stdin\"), quiet = TRUE)\ncat(sum(x), \"\\n\", sep = \"\")\n",
  },
  kotlin: {
    code: "fun main() {\n    val (a, b) = readLine()!!.split(\" \").map { it.toLong() }\n    println(a + b)\n}\n",
  },
  scala: {
    code: "object Main {\n  def main(args: Array[String]): Unit = {\n    val Array(a, b) = scala.io.StdIn.readLine().split(\" \").map(_.toLong)\n    println(a + b)\n  }\n}\n",
  },
  groovy: {
    code: "def (a, b) = System.in.newReader().readLine().split(\" \")*.toLong()\nprintln(a + b)\n",
  },
  clojure: {
    code: "(require '[clojure.string :as str])\n\n(let [[a b] (map #(Long/parseLong %) (str/split (read-line) #\" \"))]\n  (println (+ a b)))\n",
  },
  swift: {
    code: "let p = readLine()!.split(separator: \" \").map { Int($0)! }\nprint(p[0] + p[1])\n",
  },
  haskell: {
    code: "main :: IO ()\nmain = do\n  [a, b] <- map read . words <$> getLine :: IO [Integer]\n  print (a + b)\n",
  },
  ocaml: {
    code: "let () = Scanf.scanf \" %d %d\" (fun a b -> Printf.printf \"%d\\n\" (a + b))\n",
  },
  elixir: {
    code: "[a, b] =\n  IO.read(:stdio, :line)\n  |> String.split()\n  |> Enum.map(&String.to_integer/1)\n\nIO.puts(a + b)\n",
  },
  erlang: {
    code: "main(_) ->\n    {ok, [A, B]} = io:fread(\"\", \"~d ~d\"),\n    io:format(\"~p~n\", [A + B]).\n",
  },
  dart: {
    code: "import 'dart:io';\n\nvoid main() {\n  final p = stdin.readLineSync()!.split(' ').map(int.parse).toList();\n  print(p[0] + p[1]);\n}\n",
  },
  fortran: {
    code: "program sum\n  integer(8) :: a, b\n  read(*,*) a, b\n  print \"(I0)\", a + b\nend program sum\n",
  },
  pascal: {
    code: "var a, b: int64;\nbegin\n  readln(a, b);\n  writeln(a + b);\nend.\n",
  },
  objc: {
    code: "#import <Foundation/Foundation.h>\n\nint main(void) {\n  @autoreleasepool {\n    long a, b;\n    scanf(\"%ld %ld\", &a, &b);\n    NSNumber *sum = @(a + b);\n    printf(\"%s\\n\", [[sum stringValue] UTF8String]);\n  }\n  return 0;\n}\n",
  },
  d: {
    code: "import std.stdio;\n\nvoid main() {\n    long a, b;\n    readf(\" %d %d\", &a, &b);\n    writeln(a + b);\n}\n",
  },
  cobol: {
    code: "IDENTIFICATION DIVISION.\nPROGRAM-ID. SUM.\nDATA DIVISION.\nWORKING-STORAGE SECTION.\n01 L PIC X(40).\n01 A PIC 9(9).\n01 B PIC 9(9).\n01 R PIC Z(9)9.\nPROCEDURE DIVISION.\n    ACCEPT L.\n    UNSTRING L DELIMITED BY SPACE INTO A B.\n    COMPUTE R = A + B.\n    DISPLAY FUNCTION TRIM(R).\n    STOP RUN.\n",
  },
  "c-clang": {
    code: "#include <stdio.h>\n\nint main(void) {\n    long a, b;\n    scanf(\"%ld %ld\", &a, &b);\n    printf(\"%ld\\n\", a + b);\n    return 0;\n}\n",
  },
  "cpp-clang": {
    code: "#include <bits/stdc++.h>\nusing namespace std;\n\nint main() {\n    long long a, b;\n    cin >> a >> b;\n    cout << a + b << \"\\n\";\n}\n",
  },
  // These three take no input: a query that brings its own table, a file
  // that is its own output, and a program that writes with a system call.
  sqlite: {
    code: "CREATE TABLE emp (id INTEGER PRIMARY KEY, name TEXT, dept TEXT, salary INT);\nINSERT INTO emp VALUES (1,'ann','eng',120),(2,'bob','eng',100),(3,'cy','ops',90),(4,'di','ops',NULL);\n\nSELECT dept, COUNT(*) AS n, SUM(salary) AS total\nFROM emp\nGROUP BY dept\nORDER BY dept;\n",
    expected: "eng|2|220\nops|2|90\n",
  },
  text: { code: "42\n" },
  assembly: {
    code: "global _start\n\nsection .text\n_start:\n    mov rax, 1          ; write(1, msg, 3)\n    mov rdi, 1\n    mov rsi, msg\n    mov rdx, 3\n    syscall\n    mov rax, 60         ; exit(0)\n    xor rdi, rdi\n    syscall\n\nsection .data\nmsg: db \"42\", 10\n",
  },
};

function sampleFor(key) {
  const s = SAMPLES[key];
  if (!s) return null;
  return { code: s.code, stdin: s.stdin || SUM_INPUT, expected: s.expected || SUM_EXPECTED, sql: !!s.sql };
}
