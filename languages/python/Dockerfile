ARG JUDGE_BIN
FROM ${JUDGE_BIN} AS judge

FROM python:3.13-slim
COPY requirements.txt /tmp/requirements.txt
RUN pip install --no-cache-dir --root-user-action=ignore -r /tmp/requirements.txt \
 && python -m compileall -q /usr/local/lib/python3.13/site-packages || true \
 && rm /tmp/requirements.txt

COPY --from=judge /judge /jexec /opt/judge/
COPY langs/ /opt/judge/langs/
ENTRYPOINT ["/opt/judge/judge", "serve"]
