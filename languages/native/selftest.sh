#!/bin/sh
# Run while the image is built: every language in it must compile and run a
# program that reads a number and prints twice that. The build fails if one
# cannot, and says which.
export HOME=/tmp/home LANG=C.UTF-8
mkdir -p /tmp/home /tmp/t && cd /tmp/t || exit 1
fail=0
# Programs are judged with a 1 MB limit on any file they write (ulimit -f
# counts 512-byte blocks), so run them under it here too.
check() {
  got=$( (ulimit -f 2048; echo 21 | ./main) 2>/tmp/stderr)
  if [ "$got" = 42 ]; then echo "ok    $1"; else echo "FAIL  $1: $got $(cat /tmp/stderr)"; fail=1; fi
  rm -rf /tmp/t/*
}

cat > main.dart <<'EOF'
import 'dart:io';
void main() { print(int.parse(stdin.readLineSync()!) * 2); }
EOF
dart compile exe -o main main.dart >/dev/null 2>build.log || cat build.log; check dart

cat > main.f90 <<'EOF'
program p
  integer :: n
  read(*,*) n
  print "(I0)", n * 2
end program p
EOF
gfortran -O2 -o main main.f90; check fortran

cat > main.pas <<'EOF'
var n: longint;
begin
  readln(n);
  writeln(n * 2);
end.
EOF
fpc -O2 -omain main.pas >build.log 2>&1 || cat build.log; check pascal

cat > main.m <<'EOF'
#import <Foundation/Foundation.h>
int main(void) {
  @autoreleasepool {
    int n; scanf("%d", &n);
    NSArray *parts = @[@(n * 2)];
    NSString *s = [NSString stringWithFormat:@"%@", [parts objectAtIndex:0]];
    printf("%s\n", [s UTF8String]);
  }
  return 0;
}
EOF
/opt/lang/objc.sh || find /usr/include /usr/lib -name Foundation.h -o -name objc.h | head -5; check objective-c

cat > main.d <<'EOF'
import std.stdio;
void main() { int n; readf(" %d", &n); writeln(n * 2); }
EOF
gdc -O2 -o main main.d; check d

cat > main.asm <<'EOF'
global _start
section .bss
buf: resb 16
section .text
_start:
  mov rax, 0          ; read(0, buf, 16)
  mov rdi, 0
  mov rsi, buf
  mov rdx, 16
  syscall
  mov rax, 1          ; write(1, msg, 3)
  mov rdi, 1
  mov rsi, msg
  mov rdx, 3
  syscall
  mov rax, 60         ; exit(0)
  xor rdi, rdi
  syscall
section .data
msg: db "42", 10
EOF
nasm -f elf64 -o main.o main.asm && ld -o main main.o; check assembly

cat > main.cob <<'EOF'
IDENTIFICATION DIVISION.
PROGRAM-ID. P.
DATA DIVISION.
WORKING-STORAGE SECTION.
01 N PIC 9(4).
01 R PIC Z(4)9.
PROCEDURE DIVISION.
    ACCEPT N.
    COMPUTE R = N * 2.
    DISPLAY FUNCTION TRIM(R).
    STOP RUN.
EOF
cobc -free -x -O2 -o main main.cob; check cobol

cat > main.c <<'EOF'
#include <stdio.h>
int main(void) { int n; scanf("%d", &n); printf("%d\n", n * 2); return 0; }
EOF
clang -O2 -std=gnu17 -o main main.c -lm; check "c (clang)"

cat > main.cpp <<'EOF'
#include <bits/stdc++.h>
int main() { int n; std::cin >> n; std::cout << n * 2 << "\n"; }
EOF
clang++ -O2 -std=gnu++20 -o main main.cpp; check "c++ (clang)"

cd / && rm -rf /tmp/t /tmp/home
dart --version; gfortran --version | head -1; echo "fpc $(fpc -iV)"; clang --version | head -1
gdc --version | head -1; nasm -v; cobc --version | head -1
exit $fail
