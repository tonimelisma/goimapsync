#!/usr/bin/env bash
# Sets up pimvault's Google Cloud project for end-to-end tests (D193):
# enables the APIs, creates the test token secrets (D158) and a service
# account that may read them and add new versions, lets this repository's
# GitHub Actions workflows act as that account through Workload Identity
# Federation (D199), and prints the account's key for the cloud environment
# (the weekly sign-in, D195). Run it in Google Cloud Shell with the pimvault
# project selected.
set -euo pipefail

project=$(gcloud config get-value project 2>/dev/null)
if [ -z "${project}" ]; then
  echo "select the project first: gcloud config set project <project id>" >&2
  exit 1
fi

gcloud services enable gmail.googleapis.com calendar-json.googleapis.com people.googleapis.com \
  tasks.googleapis.com secretmanager.googleapis.com iam.googleapis.com \
  iamcredentials.googleapis.com sts.googleapis.com

account="pimvault-e2e@${project}.iam.gserviceaccount.com"
if ! gcloud iam service-accounts describe "${account}" >/dev/null 2>&1; then
  gcloud iam service-accounts create pimvault-e2e --display-name="pimvault end-to-end tests"
fi

for secret in e2e-gmail-token e2e-outlook-token; do
  if ! gcloud secrets describe "${secret}" >/dev/null 2>&1; then
    gcloud secrets create "${secret}" --replication-policy=automatic
  fi

  # Read versions, add new ones and destroy old ones, so refreshed tokens
  # replace earlier versions instead of piling up.
  for role in roles/secretmanager.secretAccessor roles/secretmanager.secretVersionManager; do
    gcloud secrets add-iam-policy-binding "${secret}" --member="serviceAccount:${account}" \
      --role="${role}" >/dev/null
  done
done

# Keyless credentials for GitHub Actions, only for this repository's workflows.
# Trust is by repository and owner ID, which survive a rename (D200), so a
# later repository reusing a name gets nothing. The provider ID goimapsync is
# the repository's earlier name.
repo=tonimelisma/pimvault
repo_id=1468242798
owner_id=59022391
if ! gcloud iam workload-identity-pools describe github --location=global >/dev/null 2>&1; then
  gcloud iam workload-identity-pools create github --location=global --display-name="GitHub Actions"
fi
if ! gcloud iam workload-identity-pools providers describe goimapsync --location=global \
  --workload-identity-pool=github >/dev/null 2>&1; then
  gcloud iam workload-identity-pools providers create-oidc goimapsync \
    --location=global --workload-identity-pool=github \
    --display-name="${repo}" \
    --issuer-uri="https://token.actions.githubusercontent.com" \
    --attribute-mapping="google.subject=assertion.sub,attribute.repository=assertion.repository,attribute.repository_id=assertion.repository_id" \
    --attribute-condition="assertion.repository_id == '${repo_id}' && assertion.repository_owner_id == '${owner_id}'"
fi
number=$(gcloud projects describe "${project}" --format='value(projectNumber)')
gcloud iam service-accounts add-iam-policy-binding "${account}" --role=roles/iam.workloadIdentityUser \
  --member="principalSet://iam.googleapis.com/projects/${number}/locations/global/workloadIdentityPools/github/attribute.repository_id/${repo_id}" \
  >/dev/null

echo
echo "Workload Identity provider for .github/workflows/e2e.yml:"
gcloud iam workload-identity-pools providers describe goimapsync --location=global \
  --workload-identity-pool=github --format='value(name)'

key=$(mktemp)
trap 'rm -f "${key}"' EXIT
gcloud iam service-accounts keys create "${key}" --iam-account="${account}" >/dev/null

echo
echo "Store this JSON as PIMVAULT_E2E_SERVICE_ACCOUNT in the cloud environment only (not in GitHub):"
echo
tr -d '\n' <"${key}"
echo
