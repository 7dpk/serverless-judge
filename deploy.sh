#!/usr/bin/env bash
# Build and deploy the serverless judge.
#
#   ./deploy.sh infra            one-time project setup (safe to re-run)
#   ./deploy.sh lang <family>... build and deploy runners, register languages
#   ./deploy.sh api              build and deploy the API
#   ./deploy.sh all              infra, every language, then the API
#   ./deploy.sh status           URLs and deployed services
#   ./deploy.sh key              print the API key (X-Auth-Token)
#
# A "family" is a folder under languages/ (gcc, python, node, ...). To add a
# language, add a folder with a Dockerfile, service.json and langs/<name>.json
# and run `./deploy.sh lang <folder>`. To add packages, edit that folder's
# package list and run the same command.
#
# Nothing is rebuilt unless its inputs changed: image tags are content hashes.
set -euo pipefail
cd "$(dirname "$0")"

PROJECT="${PROJECT:-$(gcloud config get-value project 2>/dev/null)}"
REGION="${REGION:-us-central1}"
[ -n "$PROJECT" ] || { echo "set PROJECT or run: gcloud config set project <id>" >&2; exit 1; }

REPO="$REGION-docker.pkg.dev/$PROJECT/judge"
DB=judge
TOPIC=judge-runs
SUBSCRIPTION=judge-runs-worker
API_SA="judge-api@$PROJECT.iam.gserviceaccount.com"
RUNNER_SA="judge-runner@$PROJECT.iam.gserviceaccount.com"
PUSHER_SA="judge-pusher@$PROJECT.iam.gserviceaccount.com"
G=(gcloud --project "$PROJECT" --quiet)

# Cloud Run gives a project a fixed amount of CPU per region (200 vCPU by
# default; see "Total CPU allocation" under IAM & Admin > Quotas). Every
# service draws on it. If the runners' instance caps add up to more than the
# quota, a busy period lets them take all of it and the API can no longer
# start instances: callers then get Cloud Run's HTTP 500 page. So the caps
# must fit, with room kept for the API and for rollouts.
QUOTA_VCPU="${QUOTA_VCPU:-200}"
API_MAX_INSTANCES="${API_MAX_INSTANCES:-6}"
WORKER_MAX_INSTANCES="${WORKER_MAX_INSTANCES:-8}"
ROLLOUT_RESERVE_VCPU=6

say() { printf '\n==> %s\n' "$*"; }

# New service accounts take a few seconds to become visible to IAM.
retry() {
  local n=0
  until "$@"; do
    n=$((n + 1)); [ "$n" -ge 8 ] && return 1
    sleep 5
  done
}

# Content hash of files and directories, used as an image tag.
hash_of() {
  find "$@" -type f ! -name '*_test.go' ! -name '.DS_Store' -print0 | sort -z | xargs -0 shasum -a 256 | shasum -a 256 | cut -c1-12
}

