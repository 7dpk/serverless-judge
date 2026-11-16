# The Cloud Run services. Every one has min instances 0 by default and
# cpu_idle = true (request-based billing): an instance is billed only while
# it handles a request, and with no traffic there are no instances.
#
# cpu_idle must be spelled out. Once a resources block is present, leaving it
# unset means "CPU always allocated", which bills for the whole life of each
# instance.

# One service per toolchain image. Each instance takes one request at a time
# and may start sandboxes; the program under test runs inside one.
resource "google_cloud_run_v2_service" "runner" {
  provider = google-beta
  for_each = local.families

  name                = "${local.prefix}runner-${each.key}"
  location            = var.region
  launch_stage        = "BETA"
  ingress             = "INGRESS_TRAFFIC_ALL"
  deletion_protection = false
  labels              = local.labels

  template {
    service_account                  = google_service_account.runner.email
    execution_environment            = "EXECUTION_ENVIRONMENT_GEN2"
    max_instance_request_concurrency = 1
    timeout                          = "300s"
    labels                           = local.labels

    scaling {
      min_instance_count = each.value.min_instances
      max_instance_count = each.value.max_instances
    }

    containers {
      image            = terraform_data.runner_image[each.key].output
      sandbox_launcher = true

      # The runner refuses a request for network access unless this is 1,
      # whatever the API asks of it.
      env {
        name  = "JUDGE_ALLOW_NETWORK"
        value = var.allow_network ? "1" : "0"
      }

      resources {
        limits = {
          cpu    = each.value.cpu
          memory = each.value.memory
        }
        cpu_idle          = true
        startup_cpu_boost = true
      }
    }
  }

  depends_on = [terraform_data.capacity, time_sleep.iam, time_sleep.log_routing]
}

# Runners accept calls from the API's identity and nobody else.
resource "google_cloud_run_v2_service_iam_member" "api_invokes_runner" {
  for_each = local.families
  name     = google_cloud_run_v2_service.runner[each.key].name
  location = var.region
  role     = "roles/run.invoker"
  member   = google_service_account.api.member
}

# Cloud Run keeps refusing a caller for a while after it has been granted
# access: 62 to 70 seconds when this was measured. The API routes to a runner
# as soon as its languages are in the registry, so the registry rows (in
# data.tf) wait for this. Without it, the first minute after an apply gives
# "runner HTTP 403" verdicts.
resource "time_sleep" "runner_access" {
  for_each        = local.families
  create_duration = "120s"

  # The public grant on the API is given the same time to settle, so the
  # URLs that apply prints work when it returns.
  depends_on = [
    google_cloud_run_v2_service_iam_member.api_invokes_runner,
    google_cloud_run_v2_service_iam_member.anyone_invokes_api,
  ]
}

locals {
  api_env = {
    PROJECT_ID           = var.project
    FIRESTORE_DB         = google_firestore_database.judge.name
    QUEUE_TOPIC          = google_pubsub_topic.runs.name
    SUBMISSION_TTL_HOURS = tostring(var.submission_ttl_hours)

    ALLOW_ENABLE_NETWORK   = var.allow_network ? "1" : "0"
    NETWORK_TIME_LIMIT     = tostring(min(var.network_time_limit, var.max_network_time_limit))
    MAX_NETWORK_TIME_LIMIT = tostring(var.max_network_time_limit)
    MAX_DOWNLOAD_MB        = tostring(var.max_download_mb)
  }
}

# Private. Pub/Sub pushes each queued group here and it runs them. It is its
# own service so a backlog cannot use up the capacity that answers callers.
resource "google_cloud_run_v2_service" "worker" {
  name                = "${local.prefix}judge-worker"
  location            = var.region
  ingress             = "INGRESS_TRAFFIC_ALL"
  deletion_protection = false
  labels              = local.labels

  template {
    service_account                  = google_service_account.api.email
    max_instance_request_concurrency = 1000
    timeout                          = "900s"
    labels                           = local.labels

    scaling {
      min_instance_count = 0
      max_instance_count = var.worker_max_instances
    }

    containers {
      image = terraform_data.api_image.output

      dynamic "env" {
        for_each = merge(local.api_env, { ROLE = "worker" })
        content {
          name  = env.key
          value = env.value
        }
      }

      resources {
        limits = {
          cpu    = "1"
          memory = "512Mi"
        }
        cpu_idle          = true
        startup_cpu_boost = true
      }
    }
  }

  depends_on = [terraform_data.capacity, time_sleep.iam, time_sleep.log_routing]
}

resource "google_cloud_run_v2_service_iam_member" "pusher_invokes_worker" {
  name     = google_cloud_run_v2_service.worker.name
  location = var.region
  role     = "roles/run.invoker"
  member   = google_service_account.pusher.member
}

# Public. Callers authenticate with X-Auth-Token, checked by the API itself.
resource "google_cloud_run_v2_service" "api" {
  name                = "${local.prefix}judge-api"
  location            = var.region
  ingress             = "INGRESS_TRAFFIC_ALL"
  deletion_protection = false
  labels              = local.labels

  template {
    service_account                  = google_service_account.api.email
    max_instance_request_concurrency = 500
    timeout                          = "900s"
    labels                           = local.labels

    scaling {
      min_instance_count = var.api_min_instances
      max_instance_count = var.api_max_instances
    }

    containers {
      image = terraform_data.api_image.output

      dynamic "env" {
        for_each = merge(local.api_env, { ROLE = "api" })
        content {
          name  = env.key
          value = env.value
        }
      }
      env {
        name = "AUTH_TOKENS"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.api_key.secret_id
            version = "latest"
          }
        }
      }

      resources {
        limits = {
          cpu    = "1"
          memory = "512Mi"
        }
        cpu_idle          = true
        startup_cpu_boost = true
      }
    }
  }

  depends_on = [terraform_data.capacity, terraform_data.api_key, time_sleep.iam, time_sleep.log_routing]
}

resource "google_cloud_run_v2_service_iam_member" "anyone_invokes_api" {
  name     = google_cloud_run_v2_service.api.name
  location = var.region
  role     = "roles/run.invoker"
  member   = "allUsers"
}
