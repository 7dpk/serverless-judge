# Images are built by Cloud Build (cloudbuild/build.sh), so nothing but
# gcloud is needed locally. Each tag is a hash of the files that go into the
# image: Terraform rebuilds an image, and rolls the service that uses it,
# only when those files change.

locals {
  repo = "${var.region}-docker.pkg.dev/${var.project}/${google_artifact_registry_repository.judge.repository_id}"

  # Tests, local build output and editor noise do not change an image.
  not_source = "(_test\\.go|\\.DS_Store|\\.test|\\.out)$|^(judge|jexec/jexec|api)$"

  dir_hash = {
    for d in concat(["runner", "api"], [for fam in keys(local.families) : "languages/${fam}"]) :
    d => sha256(join("\n", [
      for f in sort(tolist(fileset("${local.root}/${d}", "**"))) :
      "${f} ${filesha256("${local.root}/${d}/${f}")}" if !can(regex(local.not_source, f))
    ]))
  }

  prebuilds = { for fam, c in local.families : fam => c.prebuild if c.prebuild != null }

  image = {
    bin = "${local.repo}/judge-bin:${substr(local.dir_hash["runner"], 0, 12)}"
    api = "${local.repo}/judge-api:${substr(sha256("${local.dir_hash["api"]} ${local.dir_hash["runner"]}"), 0, 12)}"
    runner = {
      for fam in keys(local.families) :
      fam => "${local.repo}/runner-${fam}:${substr(sha256("${local.dir_hash["languages/${fam}"]} ${local.dir_hash["runner"]}"), 0, 12)}"
    }
    # A slow, rarely changing base (PostgreSQL built from source), hashed
    # over just the files named in service.json.
    prebuilt = {
      for fam, p in local.prebuilds :
      fam => "${local.repo}/${fam}-prebuilt:${substr(sha256(join("\n", [for f in p.inputs : filesha256("${local.root}/languages/${fam}/${f}")])), 0, 12)}"
    }
  }
}

# The judge binary and the launcher, copied into every runner image.
resource "terraform_data" "bin_image" {
  input            = local.image.bin
  triggers_replace = local.image.bin

  provisioner "local-exec" {
    working_dir = local.root
    command     = "cloudbuild/build.sh \"$IMAGE\" runner Dockerfile"
    environment = { PROJECT = var.project, IMAGE = self.input }
  }
}

resource "terraform_data" "prebuilt_image" {
  for_each         = local.prebuilds
  input            = local.image.prebuilt[each.key]
  triggers_replace = local.image.prebuilt[each.key]

  provisioner "local-exec" {
    working_dir = local.root
    command     = "cloudbuild/build.sh \"$IMAGE\" \"$CONTEXT\" \"$DOCKERFILE\" \"$MACHINE\""
    environment = {
      PROJECT    = var.project
      IMAGE      = self.input
      CONTEXT    = "languages/${each.key}"
      DOCKERFILE = each.value.dockerfile
      MACHINE    = try(each.value.machine_type, "")
    }
  }
}

resource "terraform_data" "runner_image" {
  for_each         = local.families
  input            = local.image.runner[each.key]
  triggers_replace = local.image.runner[each.key]

  provisioner "local-exec" {
    working_dir = local.root
    command     = "cloudbuild/build.sh \"$IMAGE\" \"$CONTEXT\" Dockerfile \"$MACHINE\" $BUILD_ARGS"
    environment = {
      PROJECT = var.project
      IMAGE   = self.input
      CONTEXT = "languages/${each.key}"
      MACHINE = each.value.machine_type
      BUILD_ARGS = join(" ", compact([
        "JUDGE_BIN=${terraform_data.bin_image.output}",
        contains(keys(local.prebuilds), each.key) ? "PREBUILT=${terraform_data.prebuilt_image[each.key].output}" : "",
      ]))
    }
  }
}

# One image, two services: the API and the worker.
resource "terraform_data" "api_image" {
  input            = local.image.api
  triggers_replace = local.image.api

  provisioner "local-exec" {
    working_dir = local.root
    command     = "cloudbuild/build.sh \"$IMAGE\" . api/Dockerfile"
    environment = { PROJECT = var.project, IMAGE = self.input }
  }
}
