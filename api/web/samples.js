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
};

function sampleFor(key) {
  const s = SAMPLES[key];
  if (!s) return null;
  return { code: s.code, stdin: s.stdin || SUM_INPUT, expected: s.expected || SUM_EXPECTED, sql: !!s.sql };
}
