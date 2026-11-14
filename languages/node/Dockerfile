ARG JUDGE_BIN
FROM ${JUDGE_BIN} AS judge

FROM node:24-slim
WORKDIR /opt/lang
COPY package.json /opt/lang/package.json
RUN npm install --no-audit --no-fund && npm cache clean --force
COPY tsc.sh /opt/lang/tsc.sh
RUN chmod +x /opt/lang/tsc.sh
ENV NODE_PATH=/opt/lang/node_modules

COPY --from=judge /judge /jexec /opt/judge/
COPY langs/ /opt/judge/langs/
WORKDIR /
ENTRYPOINT ["/opt/judge/judge", "serve"]
