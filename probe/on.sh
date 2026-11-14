#!/usr/bin/env bash
# usage: probe/on.sh <family>   Redeploys the private judge-probe service on top of that runner image.
set -euo pipefail
cd "$(dirname "$0")/.."
P=$(gcloud config get-value project 2>/dev/null); R=us-central1-docker.pkg.dev/$P/judge
BASE=$(gcloud run services describe "runner-$1" --region us-central1 --format='value(spec.template.spec.containers[0].image)')
IMG=$R/probe:on-$1
cat > /tmp/probe-on-build.yaml <<YAML
steps:
  - name: gcr.io/cloud-builders/docker
    args: [build, -f, on.Dockerfile, --build-arg, "BASE=$BASE", -t, "$IMG", .]
images: ["$IMG"]
YAML
gcloud builds submit probe --config /tmp/probe-on-build.yaml --suppress-logs >/dev/null
gcloud beta run deploy judge-probe --image "$IMG" --sandbox-launcher --no-allow-unauthenticated \
  --service-account "judge-runner@$P.iam.gserviceaccount.com" --cpu 2 --memory 2Gi --concurrency 10 \
  --max-instances 1 --timeout 300 --region us-central1 --quiet >/dev/null 2>&1
echo "probe is now on runner-$1"
