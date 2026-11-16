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

variable "allow_network" {
  description = "Let a submission ask for outbound network access with enable_network. Off by default: no sandbox then has a network. When on, such a run can reach any public host, so only admin keys may ask for it, plus temporary tokens created with that right."
  type        = bool
  default     = false
}

variable "network_time_limit" {
  description = "Seconds a run with network access may spend waiting on the network, on top of its wall time limit. A submission can ask for less or more with network_time_limit."
  type        = number
  default     = 30
}

variable "max_network_time_limit" {
  description = "The most a submission may ask for as network_time_limit, in seconds."
  type        = number
  default     = 60

  validation {
    condition     = var.max_network_time_limit >= 1 && var.max_network_time_limit <= 240
    error_message = "max_network_time_limit must be between 1 and 240 seconds: a runner request ends after 300."
  }
}

variable "max_download_mb" {
  description = "The most a run with network access may download, and the largest file it may write, in MB."
  type        = number
  default     = 125

  validation {
    condition     = var.max_download_mb >= 1 && var.max_download_mb <= 900
    error_message = "max_download_mb must be between 1 and 900: a download is held in the instance's memory."
  }
}

variable "delete_data_on_destroy" {
  description = "If true, terraform destroy also deletes the Firestore database (submissions and analytics). If false, the database is left in place."
  type        = bool
  default     = false
}