json_get() { python3 -c 'import json,sys; d=json.load(open(sys.argv[1]));
for k in sys.argv[2].split("."): d = d.get(k, {}) if isinstance(d, dict) else {}
print(d if d != {} else sys.argv[3])' "$1" "$2" "${3:-}"; }

# build <image> <context dir> <dockerfile> <machine type or ""> [build args...]
# Skips the build when that tag already exists.
build() { PROJECT="$PROJECT" cloudbuild/build.sh "$@"; }

# Waits out what is left of the log routing delay (see cmd_infra), so that
# nothing a new service logs lands in the default 30-day bucket.
LOG_ROUTING_READY=0
settle_logs() {
  local left=$(( LOG_ROUTING_READY - $(date +%s) ))
  [ "$left" -gt 0 ] || return 0
  echo "waiting ${left}s for log routing to take effect"
  sleep "$left"
}

# check_budget refuses to deploy instance caps that do not fit the quota.
check_budget() {
  python3 - "$QUOTA_VCPU" "$API_MAX_INSTANCES" "$ROLLOUT_RESERVE_VCPU" "$WORKER_MAX_INSTANCES" <<'PY'
import glob, json, sys
quota, api, reserve, worker = float(sys.argv[1]), int(sys.argv[2]), float(sys.argv[3]), int(sys.argv[4])
rows = []
for path in sorted(glob.glob("languages/*/service.json")):
    cfg = json.load(open(path))
    rows.append((path.split("/")[1], int(cfg.get("max_instances", 50)), float(cfg.get("cpu", 2))))
runners = sum(n * cpu for _, n, cpu in rows)
total = runners + api + worker + reserve   # API and worker run on 1 vCPU per instance
if total > quota:
    print(f"instance caps need {total:.0f} vCPU but the regional quota is {quota:.0f}:", file=sys.stderr)
    for name, n, cpu in rows:
        print(f"  runner-{name:<10} {n:>3} x {cpu:g} vCPU = {n * cpu:g}", file=sys.stderr)
    print(f"  judge-api         {api:>3} x 1 vCPU = {api}", file=sys.stderr)
    print(f"  judge-worker      {worker:>3} x 1 vCPU = {worker}", file=sys.stderr)
    print(f"  rollout reserve               {reserve:g}", file=sys.stderr)
    print("lower max_instances in languages/*/service.json, or raise the quota and set QUOTA_VCPU.", file=sys.stderr)
    sys.exit(1)
print(f"capacity: runners {runners:.0f} + API {api} + worker {worker} + reserve {reserve:.0f} = {total:.0f} of {quota:.0f} vCPU")
PY
}

project_number() { "${G[@]}" projects describe "$PROJECT" --format='value(projectNumber)'; }

cmd_infra() {
  say "enabling APIs"
  "${G[@]}" services enable run.googleapis.com cloudbuild.googleapis.com artifactregistry.googleapis.com \
    pubsub.googleapis.com firestore.googleapis.com secretmanager.googleapis.com iam.googleapis.com

  say "artifact registry"
  "${G[@]}" artifacts repositories describe judge --location "$REGION" >/dev/null 2>&1 ||
    "${G[@]}" artifacts repositories create judge --repository-format docker --location "$REGION" \
      --description "serverless-judge images"

  # Old image versions add up to gigabytes. Keep the three newest of each.
  "${G[@]}" artifacts repositories set-cleanup-policies judge --location "$REGION" \
    --policy cloudbuild/cleanup-policy.json --no-dry-run >/dev/null

  say "service accounts"
  # The runner identity has no roles at all: code that escaped a sandbox
  # would find credentials that open nothing.
  # judge-pusher is the identity Pub/Sub uses to call the worker service.
  # Its only permission is to invoke that one service (granted in cmd_api).
  for sa in judge-runner judge-api judge-pusher; do
    "${G[@]}" iam service-accounts describe "$sa@$PROJECT.iam.gserviceaccount.com" >/dev/null 2>&1 ||
      "${G[@]}" iam service-accounts create "$sa" --display-name "serverless-judge ${sa#judge-}"
  done
  retry "${G[@]}" projects add-iam-policy-binding "$PROJECT" --member "serviceAccount:$API_SA" \
    --role roles/datastore.user --condition None >/dev/null 2>&1

  say "firestore database '$DB'"
  "${G[@]}" firestore databases describe --database "$DB" >/dev/null 2>&1 ||
    "${G[@]}" firestore databases create --database "$DB" --location "$REGION" --type firestore-native
  # Nothing about an execution is kept beyond 7 days. Submissions expire
  # after 6 (SUBMISSION_TTL_HOURS) because Firestore removes expired
  # documents within about a day. Temporary tokens go the same way.
  "${G[@]}" firestore fields ttls update expire_at --collection-group submissions --enable-ttl \
    --database "$DB" --async >/dev/null 2>&1 || true
  "${G[@]}" firestore fields ttls update expires_at --collection-group tokens --enable-ttl \
    --database "$DB" --async >/dev/null 2>&1 || true
  # Submissions are only ever read by token. Firestore indexes every field
  # unless told not to, which costs money per write and, for the timestamp
  # fields, caps write throughput. Exempt them all, once.
  if [ -n "$("${G[@]}" firestore indexes fields describe created_at --collection-group submissions \
        --database "$DB" --format 'value(indexConfig.indexes[0].queryScope)' 2>/dev/null)" ]; then
    local f
    for f in token language_id group source stdin expected has_expected files compiler_options args \
             callback_url cpu_ms wall_ms memory_kb file_kb procs redirect_stderr status_id stdout stderr \
             compile_output message time_ms wall_time_ms used_kb exit_code exit_signal ran created_at \
             finished_at expire_at claimed_at; do
      "${G[@]}" firestore indexes fields update "$f" --collection-group submissions --database "$DB" \
        --disable-indexes --async >/dev/null 2>&1 || true
    done
    echo "index exemptions requested"
  fi
  # The analytics counters are maps with a few hundred leaves per document,
  # read only by document id. Indexed, they take about forty times the
  # space of the data itself. Exempting the two top-level fields covers
  # everything inside them.
  if [ -n "$("${G[@]}" firestore indexes fields describe l --collection-group stats \
        --database "$DB" --format 'value(indexConfig.indexes[0].queryScope)' 2>/dev/null)" ]; then
    for f in t l; do
      "${G[@]}" firestore indexes fields update "$f" --collection-group stats --database "$DB" \
        --disable-indexes --async >/dev/null 2>&1 || true
    done
    echo "analytics index exemptions requested"
  fi

  say "logs kept for 7 days"
  # Request and service logs of the judge go to their own bucket with 7-day
  # retention, and are kept out of the project's default 30-day bucket. They
  # hold no code or output, but they do record that executions happened.
  local logfilter='resource.type="cloud_run_revision" AND resource.labels.service_name=~"^(judge-|runner-)"'
  "${G[@]}" logging buckets describe judge-logs --location global >/dev/null 2>&1 ||
    "${G[@]}" logging buckets create judge-logs --location global --retention-days 7 \
      --description "serverless-judge service logs" >/dev/null
  "${G[@]}" logging buckets update judge-logs --location global --retention-days 7 >/dev/null
  "${G[@]}" logging sinks describe judge-logs >/dev/null 2>&1 ||
    "${G[@]}" logging sinks create judge-logs \
      "logging.googleapis.com/projects/$PROJECT/locations/global/buckets/judge-logs" --log-filter "$logfilter" >/dev/null
  if ! "${G[@]}" logging sinks describe _Default --format 'value(exclusions[].name)' 2>/dev/null | grep -q serverless-judge; then
    "${G[@]}" logging sinks update _Default --add-exclusion "name=serverless-judge,filter=$logfilter" >/dev/null
    # A new routing rule took up to 4 minutes to be applied when measured.
    # settle_logs holds the first service deploy of this run until then.
    LOG_ROUTING_READY=$(( $(date +%s) + 300 ))
  fi

  say "queue topic '$TOPIC'"
  # Queued submissions travel through Pub/Sub (see api/dispatch.go). The
  # push subscription is created in cmd_api, once the worker has a URL.
  "${G[@]}" pubsub topics describe "$TOPIC" >/dev/null 2>&1 || "${G[@]}" pubsub topics create "$TOPIC" >/dev/null
  retry "${G[@]}" pubsub topics add-iam-policy-binding "$TOPIC" --member "serviceAccount:$API_SA" \
    --role roles/pubsub.publisher >/dev/null 2>&1
  # Pub/Sub signs each push as judge-pusher, which it may only do if its own
  # service agent is allowed to mint tokens for that account.
  retry "${G[@]}" iam service-accounts add-iam-policy-binding "$PUSHER_SA" \
    --member "serviceAccount:service-$(project_number)@gcp-sa-pubsub.iam.gserviceaccount.com" \
    --role roles/iam.serviceAccountTokenCreator >/dev/null 2>&1

  say "secrets"
  for s in judge-api-key; do
    if ! "${G[@]}" secrets describe "$s" >/dev/null 2>&1; then
      openssl rand -hex 24 | tr -d '\n' | "${G[@]}" secrets create "$s" --data-file=- --replication-policy automatic >/dev/null
      echo "created $s"
    fi
    retry "${G[@]}" secrets add-iam-policy-binding "$s" --member "serviceAccount:$API_SA" \
      --role roles/secretmanager.secretAccessor >/dev/null 2>&1
  done
  echo "infra ready"
}

bin_image() { echo "$REPO/judge-bin:$(hash_of runner)"; }

cmd_lang() {
  [ $# -gt 0 ] || { echo "usage: ./deploy.sh lang <family>..." >&2; exit 1; }
  check_budget
  local bin; bin="$(bin_image)"
  build "$bin" runner Dockerfile ""
  local pids=() fam
  for fam in "$@"; do
    [ -d "languages/$fam" ] || { echo "no such family: languages/$fam" >&2; exit 1; }
    deploy_family "$fam" "$bin" &
    pids+=($!)
  done
  local rc=0
  for p in ${pids[@]+"${pids[@]}"}; do wait "$p" || rc=1; done
  return $rc
}

deploy_family() {
  local fam="$1" bin="$2" dir="languages/$1" cfg="languages/$1/service.json"
  local args=("JUDGE_BIN=$bin")

  local pre; pre="$(json_get "$cfg" prebuild.dockerfile)"
  if [ -n "$pre" ]; then
    local inputs=() f
    for f in $(python3 -c 'import json,sys; print(" ".join(json.load(open(sys.argv[1]))["prebuild"]["inputs"]))' "$cfg"); do
      inputs+=("$dir/$f")
    done
    local pimg="$REPO/$fam-prebuilt:$(hash_of "${inputs[@]}")"
    build "$pimg" "$dir" "$pre" "$(json_get "$cfg" prebuild.machine_type)"
    args+=("PREBUILT=$pimg")
  fi

  local image="$REPO/runner-$fam:$(hash_of "$dir" runner)"
  build "$image" "$dir" Dockerfile "$(json_get "$cfg" machine_type)" "${args[@]}"

  settle_logs
  say "deploying runner-$fam"
  "${G[@]}" beta run deploy "runner-$fam" --image "$image" --region "$REGION" \
    --sandbox-launcher --execution-environment gen2 \
    --no-allow-unauthenticated --service-account "$RUNNER_SA" \
    --cpu "$(json_get "$cfg" cpu 2)" --memory "$(json_get "$cfg" memory 2Gi)" \
    --concurrency 1 --timeout 300 --cpu-boost \
    --min-instances "$(json_get "$cfg" min_instances 0)" \
    --max-instances "$(json_get "$cfg" max_instances 50)" \
    --labels app=serverless-judge >/dev/null
  # Cloud Run keeps refusing a caller for about a minute after it has been
  # granted access (62 to 70 s when measured). Registering the language is
  # what makes the API route here, so a new grant waits before that.
  if ! "${G[@]}" run services get-iam-policy "runner-$fam" --region "$REGION" \
      --format 'value(bindings.members)' 2>/dev/null | grep -q "$API_SA"; then
    "${G[@]}" run services add-iam-policy-binding "runner-$fam" --region "$REGION" \
      --member "serviceAccount:$API_SA" --role roles/run.invoker >/dev/null
    echo "runner-$fam: waiting 2 minutes for the API's access to take effect"
    sleep 120
  fi

  local url; url="$("${G[@]}" run services describe "runner-$fam" --region "$REGION" --format='value(status.url)')"
  register "$fam" "$url"
  echo "runner-$fam ready: $url"
}

# Write one registry row per language id, so the API can route to the runner.
register() {
  local fam="$1" url="$2" token base id title doc
  token="$("${G[@]}" auth print-access-token)"
  base="https://firestore.googleapis.com/v1/projects/$PROJECT/databases/$DB/documents/languages"
  # Python only builds the documents; curl sends them, so this works on
  # machines whose Python has no CA bundle.
  python3 - "$fam" "$url" <<'PY' | while IFS=$'\t' read -r id title doc; do
import glob, json, sys
fam, url = sys.argv[1:3]
cap = int(json.load(open(f"languages/{fam}/service.json")).get("max_instances", 8))
for path in sorted(glob.glob(f"languages/{fam}/langs/*.json")):
    m = json.load(open(path))
    for i in m["ids"]:
        doc = {"fields": {
            "id": {"integerValue": str(i)},
            "name": {"stringValue": m["title"]},
            "lang": {"stringValue": m["name"]},
            "url": {"stringValue": url},
            "source_file": {"stringValue": m["source_file"]},
            "max_instances": {"integerValue": str(cap)},
        }}
        print(i, m["title"], json.dumps(doc), sep="\t")
PY
    curl -fsS -X PATCH "$base/$id" -H "Authorization: Bearer $token" -H "Content-Type: application/json" \
      -d "$doc" >/dev/null && echo "registered $id: $title"
  done
}

# One image, two services: the public API and the private worker that runs
# queued submissions (see api/main.go).
cmd_api() {
  check_budget
  local image="$REPO/judge-api:$(hash_of api runner/proto runner/go.mod)"
  build "$image" . api/Dockerfile ""
  local env="PROJECT_ID=$PROJECT,FIRESTORE_DB=$DB,QUEUE_TOPIC=$TOPIC"

  settle_logs
  say "deploying judge-worker"
  "${G[@]}" run deploy judge-worker --image "$image" --region "$REGION" \
    --no-allow-unauthenticated --service-account "$API_SA" \
    --cpu 1 --memory 512Mi --concurrency 1000 --timeout 900 --cpu-boost \
    --min-instances 0 --max-instances "$WORKER_MAX_INSTANCES" \
    --set-env-vars "$env,ROLE=worker" --labels app=serverless-judge >/dev/null
  "${G[@]}" run services add-iam-policy-binding judge-worker --region "$REGION" \
    --member "serviceAccount:$PUSHER_SA" --role roles/run.invoker >/dev/null
  local worker; worker="$("${G[@]}" run services describe judge-worker --region "$REGION" --format='value(status.url)')"

  # Pub/Sub holds each delivery open for up to 10 minutes, retries with a
  # short backoff, and drops what is 30 minutes old (the worker marks a
  # submission failed well before that).
  local sub=(--push-endpoint "$worker/internal/run" --push-auth-service-account "$PUSHER_SA"
    --push-auth-token-audience "$worker" --ack-deadline 600 --min-retry-delay 5s --max-retry-delay 60s
    --message-retention-duration 30m --expiration-period never)
  if "${G[@]}" pubsub subscriptions describe "$SUBSCRIPTION" >/dev/null 2>&1; then
    "${G[@]}" pubsub subscriptions update "$SUBSCRIPTION" "${sub[@]}" >/dev/null
  else
    "${G[@]}" pubsub subscriptions create "$SUBSCRIPTION" --topic "$TOPIC" "${sub[@]}" >/dev/null
  fi

  say "deploying judge-api"
  "${G[@]}" run deploy judge-api --image "$image" --region "$REGION" \
    --allow-unauthenticated --service-account "$API_SA" \
    --cpu 1 --memory 512Mi --concurrency 500 --timeout 900 --cpu-boost \
    --min-instances "${API_MIN_INSTANCES:-0}" --max-instances "$API_MAX_INSTANCES" \
    --set-env-vars "$env,ROLE=api" --set-secrets "AUTH_TOKENS=judge-api-key:latest" \
    --labels app=serverless-judge >/dev/null
  echo "judge-api ready: https://judge-api-$(project_number).$REGION.run.app"
}

families() { ls languages; }

cmd_all() {
  cmd_infra
  # shellcheck disable=SC2046
  cmd_lang $(families)
  cmd_api
}

cmd_status() {
  "${G[@]}" run services list --region "$REGION" --filter 'metadata.labels.app=serverless-judge' \
    --format 'table(metadata.name, status.url, status.conditions[0].status:label=READY)'
}

cmd_key() { "${G[@]}" secrets versions access latest --secret judge-api-key; echo; }

case "${1:-}" in
  infra) cmd_infra ;;
  lang) shift; cmd_lang "$@" ;;
  api) cmd_api ;;
  all) cmd_all ;;
  status) cmd_status ;;
  key) cmd_key ;;
  *) sed -n '2,16p' "$0" | sed 's/^# \{0,1\}//'; exit 1 ;;
esac
