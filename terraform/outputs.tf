output "url" {
  description = "Base URL of the API (JUDGE_URL)."
  value       = google_cloud_run_v2_service.api.uri
}

output "playground" {
  description = "Try every language in the browser."
  value       = "${google_cloud_run_v2_service.api.uri}/ui/"
}

output "admin" {
  description = "Analytics and temporary tokens. Needs the API key."
  value       = "${google_cloud_run_v2_service.api.uri}/ui/admin"
}

output "api_key_command" {
  description = "Prints the API key (X-Auth-Token). The key itself is not in the Terraform state."
  value       = "gcloud secrets versions access latest --secret ${google_secret_manager_secret.api_key.secret_id} --project ${var.project}"
}

output "capacity" {
  description = "vCPU the instance caps can use, against the regional quota."
  value       = "${local.vcpu_needed} of ${var.cpu_quota} vCPU (runners ${local.vcpu_runners}, API ${var.api_max_instances}, worker ${var.worker_max_instances}, rollouts ${local.rollout_reserve})"
}

output "languages" {
  description = "Language ids and the runner that serves each."
  value       = { for id, m in local.language_ids : id => "${m.title} (runner-${m.family})" }
}
