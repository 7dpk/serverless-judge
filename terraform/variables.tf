variable "project" {
  description = "Google Cloud project id to deploy into. Billing must be enabled."
  type        = string
}

variable "region" {
  description = "Cloud Run region. Every service, the image registry and (unless firestore_location is set) the database live here."
  type        = string
  default     = "us-central1"
}

variable "firestore_location" {
  description = "Firestore location, if it should differ from region (for example the multi-region nam5)."
  type        = string
  default     = null
}

variable "name_prefix" {
  description = "Put in front of every resource name. Leave empty for one judge per project; set it (for example \"staging-\") to run a second copy in the same project."
  type        = string
  default     = ""

  validation {
    condition     = can(regex("^([a-z][a-z0-9-]{0,7})?$", var.name_prefix))
    error_message = "name_prefix must be empty, or at most 8 characters of lowercase letters, digits and hyphens, starting with a letter."
  }
}

variable "families" {
  description = "Folders under languages/ to deploy. Leave unset for all of them. Example: [\"python\", \"gcc\"]."
  type        = set(string)
  default     = null
}

variable "max_instances" {
  description = "Instance cap per family, overriding max_instances in languages/<family>/service.json. Example: { gcc = 40 }."
  type        = map(number)
  default     = {}
}

variable "cpu_quota" {
  description = "Cloud Run \"Total CPU allocation, per project per region\" in vCPU. 200 unless Google raised it for you. The instance caps must fit inside it."
  type        = number
  default     = 200
}

variable "api_max_instances" {
  description = "Instance cap of the public API (1 vCPU each)."
  type        = number
  default     = 6
}

variable "api_min_instances" {
  description = "Instances of the API kept warm. 0 means no idle cost and a cold start of about a second after a quiet period."
  type        = number
  default     = 0
}

variable "worker_max_instances" {
  description = "Instance cap of the worker that runs queued submissions (1 vCPU each)."
  type        = number
  default     = 8
}

variable "submission_ttl_hours" {
  description = "How long a submission is kept. Firestore deletes expired documents within about a day, so 144 hours means nothing is stored beyond 7 days."
  type        = number
  default     = 144
}

variable "delete_data_on_destroy" {
  description = "If true, terraform destroy also deletes the Firestore database (submissions and analytics). If false, the database is left in place."
  type        = bool
  default     = false
}
