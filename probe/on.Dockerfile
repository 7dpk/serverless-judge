# Debug shell on top of any runner image: same files, plus the probe server.
ARG BASE
FROM ${BASE}
RUN (apt-get update && apt-get install -y --no-install-recommends python3 time procps strace curl) \
 || (apk add --no-cache python3 procps strace curl) \
 || (microdnf install -y python3 procps-ng strace)
COPY server.py /server.py
ENTRYPOINT []
CMD ["python3", "/server.py"]
