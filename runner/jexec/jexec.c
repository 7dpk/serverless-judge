/*
 * jexec: launch one program under resource limits and report how it ended.
 *
 * It runs inside the sandbox, between the supervisor (judge box) and the
 * untrusted program:
 *
 *   judge box  ->  jexec (waits, reports)  ->  program
 *
 * Why a separate tiny launcher: a child's peak-memory figure (ru_maxrss)
 * starts at the size of whatever forked it. Forking from this small static
 * binary instead of the Go supervisor keeps that floor near zero, so a
 * "hello world" reports its own memory rather than the judge's.
 *
 *   jexec -r report_fd [-c cpu_s] [-a as_kb] [-p nproc] [-f fsize_kb]
 *         [-s stack_kb] [-i stdin] [-o stdout] [-e stderr] [-m] [-d dir]
 *         -- cmd [args...]
 *
 *   -m  send stderr to the same file as stdout
 *
 * One line is written to report_fd when the program ends:
 *
 *   <exited 0|1> <exit code or signal> <user usec> <sys usec> <maxrss kb>
 *
 * The launcher is non-dumpable and the report fd is closed before exec, so
 * the program cannot write the report itself. If the program kills the
 * launcher there is no report, and the supervisor fails the test.
 */
#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/prctl.h>
#include <sys/resource.h>
#include <sys/wait.h>
#include <unistd.h>

static void die(const char *what) {
    fprintf(stderr, "jexec: %s: %s\n", what, strerror(errno));
    _exit(125);
}

/* Limits can only be lowered here: the sandbox has no CAP_SYS_RESOURCE, so a
 * request above the current hard limit is clamped to it. */
static void limit(int res, rlim_t soft, rlim_t hard) {
    struct rlimit cur;
    if (getrlimit(res, &cur) != 0) die("getrlimit");
    if (cur.rlim_max != RLIM_INFINITY) {
        if (hard > cur.rlim_max) hard = cur.rlim_max;
        if (soft > hard) soft = hard;
    }
    struct rlimit r = {soft, hard};
    if (setrlimit(res, &r) != 0) die("setrlimit");
}

int main(int argc, char **argv) {
    long cpu = 0, as_kb = 0, nproc = 0, fsize_kb = 0, stack_kb = 0;
    const char *in = NULL, *out = NULL, *err = NULL, *dir = NULL;
    int merge = 0, report = -1, opt;

    while ((opt = getopt(argc, argv, "+r:c:a:p:f:s:i:o:e:d:m")) != -1) {
        switch (opt) {
        case 'r': report = atoi(optarg); break;
        case 'c': cpu = atol(optarg); break;
        case 'a': as_kb = atol(optarg); break;
        case 'p': nproc = atol(optarg); break;
        case 'f': fsize_kb = atol(optarg); break;
        case 's': stack_kb = atol(optarg); break;
        case 'i': in = optarg; break;
        case 'o': out = optarg; break;
        case 'e': err = optarg; break;
        case 'd': dir = optarg; break;
        case 'm': merge = 1; break;
        default: return 125;
        }
    }
    if (optind >= argc || report < 0) return 125;

    prctl(PR_SET_DUMPABLE, 0, 0, 0, 0);

    pid_t pid = fork();
    if (pid < 0) return 125;

    if (pid > 0) {
        int status = 0;
        struct rusage ru;
        memset(&ru, 0, sizeof ru);
        while (wait4(pid, &status, 0, &ru) < 0) {
            if (errno != EINTR) return 125;
        }
        char line[160];
        int n = snprintf(line, sizeof line, "%d %d %lld %lld %ld\n",
                         WIFEXITED(status) ? 1 : 0,
                         WIFEXITED(status) ? WEXITSTATUS(status) : WTERMSIG(status),
                         (long long)ru.ru_utime.tv_sec * 1000000LL + ru.ru_utime.tv_usec,
                         (long long)ru.ru_stime.tv_sec * 1000000LL + ru.ru_stime.tv_usec,
                         (long)ru.ru_maxrss);
        if (write(report, line, (size_t)n) != n) return 125;
        return 0;
    }

    /* Child: becomes the program. */
    setsid();
    if (dir && chdir(dir) != 0) die("chdir");

    int fd = open(in ? in : "/dev/null", O_RDONLY);
    if (fd < 0) die("open stdin");
    if (dup2(fd, 0) < 0) die("dup2");
    if (fd > 2) close(fd);

    /* O_APPEND keeps interleaving sane when stdout and stderr share a file. */
    fd = open(out ? out : "/dev/null", O_WRONLY | O_CREAT | O_TRUNC | O_APPEND, 0600);
    if (fd < 0) die("open stdout");
    if (dup2(fd, 1) < 0) die("dup2");
    if (fd > 2) close(fd);

    if (merge) {
        if (dup2(1, 2) < 0) die("dup2");
    } else {
        fd = open(err ? err : "/dev/null", O_WRONLY | O_CREAT | O_TRUNC | O_APPEND, 0600);
        if (fd < 0) die("open stderr");
        if (dup2(fd, 2) < 0) die("dup2");
        if (fd > 2) close(fd);
    }

    /* Nothing else may leak into the program, the report fd least of all. */
    for (fd = 3; fd < 256; fd++) close(fd);

    limit(RLIMIT_CORE, 0, 0);
    if (cpu > 0) limit(RLIMIT_CPU, (rlim_t)cpu, (rlim_t)cpu + 1);
    if (as_kb > 0) limit(RLIMIT_AS, (rlim_t)as_kb * 1024, (rlim_t)as_kb * 1024);
    if (fsize_kb > 0) limit(RLIMIT_FSIZE, (rlim_t)fsize_kb * 1024, (rlim_t)fsize_kb * 1024);
    if (stack_kb > 0) limit(RLIMIT_STACK, (rlim_t)stack_kb * 1024, (rlim_t)stack_kb * 1024);
    if (nproc > 0) limit(RLIMIT_NPROC, (rlim_t)nproc, (rlim_t)nproc);

    prctl(PR_SET_DUMPABLE, 1, 0, 0, 0);
    prctl(PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0);

    execvp(argv[optind], &argv[optind]);
    int e = errno;
    fprintf(stderr, "jexec: cannot run %s: %s\n", argv[optind], strerror(e));
    _exit(e == ENOENT ? 127 : 126);
}
