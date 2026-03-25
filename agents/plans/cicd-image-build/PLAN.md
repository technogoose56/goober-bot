# CI/CD Image Build - Implementation Plan

## Goal

Automate building and pushing the goober-bot Docker image to AWS ECR on every
push to `main`. This eliminates manual build/push steps and ensures the registry
always has a fresh image ready for deployment.

---

## Current State

| Aspect | Detail |
|---|---|
| Container registry | AWS ECR, repo name `goober-bot`, region `us-east-1` |
| ECR URL format | `<account_id>.dkr.ecr.us-east-1.amazonaws.com/goober-bot` |
| Dockerfile | Multi-stage build, `CGO_ENABLED=0`, final `scratch` image |
| EC2 fleet | Mixed ARM64 (`t4g`) and x86_64 (`t3a`, `t3`) spot instances |
| GitHub Actions | No workflows exist yet |
| IAM (GitHub) | No OIDC provider or push role exists in aws-infra |

### Architecture concern

The spot fleet uses both ARM64 (`t4g.nano`, `t4g.micro`) and x86_64
(`t3a.nano`, `t3.nano`) instance types. A single-arch image will fail to run
on instances of the other architecture. Because `CGO_ENABLED=0` is already set,
Go cross-compilation is trivial. The workflow should produce a **multi-arch
manifest** (`linux/amd64` + `linux/arm64`) using `docker buildx`.

---

## Architecture

```
aws-infra/
  modules/
    github-actions-iam/       New module: OIDC provider + push IAM role
      main.tf
      variables.tf
      outputs.tf
  envs/prod/
    main.tf                   Wire in the new module

goober-bot/
  .github/
    workflows/
      build-and-push.yml      GitHub Actions workflow
```

### Workflow overview

```
Push to main
  -> Checkout
  -> Run tests (go test ./...)
  -> Configure AWS credentials via OIDC (no stored secrets)
  -> Log in to ECR
  -> Set up Docker Buildx
  -> Build multi-arch image (linux/amd64 + linux/arm64)
  -> Push with two tags: :latest and :<git-short-sha>
```

---

## Step-by-Step Implementation

### Phase 1 - GitHub Actions IAM (aws-infra)

GitHub Actions authenticates to AWS using **OIDC** — no long-lived AWS access
keys are stored as GitHub secrets. This requires two resources:

1. **IAM OIDC identity provider** for `token.actions.githubusercontent.com`
   (one per AWS account, shared across all repos).
2. **IAM role** that GitHub Actions can assume, with a trust policy scoped to
   the `goober-bot` repo on the `main` branch.

#### New module: `modules/github-actions-iam/`

**`variables.tf`**
| Variable | Description |
|---|---|
| `github_repo` | `org/repo` string used to scope the trust policy (e.g. `camloren56/goober-bot`) |
| `ecr_repo_arn` | ARN of the ECR repository to allow pushes to |
| `project_name` | Used for resource naming |

**`main.tf`** resources:
- `aws_iam_openid_connect_provider.github` — OIDC provider for
  `token.actions.githubusercontent.com` with thumbprint
  `6938fd4d98bab03faadb97b34396831e3780aea`
- `aws_iam_role.github_actions` — assume-role trust policy scoped to
  `repo:<github_repo>:ref:refs/heads/main`
- `aws_iam_role_policy.ecr_push` — inline policy granting:
  ```
  ecr:GetAuthorizationToken          (resource: *)
  ecr:BatchCheckLayerAvailability
  ecr:GetDownloadUrlForLayer
  ecr:BatchGetImage
  ecr:PutImage
  ecr:InitiateLayerUpload
  ecr:UploadLayerPart
  ecr:CompleteLayerUpload            (resource: ecr_repo_arn)
  ```

**`outputs.tf`**
- `role_arn` — the IAM role ARN; used as a GitHub Actions secret

#### Wire into `envs/prod/main.tf`

Add a module block:
```hcl
module "github_actions_iam" {
  source = "../../modules/github-actions-iam"

  project_name  = var.project_name
  github_repo   = var.github_actions_repo
  ecr_repo_arn  = module.ecr.repository_arn
}
```

Add `github_actions_repo` variable to `variables.tf` (e.g. default
`"camloren56/goober-bot"`).

Add `github_actions_role_arn` output to `outputs.tf`.

### Phase 2 - GitHub Actions workflow (goober-bot)

