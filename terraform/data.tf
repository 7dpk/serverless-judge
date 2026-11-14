# Where things are kept: submissions and counters in Firestore, queued work
# in Pub/Sub, service logs in their own 7-day bucket.

# --- Firestore --------------------------------------------------------------

resource "google_firestore_database" "judge" {
  name        = "${local.prefix}judge"
  location_id = coalesce(var.firestore_location, var.region)
  type        = "FIRESTORE_NATIVE"

  deletion_policy         = var.delete_data_on_destroy ? "DELETE" : "ABANDON"
  delete_protection_state = "DELETE_PROTECTION_DISABLED"

  depends_on = [time_sleep.apis]
}

# Nothing about an execution is kept beyond 7 days: submissions carry an
# expire_at (var.submission_ttl_hours after creation) and Firestore deletes
# them within about a day of it. Temporary tokens go the same way.
#
# deletion_policy = "ABANDON" on these three: terraform destroy then leaves
# the rules alone. If the database is kept, what is in it must still
# expire; if it is deleted, the rules go with it.
resource "google_firestore_field" "submission_ttl" {
  database        = google_firestore_database.judge.name
  collection      = "submissions"
  field           = "expire_at"
  deletion_policy = "ABANDON"
  ttl_config {}
  index_config {}
}

resource "google_firestore_field" "token_ttl" {
  database        = google_firestore_database.judge.name
  collection      = "tokens"
  field           = "expires_at"
  deletion_policy = "ABANDON"
  ttl_config {}
}

# Submissions are only ever read by token. Firestore indexes every field
# unless told not to, which slows each write and, for the timestamp fields,
# caps write throughput. Exempt them all.
resource "google_firestore_field" "submissions_unindexed" {
  database        = google_firestore_database.judge.name
  collection      = "submissions"
  field           = "*"
  deletion_policy = "ABANDON"
  index_config {}
}

# The analytics counters are maps with a few hundred leaves per document,
# read only by document id. Left indexed, each leaf costs two index entries:
# about forty times the size of the data, kept for as long as the counters.
resource "google_firestore_field" "stats_unindexed" {
  database        = google_firestore_database.judge.name
  collection      = "stats"
  field           = "*"
  deletion_policy = "ABANDON"
  index_config {}
}

# One registry row per language id, so the API can route to its runner. The
# API re-reads the registry every 30 seconds.
resource "google_firestore_document" "language" {
  for_each    = local.language_ids
  database    = google_firestore_database.judge.name
  collection  = "languages"
  document_id = each.key

  fields = jsonencode({
    id            = { integerValue = each.key }
    name          = { stringValue = each.value.title }
    lang          = { stringValue = each.value.name }
    url           = { stringValue = google_cloud_run_v2_service.runner[each.value.family].uri }
    source_file   = { stringValue = each.value.source_file }
    max_instances = { integerValue = tostring(local.families[each.value.family].max_instances) }
  })

  depends_on = [time_sleep.runner_access]
}

# --- Queue ------------------------------------------------------------------

# Queued submissions travel through Pub/Sub (see api/dispatch.go).
resource "google_pubsub_topic" "runs" {
  name       = "${local.prefix}judge-runs"
  labels     = local.labels
  depends_on = [time_sleep.apis]
}

resource "google_pubsub_topic_iam_member" "api_publishes" {
  topic  = google_pubsub_topic.runs.id
  role   = "roles/pubsub.publisher"
  member = google_service_account.api.member
}

# Pub/Sub holds each delivery open for up to 10 minutes, retries with a short
# backoff, and drops what is 30 minutes old (the worker marks a submission
# failed well before that).
resource "google_pubsub_subscription" "worker" {
  name   = "${local.prefix}judge-runs-worker"
  topic  = google_pubsub_topic.runs.id
  labels = local.labels

  ack_deadline_seconds       = 600
  message_retention_duration = "1800s"

  expiration_policy {
    ttl = ""
  }
  retry_policy {
    minimum_backoff = "5s"
    maximum_backoff = "60s"
  }
  push_config {
    push_endpoint = "${google_cloud_run_v2_service.worker.uri}/internal/run"
    oidc_token {
      service_account_email = google_service_account.pusher.email
      audience              = google_cloud_run_v2_service.worker.uri
    }
  }

  depends_on = [
    google_cloud_run_v2_service_iam_member.pusher_invokes_worker,
    time_sleep.iam,
  ]
}

# --- Logs -------------------------------------------------------------------

# Request and service logs of the judge go to their own bucket with 7-day
# retention, and are kept out of the project's default 30-day bucket. They
# hold no code or output, but they do record that executions happened.
locals {
  log_filter = "resource.type=\"cloud_run_revision\" AND resource.labels.service_name=~\"^(${local.prefix}judge-|${local.prefix}runner-)\""
}

# Cloud Logging keeps a deleted bucket for 7 days and refuses to create
# another with its name in that time. If an earlier destroy left one behind,
# bring it back; Terraform then adopts it. On a first deploy there is nothing
# to restore and this does nothing.
resource "terraform_data" "restore_log_bucket" {
  input = "${local.prefix}judge-logs"

  provisioner "local-exec" {
    command = "gcloud logging buckets undelete \"$BUCKET\" --location global --project \"$PROJECT\" --quiet >/dev/null 2>&1 || true"
    environment = {
      BUCKET  = self.input
      PROJECT = var.project
    }
  }

  depends_on = [time_sleep.apis]
}

resource "google_logging_project_bucket_config" "judge" {
  project        = var.project
  location       = "global"
  bucket_id      = terraform_data.restore_log_bucket.output
  retention_days = 7
  description    = "serverless-judge service logs"
}

resource "google_logging_project_sink" "judge" {
  name        = "${local.prefix}judge-logs"
  destination = "logging.googleapis.com/${google_logging_project_bucket_config.judge.id}"
  filter      = local.log_filter
}

# An exclusion on the project is an exclusion on its _Default sink.
resource "google_logging_project_exclusion" "judge" {
  name        = "${local.prefix}serverless-judge"
  description = "serverless-judge logs live in the ${local.prefix}judge-logs bucket"
  filter      = local.log_filter
  depends_on  = [google_logging_project_sink.judge]
}

# A new routing rule is not applied at once: lines kept reaching the default
# bucket for up to 3 minutes 55 seconds after this was created. The services
# wait for it, so nothing they log is kept for 30 days. Image builds take
# longer than this, so a first apply is no slower.
resource "time_sleep" "log_routing" {
  create_duration = "300s"
  depends_on      = [google_logging_project_exclusion.judge]
}
