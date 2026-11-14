FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
      python3 gcc g++ libc6-dev time procps util-linux curl ca-certificates strace \
    && rm -rf /var/lib/apt/lists/* && mkdir -p /opt/judge/langs /w
COPY server.py /server.py
CMD ["python3", "/server.py"]