File: `.github/workflows/build-and-push.yml`

```yaml
name: Build and Push

on:
  push:
    branches: [main]

permissions:
  id-token: write   # required for OIDC
  contents: read

env:
  AWS_REGION: us-east-1
  ECR_REPO: goober-bot

jobs:
  build-and-push:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Set up Go
        uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
          cache: true

      - name: Run tests
        run: go test ./...

      - name: Configure AWS credentials
        uses: aws-actions/configure-aws-credentials@v4
        with:
          role-to-assume: ${{ secrets.AWS_GITHUB_ACTIONS_ROLE_ARN }}
          aws-region: ${{ env.AWS_REGION }}

      - name: Log in to ECR
        id: ecr-login
        uses: aws-actions/amazon-ecr-login@v2

      - name: Set up Docker Buildx
        uses: docker/setup-buildx-action@v3

      - name: Read version
        id: version
        run: echo "value=$(cat VERSION)" >> "$GITHUB_OUTPUT"

      - name: Build and push
        uses: docker/build-push-action@v6
        with:
          context: .
          platforms: linux/amd64,linux/arm64
          push: true
          tags: |
            ${{ steps.ecr-login.outputs.registry }}/${{ env.ECR_REPO }}:latest
            ${{ steps.ecr-login.outputs.registry }}/${{ env.ECR_REPO }}:${{ github.sha }}
            ${{ steps.ecr-login.outputs.registry }}/${{ env.ECR_REPO }}:${{ steps.version.outputs.value }}
          cache-from: type=gha
          cache-to: type=gha,mode=max
```

Key design decisions:
- Tests run before the build. A failing test aborts the workflow before any
  image is pushed.
- `cache-from/cache-to: type=gha` reuses GitHub Actions layer cache across
  runs to speed up builds (especially the Go module download layer).
- The git SHA tag (`github.sha`) allows the EC2 user-data script to pin to a
  specific image version if needed, while `:latest` is the default pull target.

### Phase 3 - GitHub secret

After applying the Terraform changes, add one repository secret in GitHub:

| Secret name | Value |
|---|---|
| `AWS_GITHUB_ACTIONS_ROLE_ARN` | Output from `terraform output github_actions_role_arn` |

No AWS access keys are stored. The OIDC token is ephemeral and scoped to the
exact repo and branch.

### Phase 4 - ECR image tag mutability

The existing ECR repo uses `MUTABLE` tags (see `modules/ecr/main.tf`), which
allows `:latest` to be overwritten on each push. This is correct behavior for
this workflow and requires no change.

---

## Files Changed Summary

| Repo | Action | File |
|---|---|---|
| `aws-infra` | Create | `modules/github-actions-iam/main.tf` |
| `aws-infra` | Create | `modules/github-actions-iam/variables.tf` |
| `aws-infra` | Create | `modules/github-actions-iam/outputs.tf` |
| `aws-infra` | Modify | `envs/prod/main.tf` (add module block) |
| `aws-infra` | Modify | `envs/prod/variables.tf` (add `github_actions_repo`) |
| `aws-infra` | Modify | `envs/prod/outputs.tf` (add `github_actions_role_arn`) |
| `goober-bot` | Create | `.github/workflows/build-and-push.yml` |

---

## Implementation Status

| Phase | Status | Notes |
|---|---|---|
| Phase 1 - GitHub Actions IAM (aws-infra) | Done | `modules/github-actions-iam/` created; wired into `envs/prod/` with `github_actions_repo` variable (default `technogoose56/goober-bot`) and `github_actions_role_arn` output |
| Phase 2 - GitHub Actions workflow (goober-bot) | Done | `.github/workflows/build-and-push.yml` created; runs tests, then builds and pushes `:latest`, `:<git-sha>`, `:<version>` tags as multi-arch (amd64 + arm64) |
| Phase 3 - GitHub secret | Not started | Run `terraform apply` in `envs/prod/`, then set `AWS_GITHUB_ACTIONS_ROLE_ARN` in GitHub repo secrets to the value of `terraform output github_actions_role_arn` |
| Phase 4 - ECR tag mutability | N/A | Already correct |

---

## Future Enhancements (Out of Scope)

- Notify the running EC2 instance to pull and restart on new image push
  (e.g. via SSM Run Command triggered at end of workflow).
- Add a separate workflow to run tests on PRs (without pushing an image).
- Sign images with Cosign for supply chain verification.
