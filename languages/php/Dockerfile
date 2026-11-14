ARG JUDGE_BIN
FROM ${JUDGE_BIN} AS judge

FROM php:8.4-cli-alpine
COPY --from=composer:2 /usr/bin/composer /usr/local/bin/composer
# memory_limit is off so the judge's own limit decides, not PHP's.
RUN printf 'display_errors=stderr\nlog_errors=0\nhtml_errors=0\nmemory_limit=-1\nopcache.enable_cli=0\n' \
      > /usr/local/etc/php/conf.d/judge.ini
# Composer packages from composer.json are autoloaded for every submission.
COPY composer.json /opt/lang/composer.json
RUN set -e; cd /opt/lang; \
    if grep -q '"require"' composer.json; then \
      composer install --no-dev --no-interaction --no-progress --optimize-autoloader; \
      echo 'auto_prepend_file=/opt/lang/vendor/autoload.php' >> /usr/local/etc/php/conf.d/judge.ini; \
    fi

COPY --from=judge /judge /jexec /opt/judge/
COPY langs/ /opt/judge/langs/
ENTRYPOINT ["/opt/judge/judge", "serve"]
