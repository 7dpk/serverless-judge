// Training input for the javac ahead-of-time cache. Never run by the judge.
// It exercises the language features submissions commonly use, so the
// compiler classes those features need are already loaded and linked.
import java.io.*;
import java.math.BigInteger;
import java.util.*;
import java.util.function.*;
import java.util.stream.*;

public class Main {
    record Point(int x, int y) implements Comparable<Point> {
        public int compareTo(Point o) { return Integer.compare(x * x + y * y, o.x * o.x + o.y * o.y); }
    }

    sealed interface Shape permits Circle, Square {}
    record Circle(double r) implements Shape {}
    record Square(double side) implements Shape {}

    enum Op implements IntBinaryOperator {
        ADD { public int applyAsInt(int a, int b) { return a + b; } },
        MUL { public int applyAsInt(int a, int b) { return a * b; } }
    }

    static class Node<T extends Comparable<T>> {
        T value; Node<T> left, right;
        Node(T value) { this.value = value; }
        void insert(T v) {
            if (v.compareTo(value) < 0) { if (left == null) left = new Node<>(v); else left.insert(v); }
            else { if (right == null) right = new Node<>(v); else right.insert(v); }
        }
        int height() { return 1 + Math.max(left == null ? 0 : left.height(), right == null ? 0 : right.height()); }
    }

    static double area(Shape s) {
        return switch (s) {
            case Circle c -> Math.PI * c.r() * c.r();
            case Square q -> q.side() * q.side();
        };
    }

    static <T, R> List<R> mapAll(Collection<? extends T> in, Function<? super T, ? extends R> f) {
        List<R> out = new ArrayList<>();
        for (T t : in) out.add(f.apply(t));
        return out;
    }

    static long[] memo = new long[91];
    static long fib(int n) { return n < 2 ? n : memo[n] != 0 ? memo[n] : (memo[n] = fib(n - 1) + fib(n - 2)); }

    public static void main(String[] args) throws IOException {
        BufferedReader in = new BufferedReader(new InputStreamReader(System.in));
        PrintWriter out = new PrintWriter(new BufferedWriter(new OutputStreamWriter(System.out)));
        StringTokenizer st = new StringTokenizer(Optional.ofNullable(in.readLine()).orElse("5 3"));
        int n = Integer.parseInt(st.nextToken()), k = st.hasMoreTokens() ? Integer.parseInt(st.nextToken()) : 1;

        int[] arr = IntStream.rangeClosed(1, n).map(i -> i * k % 7).toArray();
        Arrays.sort(arr);
        long[][] dp = new long[n + 1][k + 1];
        for (int i = 1; i <= n; i++) for (int j = 1; j <= k; j++) dp[i][j] = Math.max(dp[i - 1][j], dp[i][j - 1]) + arr[i - 1];

        Map<Integer, List<Point>> byQuadrant = new HashMap<>();
        TreeMap<String, Integer> counts = new TreeMap<>(Comparator.reverseOrder());
        Deque<Point> stack = new ArrayDeque<>();
        PriorityQueue<Point> pq = new PriorityQueue<>();
        Set<Long> seen = new HashSet<>();
        for (int i = 0; i < n; i++) {
            Point p = new Point(i - n / 2, arr[i]);
            byQuadrant.computeIfAbsent(p.x() < 0 ? 0 : 1, q -> new ArrayList<>()).add(p);
            counts.merge(p.x() % 2 == 0 ? "even" : "odd", 1, Integer::sum);
            stack.push(p); pq.add(p); seen.add((long) p.x() << 32 | p.y());
        }
        Node<Integer> root = new Node<>(arr.length == 0 ? 0 : arr[0]);
        for (int v : arr) root.insert(v);

        String joined = byQuadrant.values().stream().flatMap(List::stream).sorted()
            .map(p -> p.x() + ":" + p.y()).collect(Collectors.joining(","));
        StringBuilder sb = new StringBuilder();
        for (var e : counts.entrySet()) sb.append(e.getKey()).append('=').append(e.getValue()).append(' ');
        BiFunction<Integer, Integer, Integer> gcd = new BiFunction<>() {
            public Integer apply(Integer a, Integer b) { return b == 0 ? a : this.apply(b, a % b); }
        };
        try {
            out.printf("%d %s %s %d %.2f %s %d%n", dp[n][k], joined, sb.toString().trim(), root.height(),
                area(new Circle(1)) + area(new Square(2)), BigInteger.valueOf(fib(50)).pow(2),
                Op.ADD.applyAsInt(gcd.apply(12, 18), Op.MUL.applyAsInt(2, 3)));
            out.println(mapAll(seen, Long::toHexString).size() + " " + String.join("|", List.of("a", "b")) + " " + pq.peek() + stack.size());
        } catch (ArithmeticException | IllegalStateException e) {
            System.err.println("error: " + e.getMessage());
        } finally {
            out.flush();
        }
    }
}
