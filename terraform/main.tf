# serverless-judge on Google Cloud.
#
# Everything here is read from the same files deploy.sh uses: a folder under
# languages/ is a runner service, its service.json sizes it, and each
# langs/*.json is a language the API can route to. Adding a language is
# adding a folder; no Terraform changes.

locals {
  root   = abspath("${path.module}/..")
  prefix = var.name_prefix
  labels = { app = "serverless-judge" }

  all_families = toset([for f in fileset("${local.root}/languages", "*/service.json") : dirname(f)])
  chosen       = var.families == null ? local.all_families : var.families

  families = {
    for fam in local.chosen : fam => {
      cpu           = tostring(try(local.service_json[fam].cpu, "2"))
      memory        = try(local.service_json[fam].memory, "2Gi")
      min_instances = try(local.service_json[fam].min_instances, 0)
      max_instances = lookup(var.max_instances, fam, try(local.service_json[fam].max_instances, 8))
      machine_type  = try(local.service_json[fam].machine_type, "")
      prebuild      = try(local.service_json[fam].prebuild, null)
    }
  }
  service_json = {
    for fam in setintersection(local.chosen, local.all_families) :
    fam => jsondecode(file("${local.root}/languages/${fam}/service.json"))
  }

  # One entry per langs/*.json, then one per language id.
  manifests = merge([
    for fam in keys(local.families) : {
      for f in fileset("${local.root}/languages/${fam}/langs", "*.json") :
      "${fam}/${f}" => merge(jsondecode(file("${local.root}/languages/${fam}/langs/${f}")), { family = fam })
    }
  ]...)
  language_ids = merge([
    for m in values(local.manifests) : { for id in m.ids : tostring(id) => m }
  ]...)

  # Cloud Run gives a project a fixed amount of CPU per region, shared by
  # every service. See "Capacity and the CPU quota" in the README.
  rollout_reserve = 6
  vcpu_runners    = sum(concat([0], [for c in values(local.families) : tonumber(c.cpu) * c.max_instances]))
  vcpu_needed     = local.vcpu_runners + var.api_max_instances + var.worker_max_instances + local.rollout_reserve
}

# Refuse instance caps that do not fit the quota: when they do not, a busy
# period lets the runners take all of it and the API can no longer start.
resource "terraform_data" "capacity" {
  lifecycle {
    precondition {
      condition     = length(setsubtract(local.chosen, local.all_families)) == 0
      error_message = "Unknown families: ${join(", ", setsubtract(local.chosen, local.all_families))}. Each must be a folder under languages/."
    }
    precondition {
      condition     = local.vcpu_needed <= var.cpu_quota
      error_message = "Instance caps need ${local.vcpu_needed} vCPU (runners ${local.vcpu_runners}, API ${var.api_max_instances}, worker ${var.worker_max_instances}, rollouts ${local.rollout_reserve}) but cpu_quota is ${var.cpu_quota}. Lower max_instances, deploy fewer families, or raise the quota and set cpu_quota."
    }
  }
}

# --- APIs -------------------------------------------------------------------

resource "google_project_service" "apis" {
  for_each = toset([
    "artifactregistry.googleapis.com",
    "cloudbuild.googleapis.com",
    "cloudresourcemanager.googleapis.com",
    "firestore.googleapis.com",
    "iam.googleapis.com",
    "logging.googleapis.com",
    "pubsub.googleapis.com",
    "run.googleapis.com",
    "secretmanager.googleapis.com",
  ])
  service = each.key

  # Other things in the project may rely on these.
  disable_on_destroy = false
}

# A newly enabled API answers "not enabled" for a little while.
resource "time_sleep" "apis" {
  create_duration = "60s"
  depends_on      = [google_project_service.apis]
}

# --- Images -----------------------------------------------------------------

resource "google_artifact_registry_repository" "judge" {
  repository_id = "${local.prefix}judge"
  location      = var.region
  format        = "DOCKER"
  description   = "serverless-judge images"
  labels        = local.labels

  # Old image versions add up to gigabytes. Keep the three newest of each.
  cleanup_policies {
    id     = "keep-newest"
    action = "KEEP"
    most_recent_versions {
      keep_count = 3
    }
  }
  cleanup_policies {
    id     = "delete-older-than-a-day"
    action = "DELETE"
    condition {
      older_than = "1d"
    }
  }

  depends_on = [time_sleep.apis]
}

# --- Identities -------------------------------------------------------------

# The runner identity has no roles at all: code that escaped a sandbox would
# find credentials that open nothing.
resource "google_service_account" "runner" {
  account_id   = "${local.prefix}judge-runner"
  display_name = "serverless-judge runner"
  depends_on   = [time_sleep.apis]
}

resource "google_service_account" "api" {
  account_id   = "${local.prefix}judge-api"
  display_name = "serverless-judge api"
  depends_on   = [time_sleep.apis]
}

# The identity Pub/Sub uses to call the worker. Its only permission is to
# invoke that one service.
resource "google_service_account" "pusher" {
  account_id   = "${local.prefix}judge-pusher"
  display_name = "serverless-judge pusher"
  depends_on   = [time_sleep.apis]
}

resource "google_project_iam_member" "api_firestore" {
  project = var.project
  role    = "roles/datastore.user"
  member  = google_service_account.api.member
}

# Pub/Sub signs each push as the pusher account, which it may only do if its
# own service agent is allowed to mint tokens for that account.
resource "google_project_service_identity" "pubsub" {
  provider   = google-beta
  service    = "pubsub.googleapis.com"
  depends_on = [time_sleep.apis]
}

resource "google_service_account_iam_member" "pubsub_signs_as_pusher" {
  service_account_id = google_service_account.pusher.name
  role               = "roles/iam.serviceAccountTokenCreator"
  member             = google_project_service_identity.pubsub.member
}

# --- API key ----------------------------------------------------------------

resource "google_secret_manager_secret" "api_key" {
  secret_id = "${local.prefix}judge-api-key"
  labels    = local.labels
  replication {
    auto {}
  }
  depends_on = [time_sleep.apis]
}

# The key is generated here, outside Terraform, so it never appears in the
# state file. Read it with the command in the api_key_command output.
resource "terraform_data" "api_key" {
  input = google_secret_manager_secret.api_key.secret_id

  provisioner "local-exec" {
    command = <<-EOT
      set -eu
      if [ -z "$(gcloud secrets versions list "$SECRET" --project "$PROJECT" --filter 'state=ENABLED' --format 'value(name)' --limit 1)" ]; then
        openssl rand -hex 24 | tr -d '\n' | gcloud secrets versions add "$SECRET" --project "$PROJECT" --data-file=- >/dev/null
        echo "created the API key"
      fi
    EOT
    environment = {
      SECRET  = self.input
      PROJECT = var.project
    }
  }
}

resource "google_secret_manager_secret_iam_member" "api_reads_key" {
  secret_id = google_secret_manager_secret.api_key.id
  role      = "roles/secretmanager.secretAccessor"
  member    = google_service_account.api.member
}

# New accounts and bindings are not honoured at once. 30 seconds was enough
# on a first deploy and not on a redeploy that re-created the accounts (the
# API then failed with "Permission denied on secret"). The services wait for
# their images for longer than this anyway, so a first apply is no slower.
resource "time_sleep" "iam" {
  create_duration = "120s"
  depends_on = [
    google_project_iam_member.api_firestore,
    google_secret_manager_secret_iam_member.api_reads_key,
    google_service_account_iam_member.pubsub_signs_as_pusher,
    google_pubsub_topic_iam_member.api_publishes,
  ]
}
