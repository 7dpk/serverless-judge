terraform {
  required_version = ">= 1.5"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = ">= 8.11, < 9"
    }
    # The sandbox launcher is a preview feature, so it is only in the beta
    # provider (template.containers.sandbox_launcher). Verified with 8.11.0.
    google-beta = {
      source  = "hashicorp/google-beta"
      version = ">= 8.11, < 9"
    }
    time = {
      source  = "hashicorp/time"
      version = "~> 0.13"
    }
  }
}

# billing_project and user_project_override make API calls count against
# the project being deployed to, whatever quota project your local
# credentials happen to carry.
provider "google" {
  project               = var.project
  region                = var.region
  billing_project       = var.project
  user_project_override = true
}

provider "google-beta" {
  project               = var.project
  region                = var.region
  billing_project       = var.project
  user_project_override = true
}
